package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gleam/internal/agent/geo"
	"gleam/internal/config"
	"gleam/internal/harness/conversation"
	"gleam/internal/harness/credentials"
	"gleam/internal/harness/growth"
	"gleam/internal/harness/memory"
	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/internal/harness/scheduler"
	"gleam/internal/harness/skill"
	"gleam/internal/harness/space"
	"gleam/internal/llm"
	"gleam/internal/tools/file"
	"gleam/pkg/types"
)

// Agent Gleam 核心引擎：Plan-Execute-Reflect 循环 + 任务登记。
type Agent struct {
	LLM       llm.Client
	Reg       *registry.Registry
	Mem       *memory.Manager
	Gate      *safety.Gate
	Skills    *skill.Store
	Sched     *scheduler.Scheduler
	Cfg       *config.Config
	Notifier  Notifier
	MCP       *MCPManager
	FileTools *file.Tools         // 文件工具集（工作区热切换时更新 Roots）
	Growth    *growth.Log         // 成长日志
	Convos    *conversation.Store // 多会话持久化（左侧会话列表）
	Spaces    *space.Store        // 微光空间（按工作文件夹隔离对话）
	Creds     *credentials.Store  // 本地敏感凭证（LLM Key / 云端会话，0600）
	Auth      AuthProvider        // 云端账号（可选）
	GEO       *geo.Store          // GEO 分析历史（创作产出自动分析的留档，供 GEO 板块回看）
	FastLLM   llm.Client          // 可选：辅助调用（压缩/GEO/技能优化）用的便宜模型，nil 时用主模型

	mu    sync.Mutex
	tasks map[string]*taskHandle

	// 消耗统计（消耗看板）：按 taskID 归集模型调用与工具调用开销
	usageMu  sync.Mutex
	usage    map[string]*types.TaskUsage
	budget   map[string]*taskBudget    // 用户确认后追加的预算额度
	stuck    map[string]map[string]int // taskID -> 「工具+参数」指纹 -> 出现在多少轮里（防打转）
	hookOnce sync.Once

	// 模型档位客户端缓存：档位名 -> 客户端（按配置惰性构建，设置保存后由 RebuildTierClients 重建）
	tierMu      sync.Mutex
	tierClients map[string]llm.Client
}

// taskBudget 用户批准后追加的额度（在配置上限之上再加）。
type taskBudget struct {
	extraCalls  int
	extraTokens int
}

// AuthProvider 云端账号能力（由 internal/harness/auth.Manager 实现），agent 不直接依赖 auth 包。
type AuthProvider interface {
	Configured() bool
	Session() map[string]any
	Configure(supabaseURL, anonKey string) error
	SignUp(email, password string) error
	SignIn(email, password string) error
	SignOut() error
	StartOAuth(provider string) (string, error)
}

type taskHandle struct {
	id      string
	goal    string
	mode    string
	cancel  context.CancelFunc
	status  types.GoalStatus
	result  *types.GoalResult
	started time.Time
}

// New 创建 Agent（各子系统由运行时装配）。
func New(cfg *config.Config, client llm.Client, reg *registry.Registry, mem *memory.Manager,
	gate *safety.Gate, skills *skill.Store, sched *scheduler.Scheduler, notifier Notifier) *Agent {
	if notifier == nil {
		notifier = NopNotifier{}
	}
	return &Agent{
		LLM: client, Reg: reg, Mem: mem, Gate: gate,
		Skills: skills, Sched: sched, Cfg: cfg, Notifier: notifier,
		tasks: map[string]*taskHandle{},
	}
}

// RunGoal 执行一个目标（同步；调用方可放 goroutine）。req.TaskID 非空时沿用。
func (a *Agent) RunGoal(ctx context.Context, req types.GoalRequest) *types.GoalResult {
	taskID := req.TaskID
	if taskID == "" {
		taskID = types.NewID()
	}
	req.TaskID = taskID
	goal := strings.TrimSpace(req.Goal)
	mode := normalizeMode(req.Mode)

	// 重试检测（P3-4 / 站点 H15）：同一会话短时间内相似目标再次提交 = 用户重试。
	// 必须在本条用户消息写入会话**之前**判定，否则检测到的是刚写入的这条自己。
	retried := req.ConversationID != "" && a.Convos != nil && a.Convos.DetectRetry(req.ConversationID, goal)

	// 记录到持久化会话（左侧会话列表）；原始用户目标（不含系统注入的引用前缀）。
	a.appendConvo(req.ConversationID, conversation.Message{
		Role: "user", Content: goal, TaskID: taskID,
		Mode: string(normalizeTaskMode(req.TaskMode)), CreatedAt: time.Now(),
	})

	handle := &taskHandle{id: taskID, goal: goal, mode: mode, status: types.GoalRunning, started: time.Now()}
	a.mu.Lock()
	if old, ok := a.tasks[taskID]; ok && old.status == types.GoalRunning {
		a.mu.Unlock()
		return a.resultOf(old) // 同 ID 正在运行，直接引用
	}
	a.tasks[taskID] = handle
	a.mu.Unlock()
	// 建立用量计数（GEO 等任务完成后的异步调用也会计入）
	a.initUsage(taskID)

	ctx, cancel := context.WithCancel(ctx)
	handle.cancel = cancel
	defer cancel()

	// 配置快照取**启动时**的值，不是收尾时的：任务跑到一半用户在设置页改了模型或档位，
	// 后半段用的就是新配置了，而"这次任务是用什么跑的"必须以启动时为准——
	// 收尾时再取，记下的会是一个从未完整生效过的配置。
	snap := a.Cfg.Snapshot()
	// prog 收集循环内部的交付侧事实（第几轮才成、返工了几次），回来就写进结果——
	// 与下面的成长记录紧挨着，避免"记了口径"和"用了口径"隔在文件两头。
	prog := &goalProgress{}
	result := a.runGoalLoop(ctx, req, goal, mode, normalizeTaskMode(req.TaskMode), taskID, prog)
	prog.stamp(result)
	result.ConfigSnapshot = &snap
	result.TaskID = taskID
	result.Mode = mode
	result.TaskMode = normalizeTaskMode(req.TaskMode)
	result.StartedAt = handle.started
	// trace_id 由「目标 + 任务模式 + 角色 + 模型」派生，刻意不含时间戳：
	// 同一个输入配同一个模型重复出现时 ID 相同，失败聚类按 ID 分组即可——
	// "同一个问题反复出现"不需要额外索引就能看见。
	result.TraceID = deriveTraceID(goal, string(normalizeTaskMode(req.TaskMode)), req.Role, a.LLMName())
	// 这次产出用的是哪版规则集：版本指纹 + 实际生效的规则 ID。
	// 改了规则之后第一个要回答的问题是"之前那批结果是哪一版跑出来的"，
	// 回答不了就只能靠猜。对话模式不经规划器、没有规则参与，两个字段如实留空。
	if tm := normalizeTaskMode(req.TaskMode); tm != types.TaskChat {
		result.RuleSet = RuleSetVersion()
		result.Rules = ActiveRuleIDs(string(tm), a.EffectiveTier(req.Role), req.Role)
	}
	result.FinishedAt = time.Now()
	result.Usage = a.usageOf(taskID)
	result.Usage.DurationMs = result.FinishedAt.Sub(handle.started).Milliseconds()

	// 记录助手回复到同一会话；失败/取消也留痕，避免历史缺轮。
	a.appendConvo(req.ConversationID, conversation.Message{
		Role: "assistant", Content: convoReply(result), TaskID: taskID,
		Mode: string(normalizeTaskMode(req.TaskMode)), Status: string(result.Status), CreatedAt: time.Now(),
	})

	a.mu.Lock()
	handle.status = result.Status
	handle.result = result
	a.pruneTasksLocked()
	a.mu.Unlock()
	// 记录成长日志。
	// 取消单独记中断（task_aborted）：中断率与通过率混在一个分母里，
	// "用户放弃"和"做砸了"就分不开了——两者要修的东西完全不同。
	if a.Growth != nil {
		entry := growth.Entry{
			Goal: goal, Score: result.Score,
			Steps: len(result.Steps), Duration: result.FinishedAt.Sub(result.StartedAt).Seconds(),
			Tokens: result.Usage.TotalTokens(), LLMCalls: result.Usage.LLMCalls, ToolCalls: result.Usage.ToolCalls,
			RuleSet: result.RuleSet,
			// 交付侧口径（首次通过 / 返工轮数）：产物是不是一次就合格。
			// 只对工作模式记——对话模式没有"产物"可核；而工作模式即便规划屡败回落直聊，
			// 那也是一次失败的交付，必须留在分母里（摘掉最差的几个，比率就成了摆设）。
			Delivery:  normalizeTaskMode(req.TaskMode) != types.TaskChat,
			FirstPass: result.FirstPass,
			Reworks:   result.Reworks,
		}
		if result.Status == types.GoalCancelled {
			entry.Type = "task_aborted"
		} else {
			entry.Type = "task_completed"
			entry.Retried = retried
		}
		a.Growth.Record(entry)
	}
	return result
}

