package webui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gleam/internal/agent"
	"gleam/internal/buildinfo"
	"gleam/internal/harness/skill"
	"gleam/pkg/types"
)

//go:embed static
var staticFS embed.FS

// Handler 返回完整的 HTTP 路由（含内嵌静态前端），外面包着本机 API 守卫（guard.go）。
// 宿主只应该用这个入口：守卫不是可选项。
func (s *Server) Handler() http.Handler {
	return s.guard(s.routes())
}

// routes 未加守卫的路由表。只在本包内组装 Handler 时使用。
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// 静态前端
	sub, _ := fs.Sub(staticFS, "static")
	fileServer := http.FileServer(http.FS(sub))
	// 首页不走 FileServer：要在 <head> 里注入本次启动的 API 口令（见 guard.go serveIndex）。
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		s.serveIndex(w, r)
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
	mux.HandleFunc("GET /api/goals/{id}/diff", s.handleGoalDiff)
	mux.HandleFunc("POST /api/goals/{id}/revert", s.handleGoalRevert)

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
	mux.HandleFunc("POST /api/skills/{name}/enabled", s.handleSkillEnabled)
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

	// 「连接与出网」台账：本机每条常驻边界的爆炸半径（只读派生，不发任何探测请求）
	mux.HandleFunc("GET /api/connections", s.handleConnections)

	// 候补目标：从本机任务归档浮出来的「你可能想动一下」。只提议，不执行。
	mux.HandleFunc("GET /api/cues", s.handleCues)
	mux.HandleFunc("POST /api/cues/unsuppress", s.handleCueUnsuppress)
	mux.HandleFunc("POST /api/cues/{id}/dismiss", s.handleCueDismiss)
	mux.HandleFunc("POST /api/cues/{id}/adopt", s.handleCueAdopt)

	// 设置与上下文
	mux.HandleFunc("GET /api/settings", s.handleSettingsGet)
	mux.HandleFunc("POST /api/settings", s.handleSettingsSave)
	mux.HandleFunc("POST /api/llm/test", s.handleLLMTest)
	mux.HandleFunc("POST /api/llm/models", s.handleLLMModels)
	mux.HandleFunc("GET /api/models", s.handleModelsList)
	mux.HandleFunc("POST /api/models", s.handleModelAdd)
	mux.HandleFunc("PUT /api/models/{id}", s.handleModelUpdate)
	mux.HandleFunc("DELETE /api/models/{id}", s.handleModelDelete)
	mux.HandleFunc("POST /api/models/{id}/default", s.handleModelSetDefault)
	mux.HandleFunc("POST /api/models/{id}/activate", s.handleModelActivate)
	mux.HandleFunc("GET /api/ssh/hosts", s.handleSSHHosts)
	mux.HandleFunc("DELETE /api/goals/{id}", s.handleGoalDelete)
	mux.HandleFunc("GET /api/network", s.handleNetworkInfo)
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

	// 检查更新（问 GitHub release；替换走显式确认）
	mux.HandleFunc("GET /api/update/check", s.handleUpdateCheck)
	mux.HandleFunc("POST /api/update/apply", s.handleUpdateApply)

	// 从本机其他 AI 工具导入（记忆 / MCP / 技能检测）
	mux.HandleFunc("GET /api/import/scan", s.handleImportScan)
	mux.HandleFunc("POST /api/import/apply", s.handleImportApply)

	// 工作区（任务文件夹）
	mux.HandleFunc("GET /api/workspace", s.handleWorkspaceGet)
	mux.HandleFunc("POST /api/workspace", s.handleWorkspaceSet)
	mux.HandleFunc("POST /api/workspace/clear", s.handleWorkspaceClear)
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
	mux.HandleFunc("POST /api/mcp/{name}/enabled", s.handleMCPToggle)

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

	// 反馈与建议：先落本地 <DataDir>/feedback/，远端投递是可选的第二份
	mux.HandleFunc("POST /api/feedback", s.handleFeedbackSubmit)
	mux.HandleFunc("GET /api/feedback", s.handleFeedbackList)
	mux.HandleFunc("GET /api/feedback/context", s.handleFeedbackContext)
	mux.HandleFunc("POST /api/feedback/{id}/resend", s.handleFeedbackResend)
	mux.HandleFunc("DELETE /api/feedback/{id}", s.handleFeedbackDelete)
	mux.HandleFunc("GET /api/feedback/attachment", s.handleFeedbackAttachment)

	// 首次引导（检测是否已配置 API Key，一键完成初始化）
	mux.HandleFunc("GET /api/onboarding", s.handleOnboardingGet)
	mux.HandleFunc("POST /api/onboarding", s.handleOnboardingSave)

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
	return readJSONCap(r, 2<<20, dst)
}

