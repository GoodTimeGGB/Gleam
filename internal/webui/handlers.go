package webui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gleam/internal/agent"
	"gleam/internal/harness/skill"
	"gleam/pkg/types"
)

//go:embed static
var staticFS embed.FS

// Handler 返回完整的 HTTP 路由（含内嵌静态前端）。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// 静态前端
	sub, _ := fs.Sub(staticFS, "static")
	fileServer := http.FileServer(http.FS(sub))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
	mux.Handle("GET /assets/", noCache(http.StripPrefix("/assets/", fileServer)))

	// 信息与事件流
	mux.HandleFunc("GET /api/info", s.handleInfo)
	mux.HandleFunc("GET /api/events", s.handleEvents)

	// 目标
	mux.HandleFunc("POST /api/goals", s.handleGoalSubmit)
	mux.HandleFunc("GET /api/goals", s.handleGoalList)
	mux.HandleFunc("GET /api/goals/{id}", s.handleGoalGet)
	mux.HandleFunc("POST /api/goals/{id}/cancel", s.handleGoalCancel)

	// 审批
	mux.HandleFunc("GET /api/approvals", s.handleApprovalList)
	mux.HandleFunc("POST /api/approvals/{id}", s.handleApprovalResolve)

	// 工具
	mux.HandleFunc("GET /api/tools", s.handleToolsList)
	mux.HandleFunc("POST /api/tools/call", s.handleToolCall)
	mux.HandleFunc("POST /api/tools/permission", s.handleToolPermission)

	// 记忆
	mux.HandleFunc("GET /api/memory", s.handleMemorySearch)
	mux.HandleFunc("POST /api/memory", s.handleMemorySave)
	mux.HandleFunc("DELETE /api/memory/{id}", s.handleMemoryDelete)

	// 技能
	mux.HandleFunc("GET /api/skills", s.handleSkillsList)
	mux.HandleFunc("POST /api/skills", s.handleSkillSave)
	mux.HandleFunc("POST /api/skills/{name}/run", s.handleSkillRun)
	mux.HandleFunc("DELETE /api/skills/{name}", s.handleSkillDelete)

	// 定时任务
	mux.HandleFunc("GET /api/schedules", s.handleScheduleList)
	mux.HandleFunc("POST /api/schedules", s.handleScheduleCreate)
	mux.HandleFunc("POST /api/schedules/{name}/notify", s.handleScheduleNotify)
	mux.HandleFunc("POST /api/schedules/{name}/enabled", s.handleScheduleEnabled)
	mux.HandleFunc("DELETE /api/schedules/{name}", s.handleScheduleDelete)

	// HTTP 回调触发（调度器事件源）与心跳
	mux.HandleFunc("POST /api/hooks/{name}", s.handleHookTrigger)
	mux.HandleFunc("POST /api/heartbeat", s.handleHeartbeat)
	// 唤起主窗口（单实例：第二个进程请求已有实例开窗）
	mux.HandleFunc("POST /api/show-window", s.handleShowWindow)

	// Go 工具链检测
	mux.HandleFunc("GET /api/go-status", s.handleGoStatus)
	mux.HandleFunc("POST /api/go-status/install", s.handleGoInstall)
	mux.HandleFunc("POST /api/go-status", s.handleGoStatusSet)

	// 安全门控留痕（被拦截 / 被批准 / 审核模型加拦）
	mux.HandleFunc("GET /api/security/audit", s.handleSecurityAudit)

	// 设置与上下文
	mux.HandleFunc("GET /api/settings", s.handleSettingsGet)
	mux.HandleFunc("POST /api/settings", s.handleSettingsSave)
	mux.HandleFunc("POST /api/llm/test", s.handleLLMTest)
	mux.HandleFunc("POST /api/llm/models", s.handleLLMModels)
	mux.HandleFunc("GET /api/context", s.handleContextGet)
	mux.HandleFunc("POST /api/context/compress", s.handleContextCompress)
	mux.HandleFunc("POST /api/context/clear", s.handleContextClear)
	mux.HandleFunc("POST /api/conversation/reset", s.handleConversationReset)

	// 多会话（左侧常驻会话列表）
	mux.HandleFunc("GET /api/conversations", s.handleConversationList)
	mux.HandleFunc("POST /api/conversations", s.handleConversationCreate)
	mux.HandleFunc("GET /api/conversations/{id}", s.handleConversationGet)
	mux.HandleFunc("PATCH /api/conversations/{id}", s.handleConversationRename)
	mux.HandleFunc("DELETE /api/conversations/{id}", s.handleConversationDelete)
	mux.HandleFunc("POST /api/conversations/{id}/activate", s.handleConversationActivate)

	// 微光空间（按工作文件夹隔离分组对话）
	mux.HandleFunc("GET /api/spaces", s.handleSpaceList)
	mux.HandleFunc("POST /api/spaces", s.handleSpaceCreate)
	mux.HandleFunc("PATCH /api/spaces/{id}", s.handleSpaceRename)
	mux.HandleFunc("POST /api/spaces/{id}/activate", s.handleSpaceActivate)
	mux.HandleFunc("DELETE /api/spaces/{id}", s.handleSpaceDelete)

	// 工作区（任务文件夹）
	mux.HandleFunc("GET /api/workspace", s.handleWorkspaceGet)
	mux.HandleFunc("POST /api/workspace", s.handleWorkspaceSet)
	mux.HandleFunc("GET /api/fs", s.handleFsBrowse)

	// 厂商预设 / 市场 / MCP 管理
	mux.HandleFunc("GET /api/providers", s.handleProviders)
	mux.HandleFunc("GET /api/market/mcp", s.handleMarketMCP)
	mux.HandleFunc("POST /api/market/mcp/install", s.handleMarketMCPInstall)
	mux.HandleFunc("GET /api/market/skills", s.handleMarketSkills)
	mux.HandleFunc("POST /api/market/skills/install", s.handleMarketSkillInstall)
	mux.HandleFunc("GET /api/mcp", s.handleMCPList)
	mux.HandleFunc("POST /api/mcp", s.handleMCPInstallCustom)
	mux.HandleFunc("DELETE /api/mcp/{name}", s.handleMCPRemove)
	mux.HandleFunc("POST /api/mcp/{name}/reconnect", s.handleMCPRetry)

	// 专家角色与成长日志
	mux.HandleFunc("GET /api/roles", s.handleRolesList)
	mux.HandleFunc("GET /api/growth", s.handleGrowthStats)
	mux.HandleFunc("GET /api/growth/recent", s.handleGrowthRecent)

	// 就绪体检（九坑自检）：只读，随时可跑
	mux.HandleFunc("GET /api/readiness", s.handleReadiness)

	// GEO（生成式引擎优化）：创作产出分析、建议与历史
	mux.HandleFunc("GET /api/geo", s.handleGEOHistory)
	mux.HandleFunc("POST /api/geo/analyze", s.handleGEOAnalyze)
	mux.HandleFunc("DELETE /api/geo/history", s.handleGEOClear)

	// 云端账号（仅登录身份在云端，其余数据全本地）
	mux.HandleFunc("GET /api/account", s.handleAccountGet)
	mux.HandleFunc("POST /api/account/configure", s.handleAccountConfigure)
	mux.HandleFunc("POST /api/account/signup", s.handleAccountSignUp)
	mux.HandleFunc("POST /api/account/signin", s.handleAccountSignIn)
	mux.HandleFunc("POST /api/account/signout", s.handleAccountSignOut)
	mux.HandleFunc("POST /api/account/oauth", s.handleAccountOAuth)
	// 我的：本地数据信息
	mux.HandleFunc("GET /api/local-data", s.handleLocalData)

	return mux
}