// ValidateGoalRequest 校验 Web/JSON-RPC 等外部入口的目标参数。
// 空模式使用兼容默认值；非空未知值必须显式拒绝，避免静默降级。
func ValidateGoalRequest(req types.GoalRequest) error {
	if err := types.ValidateReferences(req.References); err != nil {
		return err
	}
	if req.Mode != "" && req.Mode != string(types.ModeAuto) && req.Mode != string(types.ModePlanFirst) && req.Mode != string(types.ModeInteractive) {
		return fmt.Errorf("不支持的安全模式 %q", req.Mode)
	}
	if req.TaskMode != "" && req.TaskMode != types.TaskChat && req.TaskMode != types.TaskWork && req.TaskMode != types.TaskCode {
		return fmt.Errorf("不支持的任务模式 %q", req.TaskMode)
	}
	if req.Role != "" && !HasRole(req.Role) {
		return fmt.Errorf("不支持的专家角色 %q", req.Role)
	}
	// task_id 会被拼成任务归档的文件名（tasks/<id>.json），所以它的形状在**入口**就得定：
	// 放过去的话，任务会照常跑完、照常花 token，最后归档那一步默默失败——
	// 人在 `gleam replay` 报"读不到"的时候才发现，而那次的凭据已经没了。
	if req.TaskID != "" && SafeTaskName(req.TaskID) == "" {
		return fmt.Errorf("task_id %q 不能作文件名：只允许字母、数字与 - _，长度不超过 128", req.TaskID)
	}
	return nil
}

// maxRetainedTasks 已完成任务保留上限（防止长期运行内存无限增长）。
const maxRetainedTasks = 200

// pruneTasksLocked 淘汰最旧的已完成任务（须持有 a.mu）。
func (a *Agent) pruneTasksLocked() {
	if len(a.tasks) <= maxRetainedTasks {
		return
	}
	type rec struct {
		id      string
		started time.Time
	}
	var done []rec
	for id, h := range a.tasks {
		if h.status != types.GoalRunning {
			done = append(done, rec{id, h.started})
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i].started.Before(done[j].started) })
	for i := 0; i < len(done) && len(a.tasks) > maxRetainedTasks; i++ {
		delete(a.tasks, done[i].id)
		a.dropUsage(done[i].id)
	}
}

// HelperLLM 返回辅助调用用的客户端：配了便宜模型就用它，否则回退主模型。
// 上下文压缩、GEO 分析、技能参数优化这类高频低难度调用走它，规划/执行/对话仍用主模型
// （省 token 的 Harness 与强模型共同演进）。
func (a *Agent) HelperLLM() llm.Client {
	if a != nil && a.FastLLM != nil {
		return a.FastLLM
	}
	return a.LLM
}

// RebuildFastClient 按当前配置重建辅助模型客户端（设置保存后热生效；未配置则置空）。
func (a *Agent) RebuildFastClient() {
	if a == nil || a.Cfg == nil || a.Cfg.LLM.FastModel == "" || a.Cfg.LLM.Provider == "mock" {
		a.FastLLM = nil
		return
	}
	protocol := a.Cfg.LLM.Protocol
	if protocol == "" {
		protocol = llm.ProtocolOpenAIChat
	}
	a.FastLLM = llm.New(protocol, a.Cfg.LLM.BaseURL, a.Cfg.LLM.APIKey, a.Cfg.LLM.FastModel,
		a.Cfg.LLM.Temperature, a.Cfg.LLM.MaxTokens, a.Cfg.LLM.TimeoutSecs)
}

// RebuildTierClients 清空模型档位客户端缓存（设置保存后热生效）。
// 只清缓存不预建：用不到的档位不该白占连接池。
func (a *Agent) RebuildTierClients() {
	if a == nil {
		return
	}
	a.tierMu.Lock()
	a.tierClients = nil
	a.tierMu.Unlock()
}

// TierLLM 返回某个角色（场景模板）该用的模型客户端。
//
// 三层回退，任何一层缺失都不算错误——档位是"可以挑"，不是"必须挑"：
//  1. 角色没声明 model_tier → 主模型；
//  2. 配置里没有这个档位名 → 主模型；
//  3. 档位指向的模型就是主模型 → 直接复用主客户端，不重复建连接。
func (a *Agent) TierLLM(role string) llm.Client {
	if a == nil || a.Cfg == nil {
		return a.LLM
	}
	return a.tierLLM(FindRole(role).ModelTier)
}

// tierLLM 按**档位名**取客户端。
//
// TierLLM 是它"先由角色解析出档位"的包装；验收侧要直接点名一个档位（见 VerifyLLM），
// 所以把这段单独抽出来，免得两处各写一份回退与缓存——那样迟早改一边忘一边。
func (a *Agent) tierLLM(tier string) llm.Client {
	if a == nil || a.Cfg == nil {
		return a.LLM
	}
	if tier == "" || len(a.Cfg.LLM.Tiers) == 0 {
		return a.LLM
	}
	model := strings.TrimSpace(a.Cfg.LLM.Tiers[tier])
	if model == "" || model == a.Cfg.LLM.Model {
		return a.LLM
	}
	// mock 会话下不建真客户端：档位只是配置，不该让离线自测产生网络调用
	if a.Cfg.LLM.Provider == "mock" {
		return a.LLM
	}
	a.tierMu.Lock()
	defer a.tierMu.Unlock()
	if c, ok := a.tierClients[tier]; ok {
		return c
	}
	protocol := a.Cfg.LLM.Protocol
	if protocol == "" {
		protocol = llm.ProtocolOpenAIChat
	}
	c := llm.New(protocol, a.Cfg.LLM.BaseURL, a.Cfg.LLM.APIKey, model,
		a.Cfg.LLM.Temperature, a.Cfg.LLM.MaxTokens, a.Cfg.LLM.TimeoutSecs)
	if a.tierClients == nil {
		a.tierClients = map[string]llm.Client{}
	}
	a.tierClients[tier] = c
	return c
}

// DefaultVerifyTier 验收（反思）阶段默认点名的档位。
//
// 为什么是 reasoning：验收是**判断**活，不是干活——要拿实测证据跟验收标准逐条对，
// 属于"分析·调研"那一类。内置的四个档位名里，只有它为判断准备。
const DefaultVerifyTier = "reasoning"

// VerifyLLM 返回**验收**（反思）阶段该用的客户端。
//
// 为什么需要它：同源同档是独立验收最大的漏洞——同一个模型干完活又给自己打分，
// 它的盲区会同时出现在"干活"和"验收"两侧，自评偏差没有任何东西去抵消。
// 所以验收刻意走一个**不同档位**的模型（默认 DefaultVerifyTier）。
//
// 判据只有一条，而且必须落在**模型 ID 不同**上，不是"档位名不同"：
// 两个档位名指向同一个模型并不构成独立，照旧回退。
//
//  1. reasoning 档没配 / 指向主模型 / 角色自己就跑在 reasoning 档上 → 回退到执行者客户端；
//  2. mock 会话下不建真客户端（与 TierLLM 同款约定，离线自测不该打网络调用）。
//
// **回退不是错误，但不能假装独立**：回退时 VerifyModel 如实返回执行者的模型名，
// 诊断输出据此说明"未换成"。这样"独立性"是一个可核对的事实，而不是注释里的一句期望。
//
// 成本提醒：reflect 默认每个 attempt 只跑一次（ReflectEachStep=false，MaxReplans=2），
// 所以最多多出 3 次调用；但若把 reasoning 档指向一个贵模型，仍会实实在在花在验收上。
// 不想付这笔钱就把 reasoning 档留空——**档位是"可以挑"不是"必须挑"**。
//
// **已知限制（如实写在代码里，不靠注释糊过去）**：`analyst` 这类角色自己就声明了
// reasoning 档，执行者与候选验收模型是同一个，于是**换不成、走回退**。
// 这里刻意**不**退而求其次去挑 economy/coding 档或主模型顶上：验收是判断活，
// 拿一个没有理由信任的弱模型去判强模型的产出，只会得到一个"看起来独立"的橡皮图章，
// 比同源自评更难发现。要覆盖这些角色，需要的是"另一个够格的判断模型"这个输入，
// 而不是在这条链上再加一层猜测。诊断输出会把这种情况明说成"未换成"。
func (a *Agent) VerifyLLM(role string) llm.Client {
	if a == nil || a.Cfg == nil {
		return a.LLM
	}
	if !a.verifyIndependent(role) {
		return a.TierLLM(role)
	}
	return a.tierLLM(DefaultVerifyTier)
}

// VerifyModel 返回验收阶段实际用的模型名（诊断与测试用，**不建客户端**）。
//
// 存在的理由：光有 VerifyLLM 看不出"到底换没换成"——回退与换成功都返回一个可用客户端。
// 把结果摊开成一个名字，才能让"独立验收"从承诺变成可核对的事实。
func (a *Agent) VerifyModel(role string) string {
	if a == nil || a.Cfg == nil {
		return ""
	}
	if a.verifyIndependent(role) {
		return strings.TrimSpace(a.Cfg.LLM.Tiers[DefaultVerifyTier])
	}
	return a.execModel(role)
}

// verifyIndependent 验收是否真的会换到另一个模型。判据只有一条：模型 ID 不同。
func (a *Agent) verifyIndependent(role string) bool {
	if a == nil || a.Cfg == nil || a.Cfg.LLM.Provider == "mock" {
		return false // mock 会话下档位本就失效，见 EffectiveTier 的同款说明
	}
	m := strings.TrimSpace(a.Cfg.LLM.Tiers[DefaultVerifyTier])
	return m != "" && m != a.execModel(role)
}