// readJSONCap 同 readJSON，只是把体积上限交给调用方：带截图的请求天然比一个表单大得多，
// 而"大"是有限度的——上限必须贴着那一类请求的真实形状给，不能一律放到最大。
func readJSONCap(r *http.Request, limit int64, dst any) error {
	defer r.Body.Close()
	// MaxBytesReader requires a non-nil ResponseWriter and may panic while
	// reporting an oversized body. Limit the stream directly so every caller
	// gets a normal decoder error instead.
	r.Body = io.NopCloser(io.LimitReader(r.Body, limit+1))
	return json.NewDecoder(r.Body).Decode(dst)
}

func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{
		"name":    "gleam",
		"version": buildinfo.Version,
		"model":   s.Agent.LLMName(),
		"tools":   len(s.Agent.ToolNames()),
		// 记忆条数由这里给，不让前端自己数：界面上一处"条数"多一个算法，
		// 就早晚会出现两个面板报出两个数。
		"memory": s.Agent.MemoryCount(),
		"now":    time.Now(),
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

	t := &taskInfo{ID: taskID, Goal: body.Goal, Mode: body.Mode, TaskMode: body.TaskMode, Status: types.GoalRunning, Started: time.Now(), Cancel: cancel}
	s.mu.Lock()
	s.tasks[taskID] = t
	s.mu.Unlock()

	go func() {
		result := s.Agent.RunGoal(ctx, req)
		cancel() // 任务结束释放 context 资源
		// 归档先于终态被外界看到。内存表能兜住"收到 completed 就拉详情"，兜不住重启：
		// 而界面任务一旦被看到就是被认为已保存，广播之后再落盘的那几毫秒里进程若被杀，
		// 这一次跑就彻底不存在了——`gleam replay <id>` 与评测的 badcase 回流都读这一份。
		// （顺序本身没有断言：一个进程内测不出"广播后被杀"，见 scripts/mutation/README.md）
		if err := agent.SaveTaskResult(s.Agent.Cfg.DataDir, result); err != nil {
			fmt.Fprintf(os.Stderr, "[gleam] 界面任务未存档：%v\n", err)
		}
		s.mu.Lock()
		t.Status = result.Status
		t.Result = result
		s.pruneTasksLocked(taskID)
		s.mu.Unlock()
		s.broadcast(newEvent("completed", result))
	}()
	resp := map[string]any{"task_id": taskID, "status": "running"}
	if s.Agent.Cfg.LLM.Provider != "mock" {
		// 问密钥的单一出口，而不是直接读 cfg.LLM.APIKey ——后者只说"当前主机上有没有生效的 key"，
		// 说不了"配过、但配的是别家"。把后者也报成"未配置"是假的：用户明明填过，只是换厂商要重填。
		if key, storedHost := agent.LLMKeyFor(s.Agent.Cfg, s.Agent.Creds, s.Agent.Cfg.LLM.BaseURL, ""); key == "" {
			if storedHost != "" {
				resp["warning"] = fmt.Sprintf("当前接入主机没有可用密钥（本机存着的是发给 %s 的那把）。密钥按接入主机绑定，请为现在的厂商重新填一次。", storedHost)
			} else {
				resp["warning"] = "未配置 API Key，模型调用将失败（401）。请到「设置 → 模型」填写后重试，或在启动时使用 --mock-llm 离线体验。"
			}
		}
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

// listArchiveLimit 列表里最多带出多少条归档。
//
// 归档目录会一路涨，而界面显示的是"最近任务"；上限既是性能护栏，也是响应体护栏
// （每条都带完整 result，几千条会把一次轮询变成一次下载）。
const listArchiveLimit = 100

func (s *Server) handleGoalList(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	list := make([]*taskInfo, 0, len(s.tasks))
	seen := make(map[string]bool, len(s.tasks))
	for _, t := range s.tasks {
		list = append(list, t.snapshot())
		seen[t.ID] = true
	}
	s.mu.Unlock()
	// 内存表在重启（或被 maxRetainedTasks 淘汰）之后就空了，盘上的 tasks/ 才是历史。
	// 只有详情读归档、列表不读，用户看到的就是"全部 0"——他会以为记录丢了，
	// 而那份档案其实就在盘上（`gleam replay <id>` 也读得到）。
	archived, skipped, err := agent.ListTaskResults(s.Agent.Cfg.DataDir, listArchiveLimit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[gleam] 任务归档列表读不动：%v\n", err)
	}
	if skipped > 0 {
		// 坏归档只该让那一条看不见，不该让人以为整段历史没了——所以要出声。
		fmt.Fprintf(os.Stderr, "[gleam] %d 份任务归档读不动，已跳过\n", skipped)
	}
	for _, g := range archived {
		if g == nil || seen[g.TaskID] {
			continue // 内存里那份更新（可能还在跑），以它为准
		}
		list = append(list, archivedTaskInfo(g))
	}
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

// safeIDChars URL 路径参数的形状白名单。这里只管"能不能当标识符"，
// 不管落不落盘：要拼成文件名的那类 id 走 agent.SafeTaskName（读写同一条规则）。
var safeIDChars = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// archivedTask 从 tasks/<id>.json 读一份终态记录（gleam replay 读的就是它）。
//
// id 会被拼进文件路径，所以先过写入侧同一条名字规则（`agent.SafeTaskName`）：
// `..`、分隔符、空串等穿越形态必须一刀挡掉——数据目录下就有含密钥的 settings。
// 读写共用一条规则，才不会写出一个读回来的路径形状不一样的文件。
func (s *Server) archivedTask(id string) (*taskInfo, bool) {
	g, err := agent.ReadTaskResult(s.Agent.Cfg.DataDir, id)
	if err != nil {
		// 坏文件与"从没写过"是两件事：都当成不存在，用户只会反复重跑那次任务，
		// 而真正的问题是归档文件本身读不动。
		fmt.Fprintf(os.Stderr, "[gleam] 任务归档 %s 读不动：%v\n", id, err)
		return nil, false
	}
	if g == nil {
		return nil, false
	}
	return archivedTaskInfo(g), true
}

// archivedTaskInfo 把一份盘上归档还原成界面用的任务记录。
//
// 读侧只有这一个构造点：列表和详情必须给出同一种形状，否则"刚跑完"与"重启后点开"
// 会是两套字段，前端的徽标和筛选就会在重启之后突然少一半。
func archivedTaskInfo(g *types.GoalResult) *taskInfo {
	return &taskInfo{
		ID: g.TaskID, Goal: g.Goal, Mode: g.Mode, TaskMode: string(g.TaskMode),
		Status: g.Status, Result: g, Started: g.StartedAt,
	}
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
	// 取消之后必须叫醒停在审批上的那一轮：ctx 已经取消，但引擎的整计划闸门等的是
	// 审批通道，不是 ctx。"取消返回 200、任务还在 running"就是这么来的。
	s.dropTaskApprovals(id, "任务已取消")
	writeJSON(w, 200, map[string]any{"task_id": id, "cancelled": ok})
}

// ---------- 本次改动：对比与还原 ----------

// handleGoalDiff 一条路径的「写前 → 现在」行级对比。
//
// 路径走查询参数，不放进 `/diff/<path>` 路由段：那是个带分隔符的绝对路径，
// 塞进路由就多出一套转义与匹配口径，而 `{id}` 已经足够定位"哪次任务的哪一条"。
func (s *Server) handleGoalDiff(w http.ResponseWriter, r *http.Request) {
	view, err := s.Agent.TaskDiff(r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, view)
}

type goalRevertBody struct {
	Path string `json:"path"`
}

// handleGoalRevert 把一条路径退回到本次任务开始之前。只由用户点击触发。
//
// 成功后**拿归档替换内存里的那条结果**：改动清单"现在是什么样"的 owner 是归档，
// 内存副本跟着它走，才不会同一个任务在刷新前后报出两份不同的清单
// （前端因此只需要重新拉一次详情，不需要自己拼"还原后应该长什么样"）。
func (s *Server) handleGoalRevert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body goalRevertBody
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "请求体无效")
		return
	}
	res, err := s.Agent.TaskRevert(id, body.Path)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	if archived, has := s.archivedTask(id); has {
		s.mu.Lock()
		if t, ok := s.tasks[id]; ok {
			t.Result = archived.Result
		}
		s.mu.Unlock()
		res["refreshed"] = true
	}
	writeJSON(w, 200, res)
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
	if !safeIDChars.MatchString(id) {
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

// handleSkillEnabled 启用 / 停用技能：与定时任务、MCP 的启停同一条形状（状态落盘，响应带回新列表）。
func (s *Server) handleSkillEnabled(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := readJSON(r, &body); err != nil || body.Enabled == nil {
		writeErr(w, 400, "需要 enabled（true 启用 / false 停用）")
		return
	}
	res, err := s.Agent.SkillSetEnabled(name, *body.Enabled)
	if err != nil {
		writeErr(w, 404, "%v", err)
		return
	}
	writeJSON(w, 200, res)
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

// ---------- 多模型 CRUD ----------

func (s *Server) handleModelsList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{
		"models": s.Agent.ModelsList(),
		"active": s.Agent.ActiveModel(),
	})
}

func (s *Server) handleModelAdd(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "参数解析失败")
		return
	}
	view, err := s.Agent.ModelAdd(body)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) handleModelUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body map[string]any
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "参数解析失败")
		return
	}
	view, err := s.Agent.ModelUpdate(id, body)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) handleModelDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	view, err := s.Agent.ModelDelete(id)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) handleModelSetDefault(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	view, err := s.Agent.ModelSetDefault(id)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) handleModelActivate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	view, err := s.Agent.SetActiveModel(id)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, view)
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
		Force  bool              `json:"force"`
	}
	if err := readJSON(r, &body); err != nil || body.ID == "" {
		writeErr(w, 400, "需要 id")
		return
	}
	res, err := s.Agent.MCPInstallPreset(body.ID, body.Params, body.Trust, body.Force)
	if err != nil {
		writeInstallErr(w, err)
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
		Name  string `json:"name"`
		Force bool   `json:"force"`
	}
	if err := readJSON(r, &body); err != nil || body.Name == "" {
		writeErr(w, 400, "需要 name")
		return
	}
	version, err := s.Agent.SkillInstallPreset(body.Name, body.Force)
	if err != nil {
		writeInstallErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"name": body.Name, "version": version, "installed": true})
}

