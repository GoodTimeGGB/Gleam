// Package types 定义 Gleam 各层共享的核心类型。
package types

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// NewID 生成 16 位十六进制随机 ID。
func NewID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// Permission 工具权限分级。
type Permission int

const (
	PermissionReadOnly     Permission = iota // 只读，自动放行
	PermissionUserApproved                   // 中风险：默认需批准，白名单内可放行
	PermissionFullAccess                     // 高风险：始终需要人工批准
)

func (p Permission) String() string {
	switch p {
	case PermissionReadOnly:
		return "readonly"
	case PermissionUserApproved:
		return "user_approved"
	default:
		return "full_access"
	}
}

// Tool 是 Harness 工具层的统一接口，所有内置与 MCP 工具均实现它。
type Tool interface {
	Name() string
	Description() string
	Schema() map[string]any // JSON Schema（供 LLM 理解参数）
	Permission() Permission
	Execute(ctx context.Context, args map[string]any) (any, error)
}

// PathAware 由涉及文件路径的工具实现，供安全门控判断是否落在信任路径内。
type PathAware interface {
	Paths(args map[string]any) []string
}

// StepOutcome 步骤的结果性质（业务层），与 Status（执行层状态机）正交。
//
// 为什么必须把它和 Status 分开：Execute 只返回 (any, error)，err == nil 就等于"成功"。
// 但 shell 命令以非零码退出、file.search 超时，都是"跑完了但没做成"——它们把原因
// 塞进 payload 的 error/note 字段，执行器与反思器都看不见，于是整个任务被判成 success。
// 这是"完成率"这个指标失真的根因：不是统计口径问题，是数据源头就不对。
//
// 结果为空同理：空不是错误（不能因为搜不到就重试），但必须与"有结果"区分，
// 否则模型会在"换个关键词再试—还是空—再换"里空转。
type StepOutcome string

const (
	OutcomeOK     StepOutcome = "ok"     // 正常产出
	OutcomeEmpty  StepOutcome = "empty"  // 跑成功了，但没有结果（不是错误）
	OutcomeFailed StepOutcome = "failed" // 跑完了，但业务上失败
)

// OutcomeReporter 由「Execute 返回 nil error 不代表业务成功」的工具实现。
//
// 执行器在 err == nil 时会再问一次"这次到底做成了没有"；没实现该接口的工具一律按 ok 计，
// 所以这是一个纯增量的可选接口，不影响既有工具。
type OutcomeReporter interface {
	// Outcome 返回本次执行的结果性质；note 非空时会被写入 StepResult.Error，
	// 让反思器与用户看到"跑完了但没做成"的具体原因。
	Outcome(args map[string]any, out any) (StepOutcome, string)
}

// ErrorKind 错误的业务类别。失败率只回答"坏了多少"，归因分布才回答
// "这周该先修哪一层"——参数错要改参数重试，业务拒绝换方案不重试，
// 权限不足重试无用，资源不存在换目标，超时限流退避重试，上游 5xx 有限重试。
// 混在一个失败率里，这五件事长得一模一样。
type ErrorKind string

const (
	ErrParam      ErrorKind = "param"      // 参数错：改参数可重试
	ErrFormat     ErrorKind = "format"     // 格式错：改格式可重试
	ErrBusiness   ErrorKind = "business"   // 业务拒绝：换方案，重试无用
	ErrPermission ErrorKind = "permission" // 权限不足：重试无用
	ErrNotFound   ErrorKind = "not_found"  // 资源不存在：换目标
	ErrTimeout    ErrorKind = "timeout"    // 超时/限流：退避重试
	ErrUpstream   ErrorKind = "upstream"   // 上游 5xx：有限重试
	ErrUnknown    ErrorKind = "unknown"    // 归不了类的，如实记 unknown，不硬塞
)