// ---------- 基础 ----------

// noCache 静态资源禁用启发式缓存，保证升级后浏览器立即取到新版本。
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, format string, args ...any) {
	writeJSON(w, status, map[string]any{"error": fmt.Sprintf(format, args...)})
}

func readJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	// MaxBytesReader requires a non-nil ResponseWriter and may panic while
	// reporting an oversized body. Limit the stream directly so every caller
	// gets a normal decoder error instead.
	r.Body = io.NopCloser(io.LimitReader(r.Body, 2<<20+1))
	return json.NewDecoder(r.Body).Decode(dst)
}

func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{
		"name":    "gleam",
		"version": "0.1.0",
		"model":   s.Agent.LLMName(),
		"tools":   len(s.Agent.ToolNames()),
		"now":     time.Now(),
	})
}

// handleEvents SSE 事件流。
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "当前连接不支持流式推送")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := s.subscribe()
	defer s.unsubscribe(ch)

	// 打开注释行，供 EventSource 的 onopen 判定
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			// data 字段直接承载内部载荷（类型由 SSE event 名表达）
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, ev.Data)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// ---------- 目标 ----------

type goalSubmitBody struct {
	Goal           string            `json:"goal"`
	Mode           string            `json:"mode"`
	TaskMode       string            `json:"task_mode"`
	References     []types.Reference `json:"references,omitempty"`
	Role           string            `json:"role,omitempty"`
	ConversationID string            `json:"conversation_id,omitempty"`
}