// execModel 执行者实际会用的模型 ID。角色档位没生效时就是主模型。
func (a *Agent) execModel(role string) string {
	if a == nil || a.Cfg == nil {
		return ""
	}
	if t := a.EffectiveTier(role); t != "" {
		return strings.TrimSpace(a.Cfg.LLM.Tiers[t])
	}
	return a.Cfg.LLM.Model
}

// EffectiveTier 返回角色**实际生效**的档位名；档位未配置（或只是回退主模型）时返回空。
//
// 判据与 TierLLM 一致，但**不建客户端**：提示词构建不该有副作用。
// mock 会话下档位本就失效（避免离线自测打真实网络），这里同样返回空。
//
// 为什么规则表需要它：规则开关只能挂在稳定维度上，档位是其中之一。
// 但判据必须是"生效"而不是角色"声明"的档位——声明只说明这个场景**建议**用哪一档，
// 没配时角色照样跑在主模型上；拿声明档位切规则，会切出"跑着主模型、
// 却按强模型的口径删规则"这种错配。
func (a *Agent) EffectiveTier(role string) string {
	if a == nil || a.Cfg == nil {
		return ""
	}
	tier := FindRole(role).ModelTier
	if tier == "" || len(a.Cfg.LLM.Tiers) == 0 {
		return ""
	}
	model := strings.TrimSpace(a.Cfg.LLM.Tiers[tier])
	if model == "" || model == a.Cfg.LLM.Model {
		return ""
	}
	if a.Cfg.LLM.Provider == "mock" {
		return ""
	}
	return tier
}

// BuildPlanner 按一次目标请求装配规划器。
//
// 与 RunGoal 内部走的是**同一段逻辑**——评测与排障需要「只规划不执行」时调它，
// 而不是自己再拼一个 Planner：两处各写一份，迟早改一边忘一边，
// 那样评测出来的就不是真实运行时的行为了。
func (a *Agent) BuildPlanner(req types.GoalRequest) *Planner {
	taskID := req.TaskID
	if taskID == "" {
		taskID = types.NewID()
	}
	pl := &Planner{
		LLM: a.TierLLM(req.Role), Reg: a.Reg,
		MaxSteps: a.Cfg.Agent.MaxSteps,
		Style:    a.Cfg.Persona.Style,
		TaskMode: string(normalizeTaskMode(req.TaskMode)),
		Role:     req.Role,
		TaskID:   taskID,
		// 生效档位：规则表据此决定"要不要给这一档换一版规则"（见 rules.go）。
		// 未配档位时为空，规则一律落到通用版——保守优先，能力未知就不动规则。
		Tier: a.EffectiveTier(req.Role),
		// 薄 Harness：工具多了只给能力菜单 + 按需筛选出的 schema。
		// Helper 只在真配了辅助模型时才给——否则快筛会白白多花一次主模型的钱。
		MaxToolSchemas: a.Cfg.Agent.MaxToolSchemas,
		Helper:         a.FastLLM,
		// 场景模板带来的差异：核心工具（保证在）、产出格式与模型档位（见 buildSystemPrompt / TierLLM）。
		RoleTools: FindRole(req.Role).Tools,
	}
	// 钉住区（P2-1）：不可压缩的用户约束，注入时排易变段最前、绝不重排。
	if a.Mem != nil {
		pl.Pinned = a.Mem.Pinned()
	}
	return pl
}

// ---------- 消耗统计（消耗看板） ----------

// initUsage 为任务建立用量计数。
func (a *Agent) initUsage(taskID string) {
	a.installUsageHook()
	a.usageMu.Lock()
	if a.usage == nil {
		a.usage = map[string]*types.TaskUsage{}
	}
	if a.budget == nil {
		a.budget = map[string]*taskBudget{}
	}
	if a.stuck == nil {
		a.stuck = map[string]map[string]int{}
	}
	a.usage[taskID] = &types.TaskUsage{}
	a.budget[taskID] = &taskBudget{}
	a.stuck[taskID] = map[string]int{}
	a.usageMu.Unlock()
}

// budgetExtra 读取用户确认后追加的额度。
func (a *Agent) budgetExtra(taskID string) taskBudget {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	if b := a.budget[taskID]; b != nil {
		return *b
	}
	return taskBudget{}
}

// grantBudget 用户确认继续后追加一倍预算额度。
func (a *Agent) grantBudget(taskID string) {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	if a.budget == nil {
		a.budget = map[string]*taskBudget{}
	}
	b := a.budget[taskID]
	if b == nil {
		b = &taskBudget{}
		a.budget[taskID] = b
	}
	b.extraCalls += a.Cfg.Agent.MaxLLMCallsPerTask
	b.extraTokens += a.Cfg.Agent.MaxTokensPerTask
}

// taskStarted 返回任务开始时间。
func (a *Agent) taskStarted(taskID string) (time.Time, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if h := a.tasks[taskID]; h != nil {
		return h.started, true
	}
	return time.Time{}, false
}

// overBudget 判断任务是否超出预算，返回超限原因（空表示未超限）。
// 预算全部为 0 时不做限制。
func (a *Agent) overBudget(taskID string) string {
	if a == nil || a.Cfg == nil {
		return ""
	}
	cfg := a.Cfg.Agent
	if cfg.MaxLLMCallsPerTask <= 0 && cfg.MaxTokensPerTask <= 0 && cfg.MaxTaskDurationSecs <= 0 {
		return ""
	}
	u := a.usageOf(taskID)
	extra := a.budgetExtra(taskID)
	if cfg.MaxLLMCallsPerTask > 0 {
		limit := cfg.MaxLLMCallsPerTask + extra.extraCalls
		if u.LLMCalls >= limit {
			return fmt.Sprintf("模型调用已达预算上限（%d 次）", limit)
		}
	}
	if cfg.MaxTokensPerTask > 0 {
		limit := cfg.MaxTokensPerTask + extra.extraTokens
		if u.TotalTokens() >= limit {
			return fmt.Sprintf("token 消耗已达预算上限（约 %d）", limit)
		}
	}
	if cfg.MaxTaskDurationSecs > 0 {
		if started, ok := a.taskStarted(taskID); ok {
			if elapsed := time.Since(started).Seconds(); elapsed >= float64(cfg.MaxTaskDurationSecs) {
				return fmt.Sprintf("任务已运行 %.0f 秒，超过时长预算（%d 秒）", elapsed, cfg.MaxTaskDurationSecs)
			}
		}
	}
	return ""
}

// checkBudget 超预算时征求用户确认：批准则追加一倍额度并继续，拒绝则停止。
func (a *Agent) checkBudget(taskID, reason string) bool {
	if a == nil || a.Notifier == nil {
		return false
	}
	resp := a.Notifier.OnApproval(types.ApprovalRequest{
		TaskID: taskID,
		Plan:   []string{"继续执行（再追加一倍预算）"},
		Risk:   "medium",
		Reason: reason + "。继续会继续产生调用开销，是否继续？",
	})
	if !resp.Approved {
		return false
	}
	a.grantBudget(taskID)
	return true
}

// reviewer 构造动作审核器：未开启审核或未配辅助模型时返回 nil（不拦截）。
func (a *Agent) reviewer() Reviewer {
	if a == nil || a.Cfg == nil || !a.Cfg.Safety.AIReview || a.FastLLM == nil {
		return nil
	}
	return &llmReviewer{LLM: a.FastLLM, TaskID: ""}
}

// appendUnique 追加不重复的元素。
func appendUnique(list []string, v string) []string {
	for _, s := range list {
		if s == v {
			return list
		}
	}
	return append(list, v)
}

// ---------- 防打转（跨重规划的原地打转检测） ----------

// noteSteps 记录本轮真正执行过的步骤指纹，返回达到阈值的指纹与其出现轮数。
// 单轮内同一指纹只计一次——去重产生的多条结果不会被重复计数。
// 只统计真正跑过的步骤：因依赖失败而跳过的不算一次尝试。
func (a *Agent) noteSteps(taskID string, steps []types.StepResult) (fp string, rounds int) {
	if a == nil || a.Cfg == nil || a.Cfg.Agent.StuckThreshold <= 0 {
		return "", 0
	}
	th := a.Cfg.Agent.StuckThreshold
	seen := make(map[string]struct{}, len(steps))
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	if a.stuck == nil {
		a.stuck = map[string]map[string]int{}
	}
	counts := a.stuck[taskID]
	if counts == nil {
		counts = map[string]int{}
		a.stuck[taskID] = counts
	}
	for _, st := range steps {
		if st.Status != types.StepSucceeded && st.Status != types.StepFailed {
			continue
		}
		f := stepFingerprint(st.Tool, st.FinalArgs)
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		counts[f]++
		if counts[f] >= th {
			return f, counts[f]
		}
	}
	return "", 0
}

// describeFingerprint 把「工具|参数」指纹还原成人能看懂的一句话。
func describeFingerprint(fp string) string {
	i := strings.Index(fp, "|")
	if i < 0 {
		return fp
	}
	tool, args := fp[:i], fp[i+1:]
	if args == "" || args == "{}" || args == "null" {
		return tool
	}
	return tool + " " + types.Shorten(args, 120)
}