// ClassifyError 按错误文本归类。执行器里结构已知的错误（审批拒绝、超时、
// 引用替换失败）在各自落点直接定类，不走这里；这里只处理工具带出来的自由文本。
//
// 关键词启发式必然不完美，但"大多数归对 + 少数如实 unknown"远好于全部混在
// 一个失败率里——归因分布的判据是"能不能回答该先修哪层"，不是"每条都对"。
func ClassifyError(msg string) ErrorKind {
	s := strings.ToLower(msg)
	switch {
	case containsAny(s, "timeout", "deadline", "超时", "限流", "rate limit", "too many requests", "429"):
		return ErrTimeout
	case containsAny(s, "5xx", "502", "503", "504", "bad gateway", "service unavailable", "internal server error", "上游"):
		return ErrUpstream
	case containsAny(s, "permission", "denied", "forbidden", "unauthorized", "401", "403", "权限", "拒绝执行", "审批", "未授权"):
		return ErrPermission
	case containsAny(s, "not found", "no such", "不存在", "404", "找不到", "无法找到"):
		return ErrNotFound
	case containsAny(s, "invalid argument", "invalid param", "missing", "缺少参数", "参数", "引用的步骤"):
		return ErrParam
	case containsAny(s, "invalid json", "unmarshal", "解析失败", "格式"):
		return ErrFormat
	case containsAny(s, "退出码", "exit code", "业务", "已拒绝", "rejected"):
		return ErrBusiness
	default:
		return ErrUnknown
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// Step 计划中的单个执行步骤。
type Step struct {
	ID          string         `json:"id"`
	Description string         `json:"description"`
	Tool        string         `json:"tool"`
	Args        map[string]any `json:"args,omitempty"`
	DependsOn   []string       `json:"depends_on,omitempty"`
}

// Plan 规划器输出的结构化计划（支持 DAG 依赖表达并行）。
type Plan struct {
	Goal          string `json:"goal"`
	Steps         []Step `json:"steps"`
	EstimatedTime string `json:"estimated_time,omitempty"`
	// Acceptance 本目标的验收标准：规划时就定好"做到什么算完成"，
	// 反思阶段逐条判定，而不是让模型凭感觉打一个总分（生成者自评永远偏高）。
	Acceptance []string `json:"acceptance,omitempty"`
}

// StepStatus 步骤执行状态。
type StepStatus string

const (
	StepPending   StepStatus = "pending"
	StepRunning   StepStatus = "running"
	StepSucceeded StepStatus = "succeeded"
	StepFailed    StepStatus = "failed"
	StepSkipped   StepStatus = "skipped"
)

// StepResult 步骤执行结果。
type StepResult struct {
	StepID     string         `json:"step_id"`
	Tool       string         `json:"tool"`
	Status     StepStatus     `json:"status"`
	Outcome    StepOutcome    `json:"outcome,omitempty"` // 业务结果性质 ok|empty|failed（见 StepOutcome）
	Output     any            `json:"output,omitempty"`
	Error      string         `json:"error,omitempty"`
	ErrorKind  ErrorKind      `json:"error_kind,omitempty"` // 失败归因类别（见 ErrorKind）；未失败时留空
	FinalArgs  map[string]any `json:"final_args,omitempty"` // 引用替换后的实际参数
	StartedAt  time.Time      `json:"started_at,omitempty"`
	FinishedAt time.Time      `json:"finished_at,omitempty"`
	DurationMS int64          `json:"duration_ms"`
	Deduped    bool           `json:"deduped,omitempty"` // 结果来自去重：复用前序调用或跳过已知失败
	Attempt    int            `json:"attempt,omitempty"` // 第几次尝试出的结果（1 = 一次就对）
	Retried    bool           `json:"retried,omitempty"` // 是否经历自动重试——「一次就对」与「重试三次才对」必须在数据里可区分
	// Carried 本次**没有执行**，直接沿用了上一次运行的结果（`replay --from` 的恢复语义）。
	//
	// 与 Deduped 不是一回事：Deduped 是同一次运行内的去重（这一步跑过，结果被复用），
	// Carried 是这一步**这次根本没跑**。必须显式标注——回放要回答的第一个问题就是
	// 「这一步到底执行了没有」，而两种"没真跑"的原因完全不同：
	// 一个是省调用，一个是省副作用。
	Carried bool `json:"carried,omitempty"`
}

// ContextBreakdown 系统提示词的分段计量（**字节数**，与 b.Len() 同口径）。
//
// 只记总字符数时，「上下文膨胀」类问题无从定位——四成分里大头是"状态"，
// 状态段悄悄涨到六成以上，模型注意力就被淹没在了历史对话里。
// 分段与提示词布局一一对应：指令（身份+规则+输出提醒）/ 能力（菜单+schema）/
// 知识（角色领域提示）/ 状态（工作目录、摘要、记忆、对话、反馈）。
// 用字节而不是字符是因为中文一段顶三倍字节——"占比"这种相对判据与单位无关，
// 但跨段比大小、配阈值时必须知道单位。
type ContextBreakdown struct {
	Instruction int `json:"instruction"` // 指令：身份、规则、协作风格、输出提醒
	Capability  int `json:"capability"`  // 能力声明：能力菜单 + 工具 schema
	Knowledge   int `json:"knowledge"`   // 知识：专家角色的领域提示与产出格式
	State       int `json:"state"`       // 状态：工作目录、压缩摘要、记忆、最近对话、重规划反馈
	Total       int `json:"total"`
}

// StateShare 状态段占比（0-1）。站点判据：> 60% 即上下文膨胀。
func (c ContextBreakdown) StateShare() float64 {
	if c.Total <= 0 {
		return 0
	}
	return float64(c.State) / float64(c.Total)
}

// CheckResult 单条验收标准的判定结果。
type CheckResult struct {
	Criterion string `json:"criterion"`
	Passed    bool   `json:"passed"`
	Note      string `json:"note,omitempty"` // 没通过时说明差在哪，要求具体到能照着改
}

// Reflection 反思器输出的完成度评估（0-100 评分而非二值判断）。
type Reflection struct {
	Score      int           `json:"score"`
	Verdict    string        `json:"verdict"` // done | replan | failed
	Reason     string        `json:"reason"`
	Suggestion string        `json:"suggestion,omitempty"` // 主动提议
	Checks     []CheckResult `json:"checks,omitempty"`     // 逐条验收结果（有验收标准时才有）
}

// ProgressEvent 进度事件，由执行层推送、服务层转发给界面。
type ProgressEvent struct {
	TaskID   string `json:"task_id"`
	Phase    string `json:"phase"` // plan | execute | reflect | done
	Message  string `json:"message"`
	Progress int    `json:"progress"` // 0-100
	Kind     string `json:"kind"`     // info | llm | warn | error
}

// TaskDoneEvent 一次后台任务（定时 / 变化触发）跑完的事件。
//
// 为什么不复用 ProgressEvent：进度是「正在发生」，可以丢——没人在看就算了；
// 完成是「已经发生、而且失败必须被记住」，丢了用户就永远不知道它坏过。
type TaskDoneEvent struct {
	TaskID string     `json:"task_id"`
	Origin string     `json:"origin"` // 触发来源的人读描述，例如 定时任务「每日巡检」
	Result GoalResult `json:"result"`
}

// Succeeded 这次是不是**完整**成功。
//
// 判据只有一份，而且**只有 GoalSuccess 才算**：partial（部分完成）与 cancelled
// 都要通知。看起来可以宽松一点（分数高就算成），但那正是"静默掉最该知道的那次"——
// 一个每天跑、每次都只做一半的任务，用户最需要被告知。
func (e TaskDoneEvent) Succeeded() bool { return e.Result.Status == GoalSuccess }

// StateText 这次结束的人读状态词。三个宿主（控制台 / 事件推送 / 系统通知）共用一份，
// 别在各处各写一遍 switch——那种分叉最终表现为"某个入口的说法和别处不一样"。
func (e TaskDoneEvent) StateText() string {
	switch e.Result.Status {
	case GoalSuccess:
		return "完成"
	case GoalPartial:
		return "部分完成"
	case GoalCancelled:
		return "已取消"
	default:
		return "失败"
	}
}

// Title 通知标题。
func (e TaskDoneEvent) Title() string {
	origin := strings.TrimSpace(e.Origin)
	if origin == "" {
		origin = "后台任务"
	}
	return origin + e.StateText()
}

// Line 一行摘要（控制台 / 事件推送 / 通知正文共用）。
func (e TaskDoneEvent) Line() string {
	parts := []string{e.StateText() + "：" + Shorten(strings.TrimSpace(e.Result.Goal), 60)}
	if e.Result.Status == GoalSuccess || e.Result.Status == GoalPartial {
		parts = append(parts, fmt.Sprintf("完成度 %d", e.Result.Score))
	}
	if detail := strings.TrimSpace(e.Result.Error); detail != "" {
		parts = append(parts, Shorten(detail, 120))
	} else if e.Result.Status != GoalSuccess {
		if s := strings.TrimSpace(e.Result.Summary); s != "" {
			parts = append(parts, Shorten(s, 120))
		}
	}
	return strings.Join(parts, " · ")
}

// ApprovalRequest 高风险操作的审批请求。
type ApprovalRequest struct {
	TaskID    string   `json:"task_id"`
	Plan      []string `json:"plan"` // 将要执行的操作描述列表
	Risk      string   `json:"risk"` // low | medium | high
	Reason    string   `json:"reason,omitempty"`
	StepID    string   `json:"step_id,omitempty"`
	WholePlan bool     `json:"whole_plan"` // plan_first 模式的整计划审批
}

// ApprovalResponse 审批结果。
type ApprovalResponse struct {
	Approved bool   `json:"approved"`
	Note     string `json:"note,omitempty"`
}

// GoalMode 目标执行模式（安全策略，与任务模式正交）。
type GoalMode string

const (
	ModeAuto        GoalMode = "auto"        // 仅高风险操作需批准
	ModePlanFirst   GoalMode = "plan_first"  // 执行前展示完整计划请求批准
	ModeInteractive GoalMode = "interactive" // 每个非只读步骤均需批准
)

// TaskMode 任务模式（决定引擎走哪条管线）。
type TaskMode string

const (
	TaskChat TaskMode = "chat" // 对话：直连 LLM 快速问答，不经规划/执行
	TaskWork TaskMode = "work" // 工作：Plan-Execute-Reflect 全流程（默认）
	TaskCode TaskMode = "code" // 编程：工作流程 + 编程准则（最小 diff、验证步骤）
)

// GoalRequest 目标模式请求。
type Reference struct {
	Kind  string `json:"kind"`  // file | goal | skill | plugin | memory
	Label string `json:"label"` // 面向用户的短名称
	Value string `json:"value"` // 实际引用标识，不等同于显示文本
}

const MaxReferences = 20

// ValidateReferences 校验所有外部入口共享的目标引用契约。
func ValidateReferences(refs []Reference) error {
	if len(refs) > MaxReferences {
		return fmt.Errorf("引用数量不能超过 %d", MaxReferences)
	}
	allowed := map[string]bool{"file": true, "goal": true, "skill": true, "plugin": true, "memory": true}
	for i, ref := range refs {
		if !allowed[ref.Kind] {
			return fmt.Errorf("第 %d 个引用类型 %q 不受支持", i+1, ref.Kind)
		}
		label := strings.TrimSpace(ref.Label)
		value := strings.TrimSpace(ref.Value)
		if label == "" || value == "" {
			return fmt.Errorf("第 %d 个引用的 label 和 value 不能为空", i+1)
		}
		if len([]rune(label)) > 200 || len([]rune(value)) > 2048 {
			return fmt.Errorf("第 %d 个引用过长", i+1)
		}
	}
	return nil
}

type GoalRequest struct {
	TaskID     string         `json:"task_id,omitempty"` // 服务端可预分配
	Goal       string         `json:"goal"`
	Context    map[string]any `json:"context,omitempty"`
	References []Reference    `json:"references,omitempty"`
	Mode       string         `json:"mode"`                // 安全模式 auto | plan_first | interactive
	TaskMode   TaskMode       `json:"task_mode,omitempty"` // 任务模式 chat | work | code（空视为 work）
	Role       string         `json:"role,omitempty"`      // 专家角色 ID（general/analyst/writer/coder/pm/researcher/ops）

	ConversationID string `json:"conversation_id,omitempty"` // 所属会话 ID（左侧会话列表；空表示不持久化到会话）
}

// GoalStatus 目标任务状态。
type GoalStatus string

const (
	GoalRunning   GoalStatus = "running"
	GoalSuccess   GoalStatus = "success"
	GoalPartial   GoalStatus = "partial" // 部分完成（完成度评分中等）
	GoalFailed    GoalStatus = "failed"
	GoalCancelled GoalStatus = "cancelled"
)

// TaskUsage 一次任务的资源消耗（消耗看板）。
// 用于回答"这次任务花了多少"：模型调用次数、token、工具调用、重试与耗时。
type TaskUsage struct {
	LLMCalls         int   `json:"llm_calls"`         // 模型调用次数
	PromptTokens     int   `json:"prompt_tokens"`     // 输入 token（厂商未返回时为估算）
	CompletionTokens int   `json:"completion_tokens"` // 输出 token
	EstimatedCalls   int   `json:"estimated_calls"`   // 其中为估算值的调用次数
	CachedTokens     int   `json:"cached_tokens"`     // 输入 token 中命中服务端提示词缓存的部分
	CachedCalls      int   `json:"cached_calls"`      // 其中报告了缓存命中的调用次数
	ToolCalls        int   `json:"tool_calls"`        // 工具执行次数（含超时重试）
	Retries          int   `json:"retries"`           // 步骤重试次数
	Deduped          int   `json:"deduped"`           // 被去重省下的调用次数（复用结果或跳过已知失败）
	DurationMs       int64 `json:"duration_ms"`       // 任务耗时
}

// CacheHitRate 输入侧缓存命中率（0-1）：命中 token / 输入 token。
// 它衡量的是"提示词布局有多缓存友好"，而不是"省了多少钱"——
// 厂商不给缓存字段时恒为 0，因此 0 只能说明"没观察到命中"，不能断定没有缓存。
func (u TaskUsage) CacheHitRate() float64 {
	if u.PromptTokens <= 0 || u.CachedTokens <= 0 {
		return 0
	}
	if u.CachedTokens > u.PromptTokens {
		return 1
	}
	return float64(u.CachedTokens) / float64(u.PromptTokens)
}

// TotalTokens 本次任务的总 token。
func (u TaskUsage) TotalTokens() int { return u.PromptTokens + u.CompletionTokens }

// ConfigSnapshot 任务**启动时**生效的关键配置。
//
// 为什么要记：一个跑几分钟的任务，中途用户在设置页改了模型或档位，前半段和后半段
// 用的就不是同一套配置了——而结果里只有一个事后算出的规则指纹，**无法区分
// "是配置变了"还是"模型发挥不稳"**。快照把"当时用的是什么"变成可核对的事实，
// 也是 `replay --rerun` 能真正复现的前提（拿当前配置重跑，比出来的差异分不清是哪来的）。
//
// 刻意**只记影响行为的关键项**，不记全量配置，两个理由：
//   - 全量快照会把 API Key 一起写进 tasks/<id>.json，而那是明文落盘；
//   - 改一个无关字段也算"配置变了"，反而看不出真正影响结果的那几项。
//
// 路径类字段（工作区、数据目录）同样不记——它们属于"这次在哪跑"，不属于"用什么跑"。
type ConfigSnapshot struct {
	Provider  string            `json:"provider,omitempty"`
	Model     string            `json:"model,omitempty"`
	FastModel string            `json:"fast_model,omitempty"`
	Tiers     map[string]string `json:"tiers,omitempty"` // 档位名 → 模型 ID
	// SafetyMode 决定"同一个动作会不会被拦下来"，改了它同一条计划能跑出不同结果。
	SafetyMode string `json:"safety_mode,omitempty"`

	// 决定「算不算做完」与「什么时候停」
	DoneThreshold int `json:"done_threshold,omitempty"`
	MaxReplans    int `json:"max_replans,omitempty"`
	MaxSteps      int `json:"max_steps,omitempty"`
	// 决定「单步怎么跑」（replay --rerun 靠这几个复现）
	StepTimeoutSecs int  `json:"step_timeout_secs,omitempty"`
	StepRetries     int  `json:"step_retries,omitempty"`
	MaxConcurrency  int  `json:"max_concurrency,omitempty"`
	DedupeCalls     bool `json:"dedupe_calls,omitempty"`
	MaxOutputRunes  int  `json:"max_output_runes,omitempty"`
	// 决定「上下文里放什么」
	MaxToolSchemas  int  `json:"max_tool_schemas,omitempty"`
	ReflectEachStep bool `json:"reflect_each_step,omitempty"`
}

// GoalResult 目标执行结果。
type GoalResult struct {
	TaskID     string       `json:"task_id"`
	Goal       string       `json:"goal"`
	Status     GoalStatus   `json:"status"`
	Score      int          `json:"score"`
	Summary    string       `json:"summary"`
	Error      string       `json:"error,omitempty"`
	Suggestion string       `json:"suggestion,omitempty"`
	Steps      []StepResult `json:"steps,omitempty"`
	Usage      TaskUsage    `json:"usage"`
	StartedAt  time.Time    `json:"started_at"`
	FinishedAt time.Time    `json:"finished_at"`
	// Acceptance / Checks：做了什么承诺、逐条验得到哪一步——让"完成"是可核对的，不是一个分数
	Acceptance []string      `json:"acceptance,omitempty"`
	Checks     []CheckResult `json:"checks,omitempty"`
	// RuleSet / Rules：这次产出用的是哪版规则集。
	//
	// 改了提示词规则之后，第一个要回答的问题是"之前那批结果是用哪一版跑出来的"——
	// 回答不了，归因就只能靠猜，而猜出来的结论会让人对评测失去信心。
	//   - RuleSet 规则表的版本指纹（内容派生，改一字就变，见 agent.RuleSetVersion）；
	//   - Rules   本次实际生效的规则 ID（按提示词里的出现顺序）。
	// 两者互补：指纹回答"是不是同一份"，ID 列表回答"用了哪几条"。
	// 对话模式不经规划器、没有规则参与，两个字段如实留空——留空也是一种回答。
	RuleSet string   `json:"rule_set,omitempty"`
	Rules   []string `json:"rules,omitempty"`
	// ConfigSnapshot 任务**启动时**的配置快照（见 ConfigSnapshot 的说明）。
	// 它与 RuleSet 是同一类东西的两个半边：规则集回答"提示词是哪一版"，
	// 快照回答"模型与阈值是哪一套"——缺任何一边，历史结果都不可比。
	ConfigSnapshot *ConfigSnapshot `json:"config_snapshot,omitempty"`
	// TraceID 由「目标内容 + 任务模式 + 角色 + 模型」内容派生的短哈希。
	// 刻意不含时间戳：同一个输入配同一个模型重复出现时，trace_id 相同——
	// 失败聚类因此不需要额外的索引，按 ID 分组就能看到"同一个问题反复出现"。
	TraceID string `json:"trace_id,omitempty"`
	// ExecutedPlan 参数替换后的实际执行计划。GoalResult 会落盘为 tasks/<id>.json，
	// 有了它，"当时到底打算怎么做"就能离线回放（gleam replay），
	// 而不是只留下一个执行结果让复盘的人猜计划长什么样。
	ExecutedPlan *Plan `json:"executed_plan,omitempty"`
	// PromptBreakdown 系统提示词分段计量（见 ContextBreakdown）。
	// 只记总数时上下文膨胀无从定位；分段才能回答"是哪一段在涨"。
	PromptBreakdown *ContextBreakdown `json:"prompt_breakdown,omitempty"`
	// FailureBreakdown 失败归因分布：ErrorKind -> 步骤数。
	// 失败率只回答"坏了多少"，这份分布回答"该先修哪一层"。
	FailureBreakdown map[ErrorKind]int `json:"failure_breakdown,omitempty"`
	// FirstPass / Reworks 交付侧的一次性口径：产物是不是**一次就合格**。
	//
	// 与 StepResult.Retried 的区别在层级：那个是**步骤级**的（这一步重试了两次），
	// 回答不了"整个目标返工了几轮"。而重试三次才凑出来的结果与一次做对的结果，
	// 在成功率上完全一样，在成本与可信度上完全是两回事——成功率这个数字看不见差别。
	//
	//   - FirstPass 是否**第一轮尝试**就走完流程并通过完成判定（验收 + 产物核对）。
	//     它不等于"成功"：返工一次才成功同样是成功，但不是一次就合格。
	//     口径含"计划第一次就可用"——规划校验失败导致的重规划不算返工（没做过的事
	//     谈不上重做），但它确实让这次尝试没走完，所以 FirstPass 会落成 false。
	//   - Reworks 反思判定未达标后**真正重新执行**的轮数（规划校验失败的重规划不计）。
	//
	// 对话模式不经规划/反思，两个字段保持零值——留空也是一种回答，
	// 不要把它读成"没通过"（成长统计里另按对话自己的核对结论记，见 runChatPath）。
	FirstPass bool `json:"first_pass,omitempty"`
	Reworks   int  `json:"reworks,omitempty"`
}

// ReplayKind 回放方式。
type ReplayKind string

const (
	ReplayRerun  ReplayKind = "rerun"  // 原样重跑（--rerun）
	ReplayResume ReplayKind = "resume" // 从某一步起继续（--from），之前的步骤沿用当时结果
	ReplayFork   ReplayKind = "fork"   // 从某一步起换一种走法（--from + --tool/--args）
)

// ReplayRecord 一次回放（重跑 / 恢复 / 分叉）的落盘记录。
//
// **回放不是任务**，所以它不住 `tasks/`：那条目录是任务列表与质量统计（完成率、
// 用户重试率、首次通过率）的分母来源，把回放混进去，指标就会被自己的复盘动作污染——
// 用户重放一次历史任务，统计里就多一个"用户提交过的任务"。
//
// 为什么要落盘而不是只打印：审计的四种用法里，「并行比较」要比较的就是两条**已存在**
// 的记录。不落盘，每次重跑的结果看完就没了，两次重跑之间的差异也就无从谈起——
// 而"同一配置重跑两次差异多大"正是可重复性的直接证据。
type ReplayRecord struct {
	ReplayID string     `json:"replay_id"`
	Kind     ReplayKind `json:"kind"`
	// SourceTaskID 被回放的那条任务。
	SourceTaskID string `json:"source_task_id"`
	// FromStep 从哪一步开始重跑；之前的步骤沿用 SourceTask 当时的结果（Kind=rerun 时为空）。
	FromStep string `json:"from_step,omitempty"`
	// OverrideTool / OverrideArgs 分叉时替换掉 FromStep 那一步的工具或参数。
	// 只记被改掉的那一项——原样保留其余部分，读的人才知道"变的是哪一个变量"。
	OverrideTool string         `json:"override_tool,omitempty"`
	OverrideArgs map[string]any `json:"override_args,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	// Steps 本次真正产出的步骤结果。Carried 为真的那几步是沿用的，不是这次跑的。
	Steps []StepResult `json:"steps"`
	// 计数与 GoalResult 的口径一致：Outcome=failed 的步骤计入 Failed，不因为
	// Status=succeeded 就算成功（否则"跑完了但没做成"会被回放报告洗白）。
	Succeeded  int   `json:"succeeded"`
	Failed     int   `json:"failed"`
	Skipped    int   `json:"skipped"`
	Empty      int   `json:"empty"`
	Carried    int   `json:"carried"`
	DurationMs int64 `json:"duration_ms"`
	// ConfigSnapshot 本次回放用的配置（语义同 GoalResult.ConfigSnapshot）。
	// 与任务快照并排存，差异来源才解释得清：是环境变了，还是两次回放用了不同配置。
	ConfigSnapshot *ConfigSnapshot `json:"config_snapshot,omitempty"`
}

// Suggestion 主动提议（人格化协作层）。
type Suggestion struct {
	TaskID string      `json:"task_id,omitempty"`
	Type   string      `json:"type"` // suggestion | suggest_skill
	Text   string      `json:"text,omitempty"`
	Skill  *SkillDraft `json:"skill,omitempty"`
}

// RetryOK 经历自动重试后才成功的步骤数。
// "一次就对"与"重试三次才对"在通过率里长得一样——后者是隐患位置，
// 这个数让它至少在汇总层面可见。
func (g *GoalResult) RetryOK() int {
	n := 0
	for _, st := range g.Steps {
		if st.Retried && st.Status == StepSucceeded {
			n++
		}
	}
	return n
}

// SkillDraft 技能固化建议。
type SkillDraft struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Steps       []Step `json:"steps"`
}

// MemoryHitView 记忆检索结果的传输视图。
type MemoryHitView struct {
	ID      string   `json:"id"`
	Content string   `json:"content"`
	Tags    []string `json:"tags,omitempty"`
	Score   float32  `json:"score"`
}

// ScheduleJobView 定时任务的传输视图。
type ScheduleJobView struct {
	Name         string `json:"name"`
	Cron         string `json:"cron,omitempty"`
	IntervalSec  int    `json:"interval_sec,omitempty"`
	Goal         string `json:"goal"`
	Mode         string `json:"mode,omitempty"`
	Notify       string `json:"notify,omitempty"` // 通知策略（已归一化：always / on_failure / never）
	Enabled      bool   `json:"enabled"`
	WhenText     string `json:"when_text,omitempty"`
	ScheduleText string `json:"schedule_text,omitempty"`
	NextRun      string `json:"next_run,omitempty"`
	LastRun      string `json:"last_run,omitempty"`
}

// ErrStepRejected 用户拒绝审批。
var ErrStepRejected = fmt.Errorf("用户拒绝执行该操作")

// Describe 返回步骤的人读描述（用于审批卡片与进度推送）。
func (s Step) Describe() string {
	if s.Description != "" {
		return s.Description
	}
	return fmt.Sprintf("调用 %s", s.Tool)
}

// Shorten 按 rune 截断字符串。
func Shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// EstimateTokens 粗略估算文本 token 数：CJK 字符约 1 字 1 token，其余约 4 字符 1 token。
// 仅用于界面展示与压缩收益统计（"估算"），不用于计费。
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	cjk, other := 0, 0
	for _, r := range s {
		switch {
		case r >= 0x2E80 && r <= 0x9FFF || r >= 0xF900 && r <= 0xFAFF || r >= 0xFF00 && r <= 0xFFEF:
			cjk++
		default:
			other++
		}
	}
	return cjk + (other+3)/4
}