func (s *Server) handleGoalSubmit(w http.ResponseWriter, r *http.Request) {
	var body goalSubmitBody
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "请求体格式无效")
		return
	}
	if len([]rune(body.Goal)) < 2 {
		writeErr(w, 400, "目标太短，请至少输入 2 个字")
		return
	}
	req := types.GoalRequest{Goal: body.Goal, References: body.References, Mode: body.Mode, TaskMode: types.TaskMode(body.TaskMode), Role: body.Role, ConversationID: body.ConversationID}
	if err := agent.ValidateGoalRequest(req); err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	taskID := types.NewID()
	req.TaskID = taskID
	// 使用后台 context：页面关闭不应中断任务；cancel 仅用于显式取消
	ctx, cancel := context.WithCancel(context.Background())

	t := &taskInfo{ID: taskID, Goal: body.Goal, Mode: body.Mode, Status: types.GoalRunning, Started: time.Now(), Cancel: cancel}
	s.mu.Lock()
	s.tasks[taskID] = t
	s.mu.Unlock()

	go func() {
		result := s.Agent.RunGoal(ctx, req)
		cancel() // 任务结束释放 context 资源
		s.mu.Lock()
		t.Status = result.Status
		t.Result = result
		s.pruneTasksLocked(taskID)
		s.mu.Unlock()
		s.broadcast(newEvent("completed", result))
	}()
	resp := map[string]any{"task_id": taskID, "status": "running"}
	if s.Agent.Cfg.LLM.Provider != "mock" && strings.TrimSpace(s.Agent.Cfg.LLM.APIKey) == "" {
		resp["warning"] = "未配置 API Key，模型调用将失败（401）。请到「设置 → 模型」填写后重试，或在启动时使用 --mock-llm 离线体验。"
	}
	writeJSON(w, 200, resp)
}

// maxRetainedTasks 已完成任务保留上限（长期运行防内存无限增长）。
const maxRetainedTasks = 200

// pruneTasksLocked 淘汰最旧的已完成任务（须持有 s.mu）。
// keep 刚完成的任务不淘汰，避免 SSE completed 事件到达后前端补拉详情 404。
func (s *Server) pruneTasksLocked(keep string) {
	if len(s.tasks) <= maxRetainedTasks {
		return
	}
	type rec struct {
		id      string
		started time.Time
	}
	var done []rec
	for id, t := range s.tasks {
		if t.Status != types.GoalRunning && id != keep {
			done = append(done, rec{id, t.Started})
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i].started.Before(done[j].started) })
	for i := 0; i < len(done) && len(s.tasks) > maxRetainedTasks; i++ {
		delete(s.tasks, done[i].id)
	}
}

func (s *Server) handleGoalList(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	list := make([]*taskInfo, 0, len(s.tasks))
	for _, t := range s.tasks {
		list = append(list, t.snapshot())
	}
	s.mu.Unlock()
	// 新的在前
	sort.Slice(list, func(i, j int) bool { return list[i].Started.After(list[j].Started) })
	writeJSON(w, 200, map[string]any{"count": len(list), "goals": list})
}

