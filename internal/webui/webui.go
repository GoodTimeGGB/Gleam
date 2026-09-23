// Package webui 实现 Gleam 的 Web UI 传输层：
// HTTP REST API + SSE 实时事件流 + 审批等待器 + 内嵌静态前端（go:embed）。
// 与 stdio JSON-RPC 服务共享同一个 agent.Agent 引擎。
package webui

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"gleam/internal/agent"
	"gleam/pkg/types"
)

// sseEvent 推送给浏览器的事件。
type sseEvent struct {
	Type string          `json:"type"` // progress | approval | completed | suggestion | suggest_skill
	Data json.RawMessage `json:"data"`
}

func newEvent(typ string, payload any) sseEvent {
	b, _ := json.Marshal(payload)
	return sseEvent{Type: typ, Data: b}
}

// Server Web UI 服务。实现 agent.Notifier，把引擎事件扇出给全部 SSE 客户端。
type Server struct {
	Agent *agent.Agent

	// ShowWindowFunc 桌面模式注入：/api/show-window 被调用时弹出主窗口
	// （单实例场景下，第二个进程通过该端点唤起已有实例的窗口）。
	ShowWindowFunc func()

	mu        sync.Mutex
	tasks     map[string]*taskInfo
	approvals map[string]*approvalWaiter
	clients   map[chan sseEvent]struct{}
}

type taskInfo struct {
	ID      string             `json:"task_id"`
	Goal    string             `json:"goal"`
	Mode    string             `json:"mode"`
	Status  types.GoalStatus   `json:"status"`
	Result  *types.GoalResult  `json:"result,omitempty"`
	Events  []sseEvent         `json:"events,omitempty"`
	Started time.Time          `json:"started_at"`
	Cancel  context.CancelFunc `json:"-"` // 不可序列化
}

// snapshot 返回可在锁外编码的只读任务副本。
func (t *taskInfo) snapshot() *taskInfo {
	copy := *t
	copy.Cancel = nil
	copy.Events = append([]sseEvent(nil), t.Events...)
	return &copy
}

type approvalWaiter struct {
	ID        string                `json:"id"`
	Req       types.ApprovalRequest `json:"request"`
	ch        chan types.ApprovalResponse
	CreatedAt time.Time `json:"created_at"`
}

// NewServer 创建 Web UI 服务并接管引擎事件出口。
func NewServer(a *agent.Agent) *Server {
	s := &Server{
		Agent:     a,
		tasks:     map[string]*taskInfo{},
		approvals: map[string]*approvalWaiter{},
		clients:   map[chan sseEvent]struct{}{},
	}
	a.SetNotifier(s)
	return s
}

// ---------- agent.Notifier 实现 ----------

// OnProgress 实现 agent.Notifier。
func (s *Server) OnProgress(ev types.ProgressEvent) {
	s.mu.Lock()
	if t, ok := s.tasks[ev.TaskID]; ok {
		t.appendEvent(newEvent("progress", ev))
	}
	s.mu.Unlock()
	s.broadcast(newEvent("progress", ev))
}

// OnApproval 实现 agent.Notifier：注册等待器、SSE 通知前端、阻塞等待裁决或超时。
func (s *Server) OnApproval(req types.ApprovalRequest) types.ApprovalResponse {
	w := &approvalWaiter{
		ID:        "apr-" + types.NewID(),
		Req:       req,
		ch:        make(chan types.ApprovalResponse, 1),
		CreatedAt: time.Now(),
	}
	s.mu.Lock()
	s.approvals[w.ID] = w
	if t, ok := s.tasks[req.TaskID]; ok {
		t.appendEvent(newEvent("approval", w.view()))
	}
	s.mu.Unlock()
	s.broadcast(newEvent("approval", w.view()))

	timer := time.NewTimer(s.Agent.ApprovalTimeout())
	defer timer.Stop()
	select {
	case resp := <-w.ch:
		return resp
	case <-timer.C:
		s.resolve(w.ID, types.ApprovalResponse{Approved: false, Note: "审批超时，自动拒绝"})
		return types.ApprovalResponse{Approved: false, Note: "审批超时，自动拒绝"}
	}
}

// OnSuggestion 实现 agent.Notifier。
func (s *Server) OnSuggestion(taskID, text string) {
	payload := map[string]any{"task_id": taskID, "text": text}
	s.mu.Lock()
	if t, ok := s.tasks[taskID]; ok {
		t.appendEvent(newEvent("suggestion", payload))
	}
	s.mu.Unlock()
	s.broadcast(newEvent("suggestion", payload))
}