// budgetCheckpoint 预算检查点：超限则通过审批通道询问用户是否继续。
// 返回 nil 表示可以继续；非 nil 表示应当中断任务（用户拒绝或无法询问）。
// 检查点放在顺序执行的位置（每轮规划前 / 反思前），避免在并发步骤里争抢审批通道。
func (a *Agent) budgetCheckpoint(ctx context.Context, taskID, goal string, steps []types.StepResult) *types.GoalResult {
	if a == nil || ctx == nil || ctx.Err() != nil {
		return nil
	}
	reason := a.overBudget(taskID)
	if reason == "" {
		return nil
	}
	if a.Notifier != nil {
		a.Notifier.OnProgress(types.ProgressEvent{
			TaskID: taskID, Phase: "budget", Message: "⚠ " + reason, Progress: 88, Kind: "warn",
		})
	}
	if a.checkBudget(taskID, reason) {
		return nil
	}
	return &types.GoalResult{
		Goal:   goal,
		Status: types.GoalFailed,
		Error:  "超出任务预算，已停止：" + reason,
		Steps:  steps,
		Score:  0,
	}
}

// installUsageHook 注册一次全局用量钩子：按 taskID 认领本引擎的调用。
func (a *Agent) installUsageHook() {
	a.hookOnce.Do(func() {
		llm.AddUsageHook(func(taskID, kind string, req llm.ChatRequest, text string, u llm.Usage) {
			a.addUsage(taskID, u)
		})
	})
}

// addUsage 累加一次模型调用用量；非本引擎的任务（taskID 未登记）直接忽略。
func (a *Agent) addUsage(taskID string, u llm.Usage) {
	if taskID == "" {
		return
	}
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	st := a.usage[taskID]
	if st == nil {
		return
	}
	st.LLMCalls++
	st.PromptTokens += u.PromptTokens
	st.CompletionTokens += u.CompletionTokens
	if u.CachedTokens > 0 {
		st.CachedTokens += u.CachedTokens
		st.CachedCalls++
	}
	if u.Estimated {
		st.EstimatedCalls++
	}
}

// BumpUsage 累加工具调用、重试与去重次数（由执行器调用）。
func (a *Agent) BumpUsage(taskID string, toolCalls, retries, deduped int) {
	if taskID == "" || (toolCalls == 0 && retries == 0 && deduped == 0) {
		return
	}
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	if st := a.usage[taskID]; st != nil {
		st.ToolCalls += toolCalls
		st.Retries += retries
		st.Deduped += deduped
	}
}

// usageOf 返回任务用量快照。
func (a *Agent) usageOf(taskID string) types.TaskUsage {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	if st := a.usage[taskID]; st != nil {
		return *st
	}
	return types.TaskUsage{}
}

// dropUsage 释放任务用量计数（任务淘汰时调用，避免长期运行内存增长）。
func (a *Agent) dropUsage(taskID string) {
	a.usageMu.Lock()
	delete(a.usage, taskID)
	delete(a.budget, taskID)
	delete(a.stuck, taskID)
	a.usageMu.Unlock()
}

// runGoalLoop Plan-Execute-Reflect 主循环（含自动重规划）。
// normalizeTaskMode 归一化任务模式（chat | code，默认 work）。
func normalizeTaskMode(m types.TaskMode) types.TaskMode {
	switch types.TaskMode(strings.TrimSpace(string(m))) {
	case types.TaskChat:
		return types.TaskChat
	case types.TaskCode:
		return types.TaskCode
	default:
		return types.TaskWork
	}
}

// goalProgress 主循环内部的一次性事实，供交付侧口径（首次通过 / 返工轮数）使用。
//
// 为什么不让调用方从 result 反推：**反推不出来**。"第几轮才成"与"返工了几次"是
// 循环内部的局部事实——重试三次才成与一次做对，最后的 status/score 完全一样，
// 只有循环自己知道中间多转了两圈。所以由循环写、由 RunGoal 读，而不是事后猜。
type goalProgress struct {
	attempts   int  // 实际走到第几轮尝试；对话模式为 0（没进过循环）
	reworks    int  // 反思判未达标后真正重新执行过的轮数
	firstRound bool // 是否在第一轮尝试就走到了完成判定
}

// stamp 把循环里的交付侧事实写回结果。
//
// 只在真正进过工作循环时写：对话模式没有"轮"的概念，两个字段由对话管线按自己的
// 核对结论给（见 runChatPath）。无条件写会把那边已经给出的结论覆盖成零值，
// 于是"核对通过的对话"会被统计成"没通过首次验收"。
func (p *goalProgress) stamp(res *types.GoalResult) {
	if p == nil || res == nil || p.attempts == 0 {
		return
	}
	res.Reworks = p.reworks
	// 首次验收通过 = 第一轮走完流程 **且最终确实算成功**。两个条件都要：
	// 完成判定通过之后 applyAcceptanceVerdict 仍可能把状态校正成"部分完成"
	// （产物核对不过、反思器没核对），那不是"一次就合格"。
	res.FirstPass = p.firstRound && res.Status == types.GoalSuccess
}