func (s *Server) handleGoalGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	t, ok := s.tasks[id]
	if ok {
		t = t.snapshot()
	}
	s.mu.Unlock()
	if !ok {
		// 内存表重启即空（且有淘汰上限），盘上 tasks/ 才是终态归档——
		// 有档案却回 404 等于对真实存在的数据撒谎。
		if archived, has := s.archivedTask(id); has {
			writeJSON(w, 200, archived)
			return
		}
		writeErr(w, 404, "任务 %q 不存在", id)
		return
	}
	writeJSON(w, 200, t)
}

// safeTaskIDChars 归档回放的 id 白名单：id 会被拼进文件路径，
// `..`、分隔符、空串等穿越形态必须一刀挡掉（数据目录下有含密钥的 settings）。
var safeTaskIDChars = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// archivedTask 从 tasks/<id>.json 读一份终态记录（gleam replay 读的就是它）。
func (s *Server) archivedTask(id string) (*taskInfo, bool) {
	if !safeTaskIDChars.MatchString(id) {
		return nil, false
	}
	b, err := os.ReadFile(filepath.Join(s.Agent.Cfg.DataDir, "tasks", id+".json"))
	if err != nil {
		return nil, false
	}
	var g types.GoalResult
	if json.Unmarshal(b, &g) != nil || g.TaskID != id {
		return nil, false
	}
	return &taskInfo{ID: g.TaskID, Goal: g.Goal, Status: g.Status, Result: &g, Started: g.StartedAt}, true
}

func (s *Server) handleGoalCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	t, ok := s.tasks[id]
	if ok && t.Cancel != nil {
		t.Cancel()
	}
	s.mu.Unlock()
	_ = s.Agent.CancelTask(id)
	writeJSON(w, 200, map[string]any{"task_id": id, "cancelled": ok})
}

// ---------- 审批 ----------

func (s *Server) handleApprovalList(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]map[string]any, 0, len(s.approvals))
	for _, wt := range s.approvals {
		list = append(list, wt.view())
	}
	writeJSON(w, 200, map[string]any{"count": len(list), "approvals": list})
}

func (s *Server) handleApprovalResolve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Approved bool   `json:"approved"`
		Note     string `json:"note"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "参数解析失败")
		return
	}
	if !s.resolve(id, types.ApprovalResponse{Approved: body.Approved, Note: body.Note}) {
		writeErr(w, 404, "审批请求 %q 不存在或已裁决", id)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "resolved": true})
}

// ---------- 工具 ----------

func (s *Server) handleToolsList(w http.ResponseWriter, _ *http.Request) {
	list := s.Agent.ToolList()
	writeJSON(w, 200, map[string]any{"count": len(list), "tools": list})
}

type toolCallBody struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

func (s *Server) handleToolCall(w http.ResponseWriter, r *http.Request) {
	var body toolCallBody
	if err := readJSON(r, &body); err != nil || body.Name == "" {
		writeErr(w, 400, "需要 name")
		return
	}
	call, err := s.Agent.DirectToolCall(r.Context(), body.Name, body.Args, s.OnApproval)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, call)
}

// ---------- 记忆 ----------

func (s *Server) handleMemorySearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, 400, "需要 q")
		return
	}
	k, _ := strconv.Atoi(r.URL.Query().Get("k"))
	hits := s.Agent.MemorySearch(q, k)
	writeJSON(w, 200, map[string]any{"count": len(hits), "hits": hits})
}

func (s *Server) handleMemorySave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string   `json:"content"`
		Tags    []string `json:"tags"`
	}
	if err := readJSON(r, &body); err != nil || body.Content == "" {
		writeErr(w, 400, "需要 content")
		return
	}
	id, err := s.Agent.MemorySave(body.Content, body.Tags)
	if err != nil {
		writeErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "saved": true})
}

// handleMemoryDelete 软删除单条记忆（L3，2026-09-23 QA：之前只有整库视角，删不掉记错的单条）。
func (s *Server) handleMemoryDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeTaskIDChars.MatchString(id) {
		writeErr(w, 400, "非法记忆 ID")
		return
	}
	if !s.Agent.MemoryDelete(id) {
		writeErr(w, 404, "记忆 %q 不存在或已删除", id)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "deleted": true})
}

// ---------- 技能 ----------

func (s *Server) handleSkillsList(w http.ResponseWriter, _ *http.Request) {
	list := s.Agent.SkillList()
	writeJSON(w, 200, map[string]any{"count": len(list), "skills": list})
}

func (s *Server) handleSkillSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string       `json:"name"`
		Description string       `json:"description"`
		Params      []string     `json:"params"`
		Steps       []types.Step `json:"steps"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "参数解析失败")
		return
	}
	version, err := s.Agent.SkillSave(skill.Skill{
		Name: body.Name, Description: body.Description, Params: body.Params, Steps: body.Steps,
	})
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, map[string]any{"name": body.Name, "version": version, "saved": true})
}

