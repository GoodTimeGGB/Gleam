package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"gleam/internal/agent"
	"gleam/internal/buildinfo"
	"gleam/internal/harness/skill"
	"gleam/pkg/types"
)

// Service 目标模式服务：把 Agent 能力暴露为 JSON-RPC 方法。
type Service struct {
	Agent *agent.Agent
	Conn  *Conn

	mu    sync.Mutex
	tasks map[string]*taskEntry
}

type taskEntry struct {
	ID      string            `json:"task_id"`
	Goal    string            `json:"goal"`
	Status  types.GoalStatus  `json:"status"`
	Mode    string            `json:"mode"`
	Result  *types.GoalResult `json:"result,omitempty"`
	Started time.Time         `json:"started_at"`
	Cancel  func()
}

func (e *taskEntry) snapshot(includeResult bool) map[string]any {
	out := map[string]any{
		"task_id": e.ID, "goal": e.Goal, "status": string(e.Status),
		"mode": e.Mode, "started_at": e.Started,
	}
	if includeResult && e.Result != nil {
		out["result"] = e.Result
	}
	return out
}

// NewService 创建服务。
func NewService(a *agent.Agent) *Service {
	return &Service{Agent: a, tasks: map[string]*taskEntry{}}
}

// Bind 注册全部方法到连接，并把服务挂为 Agent 的事件出口。
func (s *Service) Bind(c *Conn) {
	s.Conn = c
	s.Agent.Notifier = s
	m := map[string]HandlerMethod{
		"initialize":      s.handleInitialize,
		"ping":            s.handlePing,
		"goal/submit":     s.handleGoalSubmit,
		"goal/status":     s.handleGoalStatus,
		"goal/cancel":     s.handleGoalCancel,
		"goal/list":       s.handleGoalList,
		"tools/list":      s.handleToolsList,
		"tools/call":      s.handleToolsCall,
		"memory/search":   s.handleMemorySearch,
		"memory/save":     s.handleMemorySave,
		"skills/list":     s.handleSkillsList,
		"skills/get":      s.handleSkillGet,
		"skills/save":     s.handleSkillSave,
		"skills/run":      s.handleSkillRun,
		"skills/delete":   s.handleSkillDelete,
		"schedule/create": s.handleScheduleCreate,
		"schedule/notify": s.handleScheduleNotify,
		"schedule/list":   s.handleScheduleList,
		"schedule/delete": s.handleScheduleDelete,
		"shutdown":        s.handleShutdown,
	}
	for name, h := range m {
		c.Handle(name, h)
	}
}

// ---------- 基础 ----------

func (s *Service) handleInitialize(_ context.Context, _ json.RawMessage) (any, *RPCError) {
	tools := s.Agent.Reg.Names()
	return map[string]any{
		"name":    "gleam",
		"version": buildinfo.Version,
		"model":   s.Agent.LLM.Name(),
		"capabilities": map[string]any{
			"goal":      true, // 目标模式
			"tools":     true,
			"skills":    true,
			"memory":    true,
			"scheduler": s.Agent.Sched != nil,
		},
		"tools": tools,
	}, nil
}

func (s *Service) handlePing(_ context.Context, _ json.RawMessage) (any, *RPCError) {
	return map[string]any{"pong": true}, nil
}

func (s *Service) handleShutdown(_ context.Context, _ json.RawMessage) (any, *RPCError) {
	go func() {
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)
	}()
	return map[string]any{"bye": true}, nil
}

// ---------- 目标模式 ----------

type goalSubmitParams struct {
	References []types.Reference `json:"references"`
	Role       string            `json:"role"`
	Goal       string            `json:"goal"`
	Context    map[string]any    `json:"context"`
	Mode       string            `json:"mode"`
	TaskID     string            `json:"task_id"`
	TaskMode   string            `json:"task_mode"`
}