// runGoalLoop Plan-Execute-Reflect 主循环（含自动重规划）。taskMode=chat 时走直聊管线。
// prog 由调用方传入并回读，记录"第几轮才成、返工了几次"（见 goalProgress）。
func (a *Agent) runGoalLoop(ctx context.Context, req types.GoalRequest, goal, mode string, taskMode types.TaskMode, taskID string, prog *goalProgress) *types.GoalResult {
	// 结构化引用由 Web UI 注入；在规划提示前转为明确的上下文段落，
	// 同时保留原始目标文本，避免引用只停留在界面芯片层。
	refs := req.References
	if len(refs) == 0 {
		if legacy, ok := req.Context["references"].([]map[string]string); ok {
			for _, ref := range legacy {
				refs = append(refs, types.Reference{Kind: ref["kind"], Label: ref["label"], Value: ref["value"]})
			}
		}
	}
	if len(refs) > 0 {
		var b strings.Builder
		b.WriteString("目标上下文引用（仅作为用户提供的工作上下文，不要把标记本身当作任务）：\n")
		for _, ref := range refs {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", ref.Kind, ref.Label, ref.Value)
		}
		goal = b.String() + "\n用户目标：" + goal
	}
	// ---------- 对话模式：直连 LLM，不经规划/执行/反思 ----------
	if taskMode == types.TaskChat {
		return a.runChatPath(ctx, goal, taskID, req.Role)
	}
	if a.Mem != nil {
		a.Mem.AddTurn("user", goal)
	}
	cwd := a.workspaceOf(req)

	notify := func(phase, msg string, pct int, kind string) {
		a.Notifier.OnProgress(types.ProgressEvent{TaskID: taskID, Phase: phase, Message: msg, Progress: pct, Kind: kind})
	}

	// 场景模板的模型档位：角色声明了档位且配置里有对应模型时换模型，否则就是主模型。
	// 规划、执行、对话都走 TierLLM（BuildPlanner 内部解析的就是它）——同一场景内换模型
	// 没有意义，只会让风格前后不一致。
	//
	// **反思是例外**：反思是验收，走 VerifyLLM 换一个不同档位的模型。
	// 原先这里复用同一个客户端，于是"独立验收"只体现在"独立调用 + 独立上下文"上，
	// 干活的和打分的仍是同一个模型——盲区相同，自评偏差没有任何东西去抵消。
	planner := a.BuildPlanner(req)
	reflector := &Reflector{
		LLM:           a.VerifyLLM(req.Role),
		DoneThreshold: a.Cfg.Agent.DoneThreshold,
		TaskID:        taskID,
	}
	tell := Narrator{Style: a.Cfg.Persona.Style}

	var feedback string
	var last *types.GoalResult
	var usedTools []string  // 本任务各轮实际用到的工具（菜单模式下重规划时强制带上 schema）
	var acceptance []string // 验收标准：一旦定下就跟着这个目标走，重规划重申与否都不丢
	planned := false
	attempts := a.Cfg.Agent.MaxReplans + 1
	if attempts < 1 {
		attempts = 1
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		if prog != nil {
			prog.attempts = attempt
		}
		if ctx.Err() != nil {
			return a.cancelledResult(goal, last)
		}
		// ---------- 预算熔断检查点 ----------
		if stop := a.budgetCheckpoint(ctx, taskID, goal, nil); stop != nil {
			notify("budget", stop.Error, 88, "error")
			a.finalize(taskID, stop, nil, req.Role)
			return stop
		}
		// ---------- Plan ----------
		notify("plan", tell.PlanStart(), 3, "info")
		// 上下文自动压缩：短期窗口外被覆盖的旧对话汇总为滚动摘要（§9 风险应对）
		if a.Cfg.Agent.ContextCompress && a.Mem != nil {
			if a.Mem.Compress(a.CompressSummarizer(ctx, taskID)) {
				notify("plan", tell.Compressed(), 4, "info")
			}
		}
		var recent []memory.Turn
		if a.Mem != nil && a.Mem.Short != nil {
			recent = a.Mem.Short.Recent(6)
		}
		var relevant []memory.Hit
		if a.Mem != nil && a.Mem.Long != nil {
			relevant = a.Mem.Relevant(goal, 3)
		}
		var summary string
		if a.Mem != nil {
			// 带省略量标注的摘要：「已自动压缩」要说清丢了多少，否则读者分不清全貌与残余
			summary = a.Mem.SummaryAnnotated()
		}
		// 计划流只报进度，不转发内容：规划响应本身就是 JSON，逐 token 转给
		// CLI/WebUI 会把原始 JSON 碎片打进进度行（2026-09-23 QA 报告 M5）。
		var planChars int
		onDelta, flush := DeltaThrottle(50, func(text string) {
			planChars += len([]rune(text))
			notify("plan", fmt.Sprintf("规划中…（已生成 %d 字）", planChars), 5, "llm")
		})
		planner.OnLLMDelta = onDelta
		// 菜单模式下把此前各轮用过的工具固定带上，避免重规划时想用却没有 schema
		planner.ForceTools = usedTools
		plan, err := planner.Plan(ctx, goal, cwd, recent, relevant, summary, feedback)
		flush()
		if err != nil {
			if ctx.Err() != nil {
				return a.cancelledResult(goal, last)
			}
			// 模型调用失败（鉴权/网络/限流）：重试无意义，立即走直聊回落给出真实错误；
			// 只有计划校验失败才值得重规划。
			if attempt == attempts || strings.Contains(err.Error(), "llm:") {
				notify("plan", "规划未成功，尝试直接回答…", 20, "warn")
				last = &types.GoalResult{Goal: goal, Status: types.GoalFailed, Error: err.Error(), Score: 0}
				break
			}
			feedback = "上一次计划无效，请修正：" + err.Error()
			notify("plan", "计划校验未通过，正在重新规划…", 5, "warn")
			continue
		}
		notify("plan", tell.PlanDone(len(plan.Steps)), 8, "info")
		planned = true
		// 验收标准只认第一次定下的那份：它是给用户的承诺，不该由没做到的那一方在重试时改写。
		// 顺带解决"重规划没重申 → 验收悄悄消失 → 又能靠一句 done 蒙混过关"。
		if len(acceptance) == 0 {
			acceptance = plan.Acceptance
		}
		plan.Acceptance = acceptance
		// 记下本轮用到的工具：下一轮规划时它们的 schema 一定在（菜单模式的漏筛兜底）
		for _, st := range plan.Steps {
			usedTools = appendUnique(usedTools, st.Tool)
		}

		// plan_first：先展示完整计划，一次批准
		preApproved := false
		if mode == string(types.ModePlanFirst) {
			resp := a.Notifier.OnApproval(types.ApprovalRequest{
				TaskID: taskID, Plan: planDescriptions(plan), WholePlan: true,
				Risk: planMaxRisk(a.Reg, plan), Reason: "plan_first 模式：执行前需要确认完整计划",
			})
			if !resp.Approved {
				// 这一行是用户会在卡片上读到的原因，不能一律写成"用户拒绝了执行计划"：
				// 取消（宿主摘掉这扇门把他叫醒）和超时自动拒绝都不是用户的拒绝。
				cause := "用户拒绝了执行计划"
				switch {
				case ctx.Err() != nil:
					cause = "任务已取消"
				case resp.Note != "":
					cause = resp.Note
				}
				res := &types.GoalResult{Goal: goal, Status: types.GoalCancelled, Error: cause, Score: 0}
				a.finalize(taskID, res, nil, req.Role)
				return res
			}
			preApproved = true
		}

		// ---------- Execute ----------
		executor := &Executor{
			Reg: a.Reg, Gate: a.Gate, Notifier: a.Notifier,
			MaxConcurrency: a.Cfg.Agent.MaxConcurrency,
			StepTimeout:    time.Duration(a.Cfg.Agent.StepTimeoutSecs) * time.Second,
			StepRetries:    a.Cfg.Agent.StepRetries,
			Dedupe:         a.Cfg.Agent.DedupeCalls,
			MaxOutputRunes: a.Cfg.Agent.MaxOutputRunes,
			DataDir:        a.Cfg.DataDir,
			OnUsage:        a.BumpUsage,
			// 运行中落盘：进程若在这次运行里退出，tasks/ 里不会有任何东西，
			// runs/<taskID>.jsonl 是唯一能回答"跑到哪一步"的凭据。
			// 正常结束时会由 DiscardRunLog 删掉（终态快照更完整，留着是噪音）。
			Sink: NewRunLog(a.Cfg.DataDir),
			Goal: goal,
			// 审核模型只在真配了辅助模型时启用：用主模型做快筛等于多花一份钱
			Reviewer: a.reviewer(),
		}
		exec := executor.Execute(ctx, plan, taskID, mode, preApproved)
		if ctx.Err() != nil && exec.Succeeded == 0 {
			res := &types.GoalResult{Goal: goal, Status: types.GoalCancelled, Score: 0, Error: "任务已取消", Steps: exec.Steps, ExecutedPlan: &exec.ExecutedPlan}
			a.finalize(taskID, res, nil, req.Role)
			return res
		}

		// ---------- 预算熔断检查点（执行后、反思前） ----------
		if stop := a.budgetCheckpoint(ctx, taskID, goal, exec.Steps); stop != nil {
			notify("budget", stop.Error, 92, "error")
			a.finalize(taskID, stop, &plan, req.Role)
			return stop
		}

		// ---------- Reflect ----------
		notify("reflect", tell.ReflectStart(), 92, "info")
		// 验收标准在规划时就定好了，这里交给反思器逐条判定（而不是让它凭感觉打分）
		reflector.Acceptance = plan.Acceptance
		reflection := reflector.Evaluate(ctx, goal, exec, attempt)
		result := a.buildResult(goal, exec, reflection, plan.Acceptance, planner.LastBreakdown)
		last = result

		// 完成判定：分数达标或模型说 done，且验收标准全部通过、产物核对也通过。
		// 抽成 completionReached 是为了让这三条守卫各自可测——它原先是一个长条件，
		// 少写一句也看不出来（见 TestCompletionReached_*）。
		if completionReached(reflection, plan.Acceptance, exec, a.Cfg.Agent.DoneThreshold) {
			if prog != nil && attempt == 1 {
				prog.firstRound = true
			}
			result.Status = statusOfExec(exec)
			if reflection.Suggestion != "" {
				result.Suggestion = reflection.Suggestion
			}
			a.finalize(taskID, result, &plan, req.Role)
			return result
		}
		// ---------- 防打转：同一动作反复出现说明在原地打转，别再耗重规划次数 ----------
		// 放在「最后一次重规划」判定之前，否则默认参数下永远不会触发（此时循环本来也要结束）。
		if fp, rounds := a.noteSteps(taskID, exec.Steps); fp != "" {
			stuckMsg := fmt.Sprintf("检测到原地打转：%s 已重复执行 %d 轮", describeFingerprint(fp), rounds)
			notify("reflect", "⚠ "+stuckMsg+"，停止自动重规划", 90, "warn")
			result.Status = statusOfExec(exec)
			if result.Status == types.GoalRunning || result.Status == types.GoalSuccess {
				result.Status = types.GoalFailed
			}
			result.Error = stuckMsg + "。继续重规划大概率还是同样结果。"
			result.Suggestion = "建议把目标说得更具体（例如指定要处理的文件或期望的输出格式），或补充缺少的信息后重试；也可以切换到「对话」模式先对齐需求。"
			a.finalize(taskID, result, &plan, req.Role)
			return result
		}
		if reflection.Verdict == "failed" || attempt == attempts {
			result.Status = statusOfExec(exec)
			if result.Status == types.GoalRunning || result.Status == types.GoalSuccess {
				if reflection.Verdict == "failed" {
					result.Status = types.GoalFailed
				}
			}
			if reflection.Suggestion != "" {
				result.Suggestion = reflection.Suggestion
			}
			a.finalize(taskID, result, &plan, req.Role)
			return result
		}

		// 继续重规划。把上次每一步的结果一并带上——原先只给一个分数和一句话，
		// 第二轮规划与第一轮几乎同样盲（文案写着"可参考"，实际没东西可参考），
		// 这正是"原地打转"频繁触发的原因之一。
		//
		// 这里才是唯一算"返工"的地方：执行过了、被判定不达标、于是重做一遍。
		// 上面那处"计划校验未通过→重规划"不算——那一步什么都没执行，谈不上重做，
		// 把它计进返工会让这个指标随"计划写得好不好"漂移，而不是随交付质量漂移。
		if prog != nil {
			prog.reworks++
		}
		feedback = fmt.Sprintf("完成度 %d/100。问题：%s\n\n上次执行的情况（针对它们改，不要原样重来）：\n%s",
			reflection.Score, reflection.Reason, buildStepDigest(exec))
		notify("reflect", tell.Replan(reflection.Score), 90, "warn")
	}
	// 规划从未产出有效计划（典型：纯聊天目标误入工作模式）→ 回落直聊，不再硬失败
	if !planned && (last == nil || last.Status == types.GoalFailed) {
		notify("plan", "多次规划未成功，切换直接回答…", 20, "warn")
		res := a.runChatPath(ctx, goal, taskID, req.Role)
		if res.Status == types.GoalSuccess {
			res.Score = 70
			res.Suggestion = "这个问题更适合「对话」模式，切换后响应更快"
		}
		// 交付物是直聊给的，不是工作循环给的 → prog 保持"没走到完成判定"的原样，
		// 于是这次交付落成 FirstPass=false。**不能把它从口径里摘出去**：
		// 规划屡败正是最差的交付，把最差的从分母里拿掉，是在把比率改成好看的样子。
		return res
	}
	return a.cancelledResult(goal, last)
}