func (s *Server) handleSkillRun(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Params map[string]string `json:"params"`
	}
	if err := readJSON(r, &body); err != nil && err != io.EOF {
		writeErr(w, 400, "参数解析失败")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := s.Agent.SkillRun(ctx, name, body.Params)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleSkillDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.Agent.SkillDelete(name); err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, map[string]any{"name": name, "deleted": true})
}

// ---------- 定时任务 ----------

func (s *Server) handleScheduleList(w http.ResponseWriter, _ *http.Request) {
	jobs := s.Agent.ScheduleList()
	writeJSON(w, 200, map[string]any{"count": len(jobs), "jobs": jobs})
}

func (s *Server) handleScheduleCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Goal        string `json:"goal"`
		When        string `json:"when"`
		Cron        string `json:"cron"`
		IntervalSec int    `json:"interval_sec"`
		Mode        string `json:"mode"`
		Notify      string `json:"notify"` // 通知策略：always / on_failure / never（空 = 默认 on_failure）
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "参数解析失败")
		return
	}
	job, err := s.Agent.ScheduleCreateWithNotify(body.Name, body.Goal, body.Mode, body.When, body.Cron, body.IntervalSec, body.Notify)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, job)
}

// handleScheduleNotify 修改定时任务的通知策略。
//
// 单独一个入口而不是混进创建：**策略是跑起来之后才会想改的东西**——
// 一个每小时跑的任务一开始设成 always，跑了两天发现太吵，改成 on_failure。
// 要求"删掉重建"来改一个开关，用户就会选择忍受噪音。
func (s *Server) handleScheduleNotify(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Notify string `json:"notify"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "参数解析失败")
		return
	}
	job, err := s.Agent.ScheduleSetNotify(name, body.Notify)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, job)
}

// handleScheduleEnabled 暂停/恢复定时任务。与 notify 同理：启停是跑起来之后才会想改的
// 开关，不该逼用户删掉重建。
func (s *Server) handleScheduleEnabled(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := readJSON(r, &body); err != nil || body.Enabled == nil {
		writeErr(w, 400, "参数解析失败，需要 {\"enabled\": true|false}")
		return
	}
	job, err := s.Agent.ScheduleSetEnabled(name, *body.Enabled)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, job)
}

func (s *Server) handleScheduleDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ok, err := s.Agent.ScheduleDelete(name)
	if err != nil {
		writeErr(w, 500, "%v", err)
		return
	}
	if !ok {
		writeErr(w, 404, "任务 %q 不存在", name)
		return
	}
	writeJSON(w, 200, map[string]any{"name": name, "deleted": true})
}

// handleHookTrigger HTTP 回调触发：立即执行指定定时任务（设计文档 §4.2.5 事件触发）。
func (s *Server) handleHookTrigger(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ok, err := s.Agent.ScheduleTrigger(name)
	if err != nil {
		writeErr(w, 404, "%v", err)
		return
	}
	writeJSON(w, 200, map[string]any{"name": name, "triggered": ok})
}

// handleHeartbeat 前端存活心跳（桌面端 app 模式据此判断窗口是否仍然打开）。
func (s *Server) handleHeartbeat(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleShowWindow 唤起主窗口（单实例：第二个进程通过此端点让已有实例开窗）。
func (s *Server) handleShowWindow(w http.ResponseWriter, _ *http.Request) {
	if s.ShowWindowFunc != nil {
		go s.ShowWindowFunc()
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": false, "reason": "no window manager"})
}

// ---------- 设置与上下文 ----------

func (s *Server) handleSettingsGet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, s.Agent.SettingsView())
}

// handleSettingsSave 应用设置补丁：校验后运行时热生效并持久化覆盖层。
func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := readJSON(r, &patch); err != nil {
		writeErr(w, 400, "参数解析失败")
		return
	}
	view, err := s.Agent.ApplySettings(patch)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, view)
}

// handleLLMTest 连通性探测：表单覆盖值（可缺省）→ 最小请求 → 结论分类。
// 永远 200：探测失败是"结论"不是"服务端错误"，前端按 kind 渲染中文提示。
func (s *Server) handleLLMTest(w http.ResponseWriter, r *http.Request) {
	var override map[string]any
	_ = readJSON(r, &override) // 体可缺省：空体即"用当前生效配置探测"
	if override == nil {
		override = map[string]any{}
	}
	writeJSON(w, 200, s.Agent.TestLLMConnection(override))
}

// handleLLMModels 在线拉取模型列表：与探测同一套表单覆盖值与 200-结论约定。
func (s *Server) handleLLMModels(w http.ResponseWriter, r *http.Request) {
	var override map[string]any
	_ = readJSON(r, &override)
	if override == nil {
		override = map[string]any{}
	}
	writeJSON(w, 200, s.Agent.ListLLMModels(override))
}

func (s *Server) handleContextGet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, s.Agent.ContextView())
}

// handleContextCompress 立即压缩待压缩的溢出对话。
func (s *Server) handleContextCompress(w http.ResponseWriter, _ *http.Request) {
	if s.Agent.Mem == nil {
		writeErr(w, 400, "记忆系统未启用")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	compressed := s.Agent.Mem.Compress(s.Agent.CompressSummarizer(ctx))
	writeJSON(w, 200, map[string]any{"compressed": compressed, "context": s.Agent.ContextView()})
}

// handleContextClear 清空滚动摘要与待压缩溢出区。
func (s *Server) handleContextClear(w http.ResponseWriter, _ *http.Request) {
	if s.Agent.Mem == nil {
		writeErr(w, 400, "记忆系统未启用")
		return
	}
	s.Agent.Mem.ClearSummary()
	writeJSON(w, 200, map[string]any{"cleared": true, "context": s.Agent.ContextView()})
}

// handleConversationReset 开始新对话：清空短期对话与滚动摘要上下文（长期记忆保留），
// 并移除已结束的任务记录（正在执行的任务保留），使刷新页面后旧目标不再复活。
func (s *Server) handleConversationReset(w http.ResponseWriter, _ *http.Request) {
	s.Agent.ResetConversation()
	s.mu.Lock()
	for id, t := range s.tasks {
		if t.Status != types.GoalRunning {
			delete(s.tasks, id)
		}
	}
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"reset": true, "context": s.Agent.ContextView()})
}

// ---------- 厂商预设 / 市场 / MCP ----------

func (s *Server) handleProviders(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"count": len(s.Agent.ProvidersView()), "providers": s.Agent.ProvidersView()})
}

func (s *Server) handleMarketMCP(w http.ResponseWriter, r *http.Request) {
	list := s.Agent.MarketMCP(r.URL.Query().Get("q"))
	writeJSON(w, 200, map[string]any{"count": len(list), "presets": list})
}

func (s *Server) handleMarketMCPInstall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID     string            `json:"id"`
		Params map[string]string `json:"params"`
		Trust  string            `json:"trust"`
	}
	if err := readJSON(r, &body); err != nil || body.ID == "" {
		writeErr(w, 400, "需要 id")
		return
	}
	res, err := s.Agent.MCPInstallPreset(body.ID, body.Params, body.Trust)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleMarketSkills(w http.ResponseWriter, r *http.Request) {
	list := s.Agent.MarketSkills(r.URL.Query().Get("q"))
	writeJSON(w, 200, map[string]any{"count": len(list), "presets": list})
}

func (s *Server) handleMarketSkillInstall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil || body.Name == "" {
		writeErr(w, 400, "需要 name")
		return
	}
	version, err := s.Agent.SkillInstallPreset(body.Name)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, map[string]any{"name": body.Name, "version": version, "installed": true})
}

func (s *Server) handleMCPList(w http.ResponseWriter, _ *http.Request) {
	list := s.Agent.MCPList()
	writeJSON(w, 200, map[string]any{"count": len(list), "mcp": list})
}

func (s *Server) handleMCPInstallCustom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string   `json:"name"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
		Trust   string   `json:"trust"`
	}
	if err := readJSON(r, &body); err != nil || body.Command == "" {
		writeErr(w, 400, "需要 command")
		return
	}
	res, err := s.Agent.MCPInstallCustom(body.Name, body.Command, body.Args, body.Trust)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleMCPRetry(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	res, err := s.Agent.MCPRetry(name)
	if err != nil {
		writeErr(w, 404, "%v", err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleMCPRemove(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	res, err := s.Agent.MCPRemove(name)
	if err != nil {
		writeErr(w, 404, "%v", err)
		return
	}
	writeJSON(w, 200, res)
}

// ---------- 工作区（任务文件夹） ----------

func (s *Server) handleWorkspaceGet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, s.Agent.WorkspaceView())
}

// handleWorkspaceSet 切换任务工作区（文件边界与门控热生效）。
func (s *Server) handleWorkspaceSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Path) == "" {
		writeErr(w, 400, "需要 path")
		return
	}
	view, err := s.Agent.WorkspaceSet(body.Path)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, view)
}