func (s *Service) handleGoalSubmit(_ context.Context, params json.RawMessage) (any, *RPCError) {
	var p goalSubmitParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, Errf(CodeInvalidParams, "参数解析失败: %v", err)
	}
	if len([]rune(p.Goal)) < 2 {
		return nil, Errf(CodeInvalidParams, "goal 不能为空")
	}
	// task_id 先补齐再校验：形状规则住在 ValidateGoalRequest（它会变成归档的文件名），
	// 顺序反了就等于把外部传入的 id 放过了这一关。
	taskID := p.TaskID
	if taskID == "" {
		taskID = types.NewID()
	}
	req := types.GoalRequest{Goal: p.Goal, Context: p.Context, References: p.References, Mode: p.Mode, TaskMode: types.TaskMode(p.TaskMode), Role: p.Role, TaskID: taskID}
	if err := agent.ValidateGoalRequest(req); err != nil {
		return nil, Errf(CodeInvalidParams, "%v", err)
	}
	entry := &taskEntry{ID: taskID, Goal: p.Goal, Status: types.GoalRunning, Mode: p.Mode, Started: time.Now()}
	s.mu.Lock()
	s.tasks[taskID] = entry
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	entry.Cancel = cancel
	go func() {
		defer cancel()
		result := s.Agent.RunGoal(ctx, req)
		s.mu.Lock()
		entry.Status = result.Status
		entry.Result = result
		s.pruneTasksLocked()
		s.mu.Unlock()
		// 持久化工作记忆（归档只有一个出口，见 agent.SaveTaskResult）
		s.saveTaskResult(result)
		s.Conn.Notify("goal/completed", result)
	}()
	return map[string]any{"task_id": taskID, "status": string(types.GoalRunning)}, nil
}

type taskIDParams struct {
	TaskID string `json:"task_id"`
}

func (s *Service) handleGoalStatus(_ context.Context, params json.RawMessage) (any, *RPCError) {
	var p taskIDParams
	if err := json.Unmarshal(params, &p); err != nil || p.TaskID == "" {
		return nil, Errf(CodeInvalidParams, "需要 task_id")
	}
	s.mu.Lock()
	entry, ok := s.tasks[p.TaskID]
	if !ok {
		s.mu.Unlock()
		return nil, Errf(CodeMethodNotFound+1000, "任务 %q 不存在", p.TaskID)
	}
	out := entry.snapshot(true)
	s.mu.Unlock()
	return out, nil
}

func (s *Service) handleGoalCancel(_ context.Context, params json.RawMessage) (any, *RPCError) {
	var p taskIDParams
	if err := json.Unmarshal(params, &p); err != nil || p.TaskID == "" {
		return nil, Errf(CodeInvalidParams, "需要 task_id")
	}
	s.mu.Lock()
	entry, ok := s.tasks[p.TaskID]
	if ok && entry.Status == types.GoalRunning && entry.Cancel != nil {
		entry.Cancel()
	}
	s.mu.Unlock()
	// 交给 Agent 二次保险
	_ = s.Agent.CancelTask(p.TaskID)
	return map[string]any{"task_id": p.TaskID, "cancelled": ok}, nil
}

func (s *Service) handleGoalList(_ context.Context, _ json.RawMessage) (any, *RPCError) {
	s.mu.Lock()
	list := make([]map[string]any, 0, len(s.tasks))
	for _, e := range s.tasks {
		list = append(list, e.snapshot(false))
	}
	s.mu.Unlock()
	sort.Slice(list, func(i, j int) bool {
		return list[i]["started_at"].(time.Time).After(list[j]["started_at"].(time.Time))
	})
	return map[string]any{"count": len(list), "tasks": list}, nil
}

// maxRetainedTasks 已完成任务保留上限（长期运行防内存无限增长）。
const maxRetainedTasks = 200

// pruneTasksLocked 淘汰最旧的已完成任务（须持有 s.mu）。
func (s *Service) pruneTasksLocked() {
	if len(s.tasks) <= maxRetainedTasks {
		return
	}
	type rec struct {
		id      string
		started time.Time
	}
	var done []rec
	for id, e := range s.tasks {
		if e.Status != types.GoalRunning {
			done = append(done, rec{id, e.Started})
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i].started.Before(done[j].started) })
	for i := 0; i < len(done) && len(s.tasks) > maxRetainedTasks; i++ {
		delete(s.tasks, done[i].id)
	}
}

// saveTaskResult 把任务结果归档到数据目录（工作记忆）。写入本身住在 agent.SaveTaskResult
// ——CLI 与常驻服务共一个出口，"写成了才删运行日志"这条顺序才只有一处需要维护。
func (s *Service) saveTaskResult(result *types.GoalResult) {
	if err := agent.SaveTaskResult(s.Agent.Cfg.DataDir, result); err != nil {
		// 归档失败不打断流程，但必须留下声音：runs/ 里因此保着运行日志，
		// 不说的话没人知道这条任务在 tasks/ 里其实查不到。
		fmt.Fprintf(os.Stderr, "[gleam] 任务未存档：%v\n", err)
	}
}

// ---------- 工具 ----------

func (s *Service) handleToolsList(_ context.Context, _ json.RawMessage) (any, *RPCError) {
	list := make([]map[string]any, 0)
	for _, t := range s.Agent.Reg.List() {
		list = append(list, map[string]any{
			"name": t.Name(), "description": t.Description(),
			"permission": t.Permission().String(), "schema": t.Schema(),
		})
	}
	return map[string]any{"count": len(list), "tools": list}, nil
}