// runChatPath 对话模式管线：记忆 + 滚动摘要注入，直连 LLM 流式回答。
// 不经规划/执行/反思；工作模式规划屡败时也回落到此路径兜底。
func (a *Agent) runChatPath(ctx context.Context, goal, taskID, role string) *types.GoalResult {
	notify := func(phase, msg string, pct int, kind string) {
		a.Notifier.OnProgress(types.ProgressEvent{TaskID: taskID, Phase: phase, Message: msg, Progress: pct, Kind: kind})
	}
	if a.Mem != nil {
		a.Mem.AddTurn("user", goal)
		if a.Cfg.Agent.ContextCompress && a.Mem.Compress(a.CompressSummarizer(ctx)) {
			notify("chat", "已自动压缩早期上下文", 15, "info")
		}
	}
	name := a.Cfg.Persona.Name
	if strings.TrimSpace(name) == "" {
		name = "Gleam"
	}
	// 稳定段在前、易变段在后（同规划器）：多轮对话里 system + 前面的消息是天然的长公共前缀，
	// 把工作目录、摘要这类每轮都可能变的内容塞在前面，会把整段前缀废掉。
	sys := fmt.Sprintf("你是 %s（微光），运行在用户本机的桌面智能体，当前对话模式。"+
		"用与用户一致的语言自然对话：回答简洁、具体、可执行；涉及本机文件或命令的操作，"+
		"提示切换到「工作」或「编程」模式提交。%s", name, styleInstruction(a.Cfg.Persona.Style))
	var volatile strings.Builder
	fmt.Fprintf(&volatile, "\n\n## 当前环境\n工作目录：%s", a.Cfg.Workspace)
	if a.Mem != nil {
		if s := a.Mem.Summary(); strings.TrimSpace(s) != "" {
			volatile.WriteString("\n\n## 早期上下文摘要\n" + types.Shorten(s, 400))
		}
	}
	sys += volatile.String()
	var msgs []llm.Message
	if a.Mem != nil && a.Mem.Short != nil {
		for _, t := range a.Mem.Short.Recent(6) {
			role := llm.RoleUser
			if t.Role == "assistant" {
				role = llm.RoleAssistant
			}
			msgs = append(msgs, llm.Message{Role: role, Content: t.Content})
		}
	}
	// 最近一轮就是本次用户消息（AddTurn 已入短期记忆），避免重复注入
	if n := len(msgs); n > 0 && strings.TrimSpace(msgs[n-1].Content) == strings.TrimSpace(goal) {
		msgs = msgs[:n-1]
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: goal})

	notify("chat", "思考中…", 30, "info")
	onDelta, flush := DeltaThrottle(40, func(text string) {
		notify("chat", strings.TrimSpace(text), 55, "llm")
	})
	text, err := a.TierLLM(role).ChatStream(ctx, llm.ChatRequest{
		System:      "你是 " + name + " 的对话管线。" + llm.MarkerChat + "\n\n" + sys,
		Messages:    msgs,
		Temperature: 0.6,
		TaskID:      taskID,
	}, onDelta)
	flush()
	if err != nil {
		if ctx.Err() != nil {
			return &types.GoalResult{Goal: goal, Status: types.GoalCancelled, Error: "任务已取消"}
		}
		notify("chat", "模型调用失败: "+err.Error(), 100, "error")
		res := &types.GoalResult{Goal: goal, Status: types.GoalFailed, Score: 0, Error: err.Error()}
		a.finalize(taskID, res, nil, role)
		return res
	}
	if a.Mem != nil {
		a.Mem.AddTurn("assistant", text)
	}
	// 回答已产出，交给辅助模型独立核对一次——对话模式没有规划阶段，
	// 这是唯一能拦住"干活的自己报满分"的地方。核对不成一律不阻断对话。
	if a.shouldChatCheck(goal, text) {
		notify("chat", "核对回答是否达成目标…", 95, "info")
	}
	res := &types.GoalResult{Goal: goal, Status: types.GoalSuccess, Score: 100, Summary: text}
	criteria, checks, outcome := a.chatSelfCheck(ctx, goal, text, taskID)
	if outcome == chatCheckDone {
		applyChatCheck(res, criteria, checks)
		// 通过就不必多嘴——清单会显示在任务卡上；没全过才值得提一句
		if res.Status != types.GoalSuccess {
			notify("chat", "自检未全过："+res.Error, 100, "warn")
		}
	} else if outcome == chatCheckUnusable {
		// 核对确实跑了，但调用失败或输出读不出来，没得出判定。
		// 状态与分数保持原样——回答已经交付，分数若改成 0，前端会渲染成红色的
		// "完成度 0/100"，那是在往反方向说谎（回答可能很好，只是没核对）。
		// 但**不能让用户以为核对过了**：静默保留 100 分正是这个功能要消灭的东西。
		notify("chat", "对话自检未能完成，本次回答未经核对", 100, "warn")
	}
	notify("chat", "已回复", 100, "info")
	a.finalize(taskID, res, nil, role)
	return res
}

// finalize 收尾：写入记忆、发出主动提议与技能固化建议；随后异步压缩上下文
// （不阻塞完成事件；溢出为空时是空操作）。
func (a *Agent) finalize(taskID string, result *types.GoalResult, plan *types.Plan, role string) {
	// 先按验收结果校正状态，再走后面的留档/提议——否则"部分完成"会被当成成功记进成长统计。
	var acceptance []string
	if plan != nil {
		acceptance = plan.Acceptance
	}
	applyAcceptanceVerdict(result, acceptance)
	if result.Summary != "" && a.Mem != nil {
		a.Mem.AddTurn("assistant", result.Summary)
	}
	if a.Cfg.Agent.ContextCompress && a.Mem != nil {
		ctxC, cancelC := context.WithTimeout(context.Background(), 30*time.Second)
		go func() {
			defer cancelC()
			a.Mem.Compress(a.CompressSummarizer(ctxC, taskID))
		}()
	}
	// 长期记忆沉淀任务记录（尽力而为）
	goal := result.Goal
	if goal != "" && a.Mem != nil && a.Mem.Long != nil {
		_, err := a.Mem.Remember(fmt.Sprintf("任务记录[%s] 目标：%s 结果：%s（完成度 %d）",
			result.Status, goal, types.Shorten(result.Summary, 200), result.Score), []string{"task"})
		if err == nil {
			_ = a.Mem.Long.Flush()
		}
	}
	if result.Suggestion != "" {
		a.Notifier.OnSuggestion(taskID, result.Suggestion)
	}
	// GEO 优化建议：创作类产出成功后分析并留档（writer 角色或识别为创作意图的任务）
	if a.shouldAnalyzeGEO(role, result) {
		a.analyzeGEOAsync(taskID, result.Goal, result.Summary)
	}
	// 技能固化建议：成功且 ≥2 个非 reply 步骤
	if plan != nil && result.Status == types.GoalSuccess {
		nonReply := 0
		for _, st := range plan.Steps {
			if st.Tool != "reply" {
				nonReply++
			}
		}
		if nonReply >= 2 {
			a.Notifier.OnSuggestSkill(taskID, types.SkillDraft{
				Name:        skillSlug(goal, a.Skills),
				Description: goal,
				Steps:       plan.Steps,
			})
		}
	}
}

// minGEOContentRunes 触发 GEO 自动分析的最短产出长度：
// 太短的回复（如"好的""已完成"）没有可优化的结构，不做分析以免刷屏。
const minGEOContentRunes = 80

// shouldAnalyzeGEO 判断本次产出是否值得做 GEO 分析。
// 条件：开关打开 + 任务成功 + 产出够长 + 属于创作（writer 角色或命中创作意图）。
func (a *Agent) shouldAnalyzeGEO(role string, result *types.GoalResult) bool {
	if a == nil || a.LLM == nil || result == nil || a.Cfg == nil {
		return false
	}
	if !a.Cfg.Agent.GEOEnabled {
		return false
	}
	if result.Status != types.GoalSuccess {
		return false
	}
	if len([]rune(strings.TrimSpace(result.Summary))) < minGEOContentRunes {
		return false
	}
	return geo.IsCreative(result.Goal, role)
}

// analyzeGEOAsync 异步分析创作产出：留档到 GEO 历史，并把建议推送给前端。
// 分析失败只静默跳过——它是对成功任务的增强，不能反过来影响任务结果。
func (a *Agent) analyzeGEOAsync(taskID, goal, content string) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	go func() {
		defer cancel()
		suggestion, err := (&geo.Analyzer{LLM: a.HelperLLM(), TaskID: taskID}).Analyze(ctx, content)
		if err != nil || suggestion == nil {
			return
		}
		if a.GEO != nil {
			a.GEO.Add(geo.Record{
				TaskID:      taskID,
				Goal:        types.Shorten(goal, 140),
				Score:       suggestion.Score,
				Summary:     suggestion.Summary,
				Strengths:   suggestion.Strengths,
				Weaknesses:  suggestion.Weaknesses,
				Actionables: suggestion.Actionables,
				Source:      "auto",
			})
		}
		a.Notifier.OnSuggestion(taskID, geo.Format(suggestion))
	}()
}

// CompressSummarizer 生成上下文压缩用的摘要回调（LLM 失败时由记忆层回退本地抽取式摘要）。
// taskID 为空表示这次压缩不属于任何任务（如设置页手动压缩），不计入任务消耗。
func (a *Agent) CompressSummarizer(ctx context.Context, taskID ...string) func(string) (string, error) {
	var owner string
	if len(taskID) > 0 {
		owner = taskID[0]
	}
	return func(overflow string) (string, error) {
		sys := "你是 Gleam 的上下文压缩器。" + llm.MarkerCompress + "\n\n" +
			"接下来是一条包含早期对话记录的消息。请用不超过 200 字的中文摘要，保留对后续任务仍有价值的信息：用户偏好、项目背景、已做的决定、遗留问题。只输出摘要正文。"
		req := llm.ChatRequest{System: sys, Messages: []llm.Message{{Role: llm.RoleUser, Content: overflow}}, Temperature: 0.2, TaskID: owner}
		return a.HelperLLM().Chat(ctx, req)
	}
}