// OnSuggestSkill 实现 agent.Notifier。
func (s *Server) OnSuggestSkill(taskID string, draft types.SkillDraft) {
	payload := map[string]any{"task_id": taskID, "skill": draft}
	s.mu.Lock()
	if t, ok := s.tasks[taskID]; ok {
		t.appendEvent(newEvent("suggest_skill", payload))
	}
	s.mu.Unlock()
	s.broadcast(newEvent("suggest_skill", payload))
}

// OnTaskDone 实现 agent.Notifier：后台任务（定时 / 变化触发）跑完。
//
// 除了广播事件，还要把它落进任务表：用户可能几小时后才打开窗口，那时 SSE 早就断了，
// 能看到的只有任务列表与时间线里的这一条。**只广播不落盘，等于"当时不在场就永远
// 不知道"**——而那恰好是定时任务的常态（跑的时候没人在看）。
//
// **为什么要补登记**：定时任务不是从 `/api/goals` 提交的，`s.tasks` 里从来没有它。
// 只做 `if t, ok := ...` 的话，落盘那段在定时任务场景下**一次都不会执行**——
// 看起来写了持久化，实际只剩广播。这是"判据对但线没接"的另一种形态：
// 判断写对了，前提假设错了。
func (s *Server) OnTaskDone(ev types.TaskDoneEvent) {
	payload := map[string]any{"task_id": ev.TaskID, "origin": ev.Origin, "title": ev.Title(), "line": ev.Line(), "result": ev.Result}
	s.mu.Lock()
	t, ok := s.tasks[ev.TaskID]
	if !ok {
		started := ev.Result.StartedAt
		if started.IsZero() {
			started = time.Now()
		}
		t = &taskInfo{ID: ev.TaskID, Goal: ev.Result.Goal, Status: ev.Result.Status, Started: started}
		res := ev.Result
		t.Result = &res
		s.tasks[ev.TaskID] = t
	}
	t.appendEvent(newEvent("task_done", payload))
	// 后台任务也会往这张表里加人，同样要裁剪，否则长期运行会无界增长。
	s.pruneTasksLocked(ev.TaskID)
	s.mu.Unlock()
	s.broadcast(newEvent("task_done", payload))
}

// ---------- SSE ----------

func (s *Server) subscribe() chan sseEvent {
	ch := make(chan sseEvent, 256)
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	pending := make([]sseEvent, 0, len(s.approvals))
	for _, w := range s.approvals {
		pending = append(pending, newEvent("approval", w.view()))
	}
	s.mu.Unlock()
	// 新订阅者先补发未决审批
	for _, ev := range pending {
		select {
		case ch <- ev:
		default:
		}
	}
	return ch
}

func (s *Server) unsubscribe(ch chan sseEvent) {
	s.mu.Lock()
	delete(s.clients, ch)
	s.mu.Unlock()
}

func (s *Server) broadcast(ev sseEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- ev:
		default: // 慢客户端丢弃事件，避免阻塞引擎
		}
	}
}

// ---------- 审批裁决 ----------

// resolve 裁决审批请求；返回是否存在。
func (s *Server) resolve(id string, resp types.ApprovalResponse) bool {
	s.mu.Lock()
	w, ok := s.approvals[id]
	if ok {
		delete(s.approvals, id)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case w.ch <- resp:
	default:
	}
	return true
}

func (w *approvalWaiter) view() map[string]any {
	return map[string]any{
		"id":         w.ID,
		"task_id":    w.Req.TaskID,
		"plan":       w.Req.Plan,
		"risk":       w.Req.Risk,
		"reason":     w.Req.Reason,
		"step_id":    w.Req.StepID,
		"whole_plan": w.Req.WholePlan,
		"created_at": w.CreatedAt,
	}
}

// appendEvent 追加事件到任务日志（cap 300）。
func (t *taskInfo) appendEvent(ev sseEvent) {
	if len(t.Events) >= 300 {
		t.Events = t.Events[1:]
	}
	t.Events = append(t.Events, ev)
}

// PendingApprovals 未决审批数量（桌面端闲置退出前的安全检查）。
func (s *Server) PendingApprovals() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.approvals)
}