// writeInstallErr 安装类错误的统一出口：409 =「已装着呢，要不要重装」（前端据此弹确认），
// 400 = 参数真的不对。三处安装入口共用一条判断，别处再写一遍就会漂。
func writeInstallErr(w http.ResponseWriter, err error) {
	if errors.Is(err, agent.ErrAlreadyInstalled) {
		writeErr(w, 409, "%v", err)
		return
	}
	writeErr(w, 400, "%v", err)
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
		Force   bool     `json:"force"`
	}
	if err := readJSON(r, &body); err != nil || body.Command == "" {
		writeErr(w, 400, "需要 command")
		return
	}
	res, err := s.Agent.MCPInstallCustom(body.Name, body.Command, body.Args, body.Trust, body.Force)
	if err != nil {
		writeInstallErr(w, err)
		return
	}
	writeJSON(w, 200, res)
}

// handleMCPToggle 启用 / 停用一台 MCP 服务器（状态落盘，与定时任务的暂停/恢复同构）。
func (s *Server) handleMCPToggle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := readJSON(r, &body); err != nil || body.Enabled == nil {
		writeErr(w, 400, "需要 enabled（true 启用 / false 停用）")
		return
	}
	res, err := s.Agent.MCPSetEnabled(name, *body.Enabled)
	if err != nil {
		writeErr(w, 404, "%v", err)
		return
	}
	// 响应里带回完整 mcp 列表：界面按返回值刷新，比再发一条 SSE 让前端重新拉一次更直接
	// （停用是用户自己点的那一下，不存在"别人改了状态"的并发场景）。
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