// autoOptimizeSkill 技能运行失败后的自动参数优化（设计文档 §4.2.2）：
// 把失败摘要交给 LLM 修订步骤，经规划校验后保存为新版本。Mock 模型下跳过，保证离线自测确定性。
func (a *Agent) autoOptimizeSkill(ctx context.Context, taskID, name string, sk *skill.Skill, exec *Result) {
	if a.LLM == nil || llm.IsMockClient(a.LLM) || !a.Cfg.Agent.SkillAutoOptimize {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## 技能 %s（当前 v%d）运行失败\n目标：%s\n\n## 现有步骤\n", name, sk.Version, sk.Description)
	for _, st := range sk.Steps {
		args, _ := json.Marshal(st.Args)
		fmt.Fprintf(&b, "- {\"id\":%q,\"description\":%q,\"tool\":%q,\"args\":%s,\"depends_on\":%s}\n",
			st.ID, st.Description, st.Tool, args, stepDepsJSON(st.DependsOn))
	}
	fmt.Fprintf(&b, "\n## 失败详情\n")
	for _, r := range exec.Steps {
		if r.Status == types.StepFailed {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", r.StepID, r.Tool, types.Shorten(r.Error, 160))
		}
	}
	sys := strings.Join([]string{
		"你是 Gleam 的技能优化器。" + llm.MarkerSkillFix,
		"",
		b.String(),
		`
## 输出（只输出严格 JSON，不要 markdown 代码块）
{"steps":[{"id":"s1","description":"一句话说明","tool":"工具名","args":{},"depends_on":[]}]}

针对失败原因修订：调整参数取值、修正步骤引用（"$ref:s2"、"{ref:s2}"）、或删除多余步骤。
只能使用原技能中出现过的工具，保持技能语义不变。`,
	}, "\n")
	req := llm.ChatRequest{System: sys, Messages: []llm.Message{{Role: llm.RoleUser, Content: "请输出修订后的步骤。"}}, Temperature: 0.2, TaskID: taskID}
	text, err := a.HelperLLM().Chat(ctx, req)
	if err != nil {
		return
	}
	raw, err := extractJSON(text)
	if err != nil {
		return
	}
	validator := &Planner{LLM: a.LLM, Reg: a.Reg, MaxSteps: a.Cfg.Agent.MaxSteps}
	plan, err := validator.validate(raw, "技能 "+name)
	if err != nil || len(plan.Steps) == 0 {
		return
	}
	optimized := *sk
	optimized.Steps = plan.Steps
	if v, err := a.SkillSave(optimized); err == nil {
		a.Notifier.OnProgress(types.ProgressEvent{
			TaskID: taskID, Phase: "reflect",
			Message:  fmt.Sprintf("技能 %s 运行失败，已自动优化参数并保存为 v%d，可重试", name, v),
			Progress: 90, Kind: "warn",
		})
	}
}

// stepDepsJSON 步骤依赖列表的 JSON 表示（用于优化器提示词）。
func stepDepsJSON(deps []string) string {
	if len(deps) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(deps)
	return string(b)
}

func (a *Agent) buildResult(goal string, exec *Result, refl types.Reflection, acceptance []string, bd types.ContextBreakdown) *types.GoalResult {
	res := &types.GoalResult{
		Goal:  goal,
		Score: refl.Score,
		Steps: exec.Steps,
		// 承诺了什么、验到哪一步——让"完成"可核对，而不只是一个分数
		Acceptance: acceptance,
		Checks:     refl.Checks,
		// 参数替换后的实际执行计划：tasks/<id>.json 有了它，"当时打算怎么做"
		// 才能离线回放（gleam replay），而不是只留一个结果让复盘的人猜。
		ExecutedPlan:     &exec.ExecutedPlan,
		PromptBreakdown:  &bd,
		FailureBreakdown: failureBreakdown(exec.Steps),
		Changes:          buildChanges(exec.Steps, exec.PreImages),
	}
	res.Summary = exec.ReplyText
	if res.Summary == "" {
		if exec.Succeeded == exec.Total && exec.Total > 0 {
			res.Summary = fmt.Sprintf("已完成全部 %d 个步骤。", exec.Total)
		} else {
			res.Summary = fmt.Sprintf("执行了 %d 个步骤：成功 %d，失败 %d，跳过 %d。%s",
				exec.Total, exec.Succeeded, exec.Failed, exec.Skipped, refl.Reason)
		}
	}
	if refl.Verdict == "failed" {
		res.Status = types.GoalFailed
	}
	return res
}

func statusOfExec(exec *Result) types.GoalStatus {
	switch {
	case exec.Cancelled && exec.Succeeded == 0:
		return types.GoalCancelled
	case exec.Succeeded == exec.Total && exec.Total > 0:
		return types.GoalSuccess
	case exec.Succeeded == 0:
		return types.GoalFailed
	default:
		return types.GoalPartial
	}
}

// failureBreakdown 统计失败步骤的归因分布。
// 失败率只回答"坏了多少"，这份分布回答"该先修哪一层"——
// 参数错要改参数、业务拒绝要换方案、权限不足重试无用，混在一起就是噪音。
func failureBreakdown(steps []types.StepResult) map[types.ErrorKind]int {
	var out map[types.ErrorKind]int
	for _, st := range steps {
		if st.Status != types.StepFailed && st.Outcome != types.OutcomeFailed {
			continue
		}
		kind := st.ErrorKind
		if kind == "" {
			kind = types.ErrUnknown
		}
		if out == nil {
			out = map[types.ErrorKind]int{}
		}
		out[kind]++
	}
	return out
}

// deriveTraceID 由任务内容派生 trace 短 ID（FNV-1a，截断到 40 位、10 位十六进制）。
// 输入相同 → ID 相同，这是"同一问题反复出现可自动聚类"的根据。
func deriveTraceID(parts ...string) string {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0x1f})
	}
	return fmt.Sprintf("%010x", h.Sum64()&0xFFFFFFFFFF)
}

// completionReached 判定这次尝试是否够格算"完成"。三条守卫缺一不可：
//
//  1. 模型说 done，或分数达到阈值——这是原来就有的那条；
//  2. 验收标准全部通过——否则把阈值调低、或模型自己说一句 done，
//     就能绕过逐条判定，让"没做到"变成"已完成"；
//  3. 产物核对通过——**不看有没有验收标准**，它判的是代码实测到的现实。
//     只靠第 1 条不够：产物核对不过时反思器已被压成 replan，但分数达标的
//     那半边仍然会放行，所以必须在这里独立判一次。
//
// 抽成独立函数而不是留在 if 里：长条件少写一句看不出来，拆开后每条都能单测。
func completionReached(refl types.Reflection, acceptance []string, exec *Result, doneThreshold int) bool {
	if refl.Verdict != "done" && refl.Score < doneThreshold {
		return false
	}
	if !acceptanceSatisfied(acceptance, refl.Checks) {
		return false
	}
	var steps []types.StepResult
	if exec != nil {
		steps = exec.Steps
	}
	return artifactsSatisfied(verifyArtifacts(steps))
}

// acceptanceSatisfied 验收标准是否全部通过。三种情况必须分清，混在一起就会出现
// 「反思器一挂、验收被整体绕过」那个洞：
//
//   - 没有验收标准（len(acceptance)==0）：返回 true，即"不干预原有流程"。
//     没承诺过什么，就没什么可核对的。
//   - 有标准、也有判定：逐条看，全过才 true。
//   - **有标准、却没有判定**（checks 为空）：返回 **false**。
//     这不是"没问题"，而是"没核对过"——反思器故障回退时正是这个形态。
//     原先它与第一种情况合并返回 true，于是反思器一失败，
//     heuristicReflection 给出 done/85，这里再放行，任务就以"已完成"收尾，
//     而它承诺的验收标准一条都没被核对过。判定缺失应当进失败处理，不能算通过。
func acceptanceSatisfied(acceptance []string, checks []types.CheckResult) bool {
	if len(acceptance) == 0 {
		return true
	}
	if len(checks) == 0 {
		return false
	}
	for _, c := range checks {
		if !c.Passed {
			return false
		}
	}
	return true
}