type toolCallParams struct {
	Name       string         `json:"name"`
	Args       map[string]any `json:"args"`
	TimeoutSec int            `json:"timeout_sec"`
}

// handleToolsCall 直接调用工具；非只读工具同样经过安全门控审批。
func (s *Service) handleToolsCall(_ context.Context, params json.RawMessage) (any, *RPCError) {
	var p toolCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, Errf(CodeInvalidParams, "参数解析失败: %v", err)
	}
	tool, ok := s.Agent.Reg.Get(p.Name)
	if !ok {
		return nil, Errf(CodeInvalidParams, "工具 %q 不存在", p.Name)
	}
	dec := s.Agent.Gate.Evaluate(tool, p.Args)
	if dec.NeedApproval {
		req := types.ApprovalRequest{Plan: []string{fmt.Sprintf("直接调用工具 %s", tool.Name())}, Risk: dec.Risk, Reason: dec.Reason}
		resp := s.OnApproval(req)
		if !resp.Approved {
			return nil, Errf(CodeInvalidParams, "操作被拒绝: %s", resp.Note)
		}
	}
	timeout := time.Duration(p.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := tool.Execute(ctx, p.Args)
	if err != nil {
		return nil, Errf(CodeInternal, "工具执行失败: %v", err)
	}
	return map[string]any{"name": tool.Name(), "output": out}, nil
}

// ---------- 记忆 ----------

func (s *Service) handleMemorySearch(_ context.Context, params json.RawMessage) (any, *RPCError) {
	var p struct {
		Query string `json:"query"`
		K     int    `json:"k"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Query == "" {
		return nil, Errf(CodeInvalidParams, "需要 query")
	}
	hits := s.Agent.Mem.Relevant(p.Query, p.K)
	return map[string]any{"count": len(hits), "hits": hits}, nil
}

func (s *Service) handleMemorySave(_ context.Context, params json.RawMessage) (any, *RPCError) {
	var p struct {
		Content string   `json:"content"`
		Tags    []string `json:"tags"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Content == "" {
		return nil, Errf(CodeInvalidParams, "需要 content")
	}
	id, err := s.Agent.Mem.Remember(p.Content, p.Tags)
	if err != nil {
		return nil, Errf(CodeInternal, "保存失败: %v", err)
	}
	return map[string]any{"id": id, "saved": true}, nil
}

// ---------- 技能 ----------

func (s *Service) handleSkillsList(_ context.Context, _ json.RawMessage) (any, *RPCError) {
	list := s.Agent.SkillList()
	return map[string]any{"count": len(list), "skills": list}, nil
}

func (s *Service) handleSkillGet(_ context.Context, params json.RawMessage) (any, *RPCError) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
		return nil, Errf(CodeInvalidParams, "需要 name")
	}
	sk, err := s.Agent.SkillGet(p.Name)
	if err != nil {
		return nil, Errf(CodeInvalidParams, "%v", err)
	}
	return sk, nil
}

func (s *Service) handleSkillSave(_ context.Context, params json.RawMessage) (any, *RPCError) {
	var p struct {
		Name        string       `json:"name"`
		Description string       `json:"description"`
		Params      []string     `json:"params"`
		Steps       []types.Step `json:"steps"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, Errf(CodeInvalidParams, "参数解析失败: %v", err)
	}
	// 走门面而不是直摸 Skills：技能入库的成长事件只在门面那一处记，绕过去就是漏项
	version, err := s.Agent.SkillSave(skill.Skill{
		Name: p.Name, Description: p.Description, Params: p.Params, Steps: p.Steps,
	})
	if err != nil {
		return nil, Errf(CodeInvalidParams, "保存失败: %v", err)
	}
	return map[string]any{"name": p.Name, "version": version, "saved": true}, nil
}

func (s *Service) handleSkillRun(_ context.Context, params json.RawMessage) (any, *RPCError) {
	var p struct {
		Name   string            `json:"name"`
		Params map[string]string `json:"params"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
		return nil, Errf(CodeInvalidParams, "需要 name")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := s.Agent.RunSkill(ctx, p.Name, p.Params)
	if err != nil {
		return nil, Errf(CodeInternal, "技能执行失败: %v", err)
	}
	return out, nil
}

func (s *Service) handleSkillDelete(_ context.Context, params json.RawMessage) (any, *RPCError) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
		return nil, Errf(CodeInvalidParams, "需要 name")
	}
	if err := s.Agent.Skills.Delete(p.Name); err != nil {
		return nil, Errf(CodeInvalidParams, "%v", err)
	}
	return map[string]any{"name": p.Name, "deleted": true}, nil
}