// handleFsBrowse 浏览文件系统目录（选择工作区用）。
func (s *Server) handleFsBrowse(w http.ResponseWriter, r *http.Request) {
	res, err := s.Agent.WorkspaceBrowse(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, res)
}

// handleToolPermission 设置工具权限覆盖（只读放行/需我批准/完全访问/恢复默认）。
func (s *Server) handleToolPermission(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Perm string `json:"permission"`
	}
	if err := readJSON(r, &body); err != nil || body.Name == "" {
		writeErr(w, 400, "需要 name")
		return
	}
	res, err := s.Agent.ToolPermissionSet(body.Name, body.Perm)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, res)
}

// ---------- Go 工具链检测 ----------

// handleGoStatus 返回 Go 工具链检测结果。
// 如果指定了 path 参数，仅检查该路径（不回退到系统 PATH）。
func (s *Server) handleGoStatus(w http.ResponseWriter, r *http.Request) {
	custom := r.URL.Query().Get("path")
	if custom != "" {
		st := DetectGo(custom)
		if !st.Found {
			st = GoStatus{Found: false, Source: "user-specified"}
		}
		writeJSON(w, 200, st)
		return
	}
	writeJSON(w, 200, DetectGo())
}

// handleGoStatusSet 接收用户指定的 Go 安装路径并重新检测。
func (s *Server) handleGoStatusSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "参数解析失败")
		return
	}
	st := DetectGo(body.Path)
	writeJSON(w, 200, st)
}

// handleGoInstall downloads and installs Go toolchain (Windows: zip to C:\\Go).
func (s *Server) handleGoInstall(w http.ResponseWriter, r *http.Request) {
	st := DetectGo()
	if st.Found {
		writeJSON(w, 200, st)
		return
	}

	// Download Go zip via PowerShell script (reuses the go-check.ps1 logic)
	// For now, return instructions; actual download is handled by the install script.
	writeJSON(w, 200, map[string]any{
		"found":    false,
		"message":  "请运行安装脚本：powershell -ExecutionPolicy Bypass -File scripts/install.ps1",
		"script":   "scripts/install.ps1",
		"download": "https://mirrors.aliyun.com/golang/go1.23.4.windows-amd64.zip",
	})
}