// applyAcceptanceVerdict 用验收结果校正"成功"这个结论。
// 步骤全部执行成功 ≠ 目标达成：写了文件不等于内容对。没有验收标准时什么都不做（向后兼容）。
// 只纠正被误判为成功的结论——已经判为失败/部分完成/取消的，不在这里翻案。
//
// 它是"完成"这道门禁的最后一道闸：acceptanceSatisfied 管的是**循环要不要继续**，
// 这里管的是**对外怎么声称**。两处都得堵，否则反思器故障时循环不早退、
// 走到最后一次尝试仍会被 statusOfExec 判成 GoalSuccess。
//
// 校正分两级，**产物核对在前**：它判的是代码实测到的现实（说好的文件在不在），
// 与"有没有承诺过验收标准"无关——没有验收标准的任务同样可能"步骤都报成功、
// 产物其实不在"，而 statusOfExec 只看步骤计数，一样会把它判成成功。
func applyAcceptanceVerdict(res *types.GoalResult, acceptance []string) {
	if res == nil || res.Status != types.GoalSuccess {
		return
	}
	if bad := failedArtifacts(verifyArtifacts(res.Steps)); len(bad) > 0 {
		res.Status = types.GoalPartial
		if res.Error == "" {
			res.Error = artifactFailureNote(bad) + "。步骤自述成功，但代码实测对不上。"
		}
		return
	}
	if len(acceptance) == 0 {
		return
	}
	// 有标准却没判定：反思器没能核对（不可用或输出无法解析）。
	// 此时既不能说"已完成"（没核对过），也不必说"失败"（步骤其实都跑成功了）——
	// 如实落成"部分完成"并写明原因，让人知道差的是核对这一步。
	if len(res.Checks) == 0 {
		res.Status = types.GoalPartial
		if res.Error == "" {
			res.Error = fmt.Sprintf("验收标准（%d 条）未能核对：反思器不可用或输出无法解析，无法确认是否达标",
				len(acceptance))
		}
		return
	}
	missed := missedCriteria(res.Checks)
	if len(missed) == 0 {
		return
	}
	if countPassed(res.Checks) == 0 {
		res.Status = types.GoalFailed
	} else {
		res.Status = types.GoalPartial
	}
	if res.Error == "" {
		res.Error = fmt.Sprintf("验收未全部通过（%d/%d），未达标：%s",
			countPassed(res.Checks), len(res.Checks), strings.Join(missed, "；"))
	}
}

func (a *Agent) cancelledResult(goal string, last *types.GoalResult) *types.GoalResult {
	if last != nil {
		last.Status = types.GoalCancelled
		last.Error = "任务已取消"
		return last
	}
	return &types.GoalResult{Goal: goal, Status: types.GoalCancelled, Error: "任务已取消"}
}

func (a *Agent) resultOf(h *taskHandle) *types.GoalResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	if h.result != nil {
		cp := *h.result
		return &cp
	}
	return &types.GoalResult{TaskID: h.id, Goal: h.goal, Status: h.status, StartedAt: h.started}
}

func (a *Agent) workspaceOf(req types.GoalRequest) string {
	if v, ok := req.Context["cwd"].(string); ok && v != "" {
		return v
	}
	if a.Cfg != nil && a.Cfg.Workspace != "" {
		return a.Cfg.Workspace
	}
	cwd, _ := os.Getwd()
	return cwd
}

func normalizeMode(m string) string {
	switch m {
	case "plan_first":
		return string(types.ModePlanFirst)
	case "interactive":
		return string(types.ModeInteractive)
	default:
		return string(types.ModeAuto)
	}
}

func planDescriptions(plan types.Plan) []string {
	out := make([]string, 0, len(plan.Steps))
	for _, st := range plan.Steps {
		out = append(out, fmt.Sprintf("%s. %s（%s）", st.ID, st.Describe(), st.Tool))
	}
	return out
}

func planMaxRisk(reg *registry.Registry, plan types.Plan) string {
	risk := "low"
	for _, st := range plan.Steps {
		t, ok := reg.Get(st.Tool)
		if !ok {
			continue
		}
		switch t.Permission() {
		case types.PermissionFullAccess:
			return "high"
		case types.PermissionUserApproved:
			risk = "medium"
		}
	}
	return risk
}

// RunSkill 执行已固化的技能（一键复用）。
func (a *Agent) RunSkill(ctx context.Context, name string, params map[string]string) (map[string]any, error) {
	if a.Skills == nil {
		return nil, fmt.Errorf("技能系统未启用")
	}
	sk, err := a.Skills.Get(name)
	if err != nil {
		return nil, err
	}
	if sk.Disabled {
		// 停用的技能连参数都留着，就是要"先别跑但别忘"。让它跑等于把停掉的东西重新挂上。
		return nil, fmt.Errorf("技能 %q 已停用，请先在技能页启用它", name)
	}
	plan := types.Plan{Goal: "运行技能 " + name}
	for i, st := range sk.Steps {
		args, err := skill.SubstituteParams(st.Args, params)
		if err != nil {
			return nil, fmt.Errorf("技能参数替换失败（步骤 %s）: %w", st.ID, err)
		}
		plan.Steps = append(plan.Steps, types.Step{ID: st.ID, Description: st.Description, Tool: st.Tool, Args: args, DependsOn: st.DependsOn})
		_ = i
	}
	taskID := types.NewID()
	a.Notifier.OnProgress(types.ProgressEvent{TaskID: taskID, Phase: "execute", Message: fmt.Sprintf("运行技能 %s v%d", name, sk.Version), Progress: 10, Kind: "info"})
	executor := &Executor{
		Reg: a.Reg, Gate: a.Gate, Notifier: a.Notifier,
		MaxConcurrency: a.Cfg.Agent.MaxConcurrency,
		StepTimeout:    time.Duration(a.Cfg.Agent.StepTimeoutSecs) * time.Second,
		StepRetries:    a.Cfg.Agent.StepRetries,
		Dedupe:         a.Cfg.Agent.DedupeCalls,
		MaxOutputRunes: a.Cfg.Agent.MaxOutputRunes,
		DataDir:        a.Cfg.DataDir,
		OnUsage:        a.BumpUsage,
	}
	exec := executor.Execute(ctx, plan, taskID, string(types.ModeAuto), false)
	ok := exec.Failed == 0 && exec.Succeeded > 0
	if err := a.Skills.RecordRun(name, ok); err != nil {
		// 统计写不进去 = 面板上的次数与成功率偏少，而没有任何东西会报这个错。必须说出来。
		a.Notifier.OnProgress(types.ProgressEvent{
			TaskID: taskID, Phase: "reflect",
			Message:  fmt.Sprintf("技能 %s 的运行统计未写入（次数与成功率会偏少）: %v", name, err),
			Progress: 95, Kind: "warn",
		})
	}
	// 成长日志：只记跑通的复用。跑坏一次也算 5 分，等级就成了「用了多少次技能」而不是
	// 「攒下多少能用的做法」——那正是这套体系要回答的反面。
	if ok && a.Growth != nil {
		a.Growth.Record(growth.Entry{
			Type: "skill_used", SkillName: name, Goal: "复用技能 " + name,
			Steps: exec.Succeeded,
		})
	}
	if !ok && !exec.Cancelled {
		a.autoOptimizeSkill(ctx, taskID, name, sk, exec)
	}
	// 技能计划目前不带验收标准（上面只填了 Steps），所以这里 hasChecks 恒为 false。
	// 仍按契约传进去：万一以后技能也声明验收标准，这条路径不该悄悄退回"全成功即完成"。
	result := a.buildResult("技能 "+name, exec, heuristicReflection(exec, "", len(plan.Acceptance) > 0), plan.Acceptance, types.ContextBreakdown{})
	result.TaskID = taskID
	result.StartedAt = time.Now()
	result.FinishedAt = time.Now()
	result.Status = statusOfExec(exec)
	if result.Summary == "" {
		result.Summary = fmt.Sprintf("技能 %s 执行完成（成功 %d/%d）", name, exec.Succeeded, exec.Total)
	}
	return map[string]any{
		"task_id": taskID,
		"status":  string(result.Status),
		"score":   result.Score,
		"summary": result.Summary,
		"steps":   result.Steps,
	}, nil
}

// SetNotifier 替换事件出口（服务层装配时调用）。
func (a *Agent) SetNotifier(n Notifier) {
	a.Notifier = n
}

// ApprovalTimeout 安全门控的审批等待时长。
func (a *Agent) ApprovalTimeout() time.Duration {
	if a.Gate == nil {
		return 5 * time.Minute
	}
	return a.Gate.ApprovalTimeoutDuration()
}

// CancelTask 取消运行中的任务。
func (a *Agent) CancelTask(taskID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	h, ok := a.tasks[taskID]
	if !ok || h.cancel == nil {
		return false
	}
	h.cancel()
	return true
}

// TaskStatus 查询任务状态。
func (a *Agent) TaskStatus(taskID string) (types.GoalStatus, *types.GoalResult) {
	a.mu.Lock()
	defer a.mu.Unlock()
	h, ok := a.tasks[taskID]
	if !ok {
		return "", nil
	}
	return h.status, h.result
}

// RunningTasks 运行中任务数。
func (a *Agent) RunningTasks() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, h := range a.tasks {
		if h.status == types.GoalRunning {
			n++
		}
	}
	return n
}

var slugStrip = regexp.MustCompile(`[\s\p{P}]+`)

// skillSlug 从目标生成技能名（避免与现有冲突）。
func skillSlug(goal string, store *skill.Store) string {
	base := slugStrip.ReplaceAllString(strings.TrimSpace(goal), "-")
	base = strings.Trim(base, "-")
	r := []rune(base)
	if len(r) > 24 {
		base = string(r[:24])
	}
	if base == "" {
		base = "skill"
	}
	name := base
	for i := 2; ; i++ {
		if store != nil {
			if _, err := store.Get(name); err != nil {
				break // 不存在，可用
			}
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}

// DataDir 保证数据目录存在。
func DataDir(cfg *config.Config) string {
	_ = os.MkdirAll(filepath.Join(cfg.DataDir, "memory"), 0o755)
	_ = os.MkdirAll(filepath.Join(cfg.DataDir, "skills"), 0o755)
	_ = os.MkdirAll(filepath.Join(cfg.DataDir, "tasks"), 0o755)
	return cfg.DataDir
}