// ---------- 调度 ----------

func (s *Service) handleScheduleCreate(_ context.Context, params json.RawMessage) (any, *RPCError) {
	var p struct {
		Name        string `json:"name"`
		Goal        string `json:"goal"`
		When        string `json:"when"`
		Cron        string `json:"cron"`
		IntervalSec int    `json:"interval_sec"`
		Mode        string `json:"mode"`
		Notify      string `json:"notify"` // always / on_failure / never（空 = 默认 on_failure）
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, Errf(CodeInvalidParams, "参数解析失败: %v", err)
	}
	if s.Agent.Sched == nil {
		return nil, Errf(CodeInternal, "调度器未启用")
	}
	view, err := s.Agent.ScheduleCreateWithNotify(p.Name, p.Goal, p.Mode, p.When, p.Cron, p.IntervalSec, p.Notify)
	if err != nil {
		return nil, Errf(CodeInvalidParams, "%v", err)
	}
	return view, nil
}

// handleScheduleNotify 修改定时任务的通知策略（always / on_failure / never；空串恢复默认）。
func (s *Service) handleScheduleNotify(_ context.Context, params json.RawMessage) (any, *RPCError) {
	var p struct {
		Name   string `json:"name"`
		Notify string `json:"notify"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, Errf(CodeInvalidParams, "参数解析失败: %v", err)
	}
	view, err := s.Agent.ScheduleSetNotify(p.Name, p.Notify)
	if err != nil {
		return nil, Errf(CodeInvalidParams, "%v", err)
	}
	return view, nil
}

func (s *Service) handleScheduleList(_ context.Context, _ json.RawMessage) (any, *RPCError) {
	if s.Agent.Sched == nil {
		return map[string]any{"count": 0, "jobs": []any{}}, nil
	}
	jobs := s.Agent.Sched.ListJobs()
	return map[string]any{"count": len(jobs), "jobs": jobs}, nil
}

func (s *Service) handleScheduleDelete(_ context.Context, params json.RawMessage) (any, *RPCError) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
		return nil, Errf(CodeInvalidParams, "需要 name")
	}
	if s.Agent.Sched == nil {
		return nil, Errf(CodeInternal, "调度器未启用")
	}
	ok, err := s.Agent.Sched.DeleteJob(p.Name)
	if err != nil {
		return nil, Errf(CodeInternal, "%v", err)
	}
	if !ok {
		return nil, Errf(CodeInvalidParams, "任务 %q 不存在", p.Name)
	}
	return map[string]any{"name": p.Name, "deleted": true}, nil
}

// ---------- agent.Notifier 桥接（推送给对端） ----------

// OnProgress 实现 agent.Notifier。
func (s *Service) OnProgress(ev types.ProgressEvent) {
	if s.Conn != nil {
		s.Conn.Notify("goal/progress", ev)
	}
}

// OnApproval 实现 agent.Notifier：通过 goal/ask_approval 请求宿主确认。
func (s *Service) OnApproval(req types.ApprovalRequest) types.ApprovalResponse {
	if s.Conn == nil {
		return types.ApprovalResponse{Approved: false, Note: "无审批通道"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.Agent.Gate.ApprovalTimeoutDuration())
	defer cancel()
	var resp types.ApprovalResponse
	if err := s.Conn.Call(ctx, "goal/ask_approval", req, &resp); err != nil {
		return types.ApprovalResponse{Approved: false, Note: err.Error()}
	}
	return resp
}

// OnSuggestion 实现 agent.Notifier。
func (s *Service) OnSuggestion(taskID, text string) {
	if s.Conn != nil {
		s.Conn.Notify("agent/suggestion", map[string]any{"task_id": taskID, "text": text})
	}
}

// OnSuggestSkill 实现 agent.Notifier。
func (s *Service) OnSuggestSkill(taskID string, draft types.SkillDraft) {
	if s.Conn != nil {
		s.Conn.Notify("agent/suggest_skill", map[string]any{"task_id": taskID, "skill": draft})
	}
}

// OnTaskDone 实现 agent.Notifier：后台任务跑完推给对端（编辑器插件可以据此弹通知）。
//
// 与 goal/progress 分开推：进度是流，完成是一次性事实——插件侧对两者的处理不同
// （前者刷新进度条，后者该提醒用户）。混在一个通道里，插件就得自己判断"这条是不是最后一条"，
// 而那正是最容易漏掉失败的分叉点。
func (s *Service) OnTaskDone(ev types.TaskDoneEvent) {
	if s.Conn != nil {
		s.Conn.Notify("goal/done", ev)
	}
}