// handleWorkspaceClear 回到「不指定工作区」。
func (s *Server) handleWorkspaceClear(w http.ResponseWriter, _ *http.Request) {
	view, err := s.Agent.WorkspaceClear()
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
	writeJSON(w, 200, s.managedGoStatus())
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

// handleGoInstall 见 goinstall.go：真下载并展开到 <数据目录>/tools/go。

// ---------- 首次引导 ----------

// handleOnboardingGet 返回引导状态与厂商列表。
// needs_onboarding = 当前无有效 API Key 且非 mock 模式。
func (s *Server) handleOnboardingGet(w http.ResponseWriter, _ *http.Request) {
	needs := false
	if s.Agent.Cfg.LLM.Provider != "mock" {
		key, _ := agent.LLMKeyFor(s.Agent.Cfg, s.Agent.Creds, s.Agent.Cfg.LLM.BaseURL, "")
		needs = key == ""
	}
	writeJSON(w, 200, map[string]any{
		"needs_onboarding": needs,
		"providers":        s.Agent.ProvidersView(),
	})
}

// handleOnboardingSave 引导流程保存：选厂商 → 填 Key → 保存并测试连接。
// 成功后标记引导完成，前端不再弹出引导页。
func (s *Server) handleOnboardingSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProviderID string `json:"provider_id"`
		Plan       string `json:"plan"`
		APIKey     string `json:"api_key"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "参数解析失败")
		return
	}
	body.ProviderID = strings.TrimSpace(body.ProviderID)
	body.Plan = strings.TrimSpace(body.Plan)
	body.APIKey = strings.TrimSpace(body.APIKey)
	if body.ProviderID == "" {
		writeErr(w, 400, "请选择一个模型厂商")
		return
	}
	if body.APIKey == "" {
		writeErr(w, 400, "请填写 API Key")
		return
	}

	patch := map[string]any{
		"llm": map[string]any{
			"provider_id": body.ProviderID,
			"plan":        body.Plan,
			"api_key":     body.APIKey,
		},
	}
	view, err := s.Agent.ApplySettings(patch)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}

	testResult := s.Agent.TestLLMConnection(map[string]any{
		"provider_id": body.ProviderID,
		"api_key":     body.APIKey,
	})

	writeJSON(w, 200, map[string]any{
		"saved":       true,
		"settings":    view,
		"test_result": testResult,
	})
}
