// Package eval 提示词与 Agent 行为的回归评测。
//
// 为什么需要它：提示词的稳定段被服务端前缀缓存折扣过，所以"给提示词做减法"的收益
// 并不在省钱，而在减少注意力稀释与指令冲突——这一点**没法用"省了多少字节"论证，
// 只能靠评测回答"删了会不会变差"**。没有评测就删规则，是赌博不是工程。
//
// 三条设计约束：
//
//  1. **不依赖 LLM 裁判。** 绝大多数要守的是结构性契约——工具路由、步数、
//     有没有给验收标准。这些都能确定性断言。让模型来打分会让评测本身不可复现，
//     也就失去了"回归"的意义。
//  2. **离线可跑。** select 深度只跑工具筛选（本地关键词打分，零模型调用），
//     因此能进 CI。plan / full 深度需要模型；Mock 下只能验证管道通畅，
//     验证不了质量——所以报告里如实标注本次用的模型是什么。
//  3. **结论指向动作。** 每条用例列出逐条断言与实测值，失败项直接说明差在哪。
package eval

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"gleam/pkg/types"
)

// Depth 评测深度：跑得越深越接近真实，但越依赖模型。
type Depth string

const (
	// DepthSelect 只跑工具筛选：纯本地、确定性、零模型调用，可离线进 CI。
	//
	// 它断言的是「本地关键词打分认不认某个工具与目标相关」，不是「最终菜单里有没有它」——
	// 菜单末尾有按注册顺序补齐的兜底，永远填满，拿它做断言会得到虚假的安心
	// （说明改短到丢了关键词，工具仍可能被兜底塞回菜单，评测却全绿）。
	// 这条路径正是提示词与工具描述改动最容易悄悄弄坏、又最难靠人工发现的地方。
	DepthSelect Depth = "select"
	// DepthPlan 跑到计划：断言计划里的工具与结构。需要模型。
	DepthPlan Depth = "plan"
	// DepthFull 跑完整目标（规划 + 执行 + 反思）。需要模型，且会真的动工具。
	DepthFull Depth = "full"
)

// ValidDepth 判断深度取值是否合法。
func ValidDepth(d Depth) bool {
	switch d {
	case DepthSelect, DepthPlan, DepthFull:
		return true
	}
	return false
}

// Layer 评测分层（H2/H3/H13）：用途不同、跑的频率与门槛不同。
//
//	冒烟层零模型调用、确定性，PR 每次都跑；回归层是行为契约；保留池**不看单条结果**。
type Layer string

const (
	// LayerSmoke 冒烟层：纯 select 可判的工具选择断言，零模型调用、确定性、可进 CI。
	LayerSmoke Layer = "smoke"
	// LayerRegression 回归层：行为契约（步数、验收标准），plan/full 深度跑。
	LayerRegression Layer = "regression"
	// LayerEdge 边界层：超长输入、空输入、极端参数。
	LayerEdge Layer = "edge"
	// LayerAdversarial 对抗层：注入、诱导、越权类用例。
	LayerAdversarial Layer = "adversarial"
	// LayerHoldout 保留池：约 20% 的用例**不参与日常调参观察**——
	// 调试视图只显示聚合，不显示单条结果。存在意义：对全部样本反复调参会过拟合，
	// 保留池是"没被盯着的样本"，只有它绿才说明改动真泛化了。
	LayerHoldout Layer = "holdout"
)

// ValidLayer 判断分层取值是否合法（空 = regression 默认层，合法）。
func ValidLayer(l Layer) bool {
	switch l {
	case "", LayerSmoke, LayerRegression, LayerEdge, LayerAdversarial, LayerHoldout:
		return true
	}
	return false
}

// LayerOf 返回用例的有效分层（空值回落 regression）。
func LayerOf(c Case) Layer {
	if c.Layer == "" {
		return LayerRegression
	}
	return Layer(c.Layer)
}

// Case 一条评测用例：一个目标 + 一组期望。
//
// 期望全部是可判定的结构，没有"感觉不错"这类主观项。
type Case struct {
	ID       string `json:"id"`
	Goal     string `json:"goal"`
	Role     string `json:"role,omitempty"`
	TaskMode string `json:"task_mode,omitempty"`
	CWD      string `json:"cwd,omitempty"`
	Note     string `json:"note,omitempty"` // 这条用例在守什么，给人看的

	// WantTools 必须出现的工具（select 深度=相关度进前 N；plan/full=计划里的）。
	WantTools []string `json:"want_tools,omitempty"`
	// NotWantTools 不该出现的工具——守误路由。比"该选到什么"更容易出问题：
	// 少选一个工具通常会让计划失败，而多选一个会静默地做错事。
	NotWantTools []string `json:"not_want_tools,omitempty"`
	// TopN select 深度的名次窗口，默认 DefaultTopN。
	// 只判前 N 名而不是"在不在相关集里"，因为本地关键词打分是兜底路径，
	// 它的长尾全是噪音：一个工具排在 21 个里的第 11 名，对行为毫无影响
	// （模型只会拿到前若干个的 schema），拿长尾判"误路由"只会得到永远红的评测。
	TopN int `json:"top_n,omitempty"`
	// MinSteps / MaxSteps 计划步数区间（plan/full 深度）。MaxSteps 守"别把简单事拆成十步"。
	MinSteps int `json:"min_steps,omitempty"`
	MaxSteps int `json:"max_steps,omitempty"`
	// WantAcceptance 是否要求给出验收标准（plan/full 深度）。
	WantAcceptance bool `json:"want_acceptance,omitempty"`
	// WantStatus 可接受的目标状态（full 深度），如 ["success","partial"]。
	WantStatus []string `json:"want_status,omitempty"`

	// KnownIssue 已知问题：这条用例当前**预期不过**。
	//
	// 评测照常跑、照常打印失败详情，但把它归入「已知问题」而不是「回归」，不参与门禁。
	// 存在的意义是把"已经查清、等排期修"的问题钉在用例集里——直接删掉这条用例
	// 会让它悄悄回来，而留在门禁里会让整个评测永远红、最后没人看。
	// 修好之后它会以「已修复」出现，提醒你摘掉这个标记。
	KnownIssue bool `json:"known_issue,omitempty"`
	// Unsolvable 不可解用例：这个目标**本来就做不到**，期望是被识别并如实报告，
	// 而不是硬猜一个看似完成的结果。
	//
	// 不可解处理率必须独立成指标，否则指标会激励"硬猜"——把猜对也算通过，
	// 模型学到的是"装作做完比承认做不到更划算"。只在 full 深度生效：
	// select/plan 深度根本不产生"完成与否"的结论，判不了这件事。
	// 判据刻意宽松：状态不是 success 即算识别（如实失败、如实部分完成都算），
	// 因为"怎么拒绝"的形态可以多样，"硬猜成功"的形态只有一种。
	Unsolvable bool `json:"unsolvable,omitempty"`

	// Layer 评测分层（smoke/regression/edge/adversarial/holdout），空 = regression。
	// holdout 是保留池：照跑、计入聚合，但调试视图不显示单条结果——
	// 没被盯着调的样本才有泛化证明力。
	Layer string `json:"layer,omitempty"`
}

// Check 单条断言的判定。
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// observation 执行器观测到的原始事实，交给 judge 判定。
// 与 Case 分开，是为了让"判定逻辑"能脱离真实执行单独测。
type observation struct {
	Tools      []string
	Steps      int
	Acceptance int
	Status     string
	Error      string
}

// CaseUsage 一条用例（或一整批）的资源消耗，**只取报告需要的字段**。
//
// 为什么不直接复用 types.TaskUsage：那个类型带 DurationMs，而 CaseResult 已经有
// DurationMS（这一遍的墙钟耗时）。同一个报告里出现两个名字几乎一样、口径不同的耗时，
// 读的人一定会拿错——而"成本"这件事最怕的就是口径含糊。耗时由 CaseResult.DurationMS
// 负责，这里只回答"花了多少 token、调了多少次模型"。
//
// 为什么不直接复用 GoalResult.Usage 的类型：见上；转换点收敛在 usageFromTask 一处，
// TaskUsage 将来加了字段，要不要进报告必须在这里显式决定一次，不会静默漂移。
type CaseUsage struct {
	LLMCalls         int `json:"llm_calls,omitempty"`
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	// EstimatedCalls 其中 token 为估算值（厂商未返回用量）的调用次数。
	// 不说这个数，一行 token 合计看起来像实测值——而它可能大半是估的。
	EstimatedCalls int `json:"estimated_calls,omitempty"`
	// CachedTokens / CachedCalls 命中服务端提示词缓存的部分与报告了命中的调用次数。
	// 评测的存在理由之一就是量测提示词布局对前缀缓存友不友好（§4.6.6），
	// 而 PromptChars 只回答"提示词多长"，回答不了"它被缓存命中了没有"。
	CachedTokens int `json:"cached_tokens,omitempty"`
	CachedCalls  int `json:"cached_calls,omitempty"`
	ToolCalls    int `json:"tool_calls,omitempty"`
}

// TotalTokens 输入 + 输出。
func (u CaseUsage) TotalTokens() int { return u.PromptTokens + u.CompletionTokens }

// Empty 是否一个数都没有。用来区分"计量了但确实是 0"与"没计量"。
func (u CaseUsage) Empty() bool {
	return u.LLMCalls == 0 && u.PromptTokens == 0 && u.CompletionTokens == 0 &&
		u.EstimatedCalls == 0 && u.CachedTokens == 0 && u.CachedCalls == 0 && u.ToolCalls == 0
}

// add 累加一条用例的用量（报告级合计用）。
func (u *CaseUsage) add(o CaseUsage) {
	u.LLMCalls += o.LLMCalls
	u.PromptTokens += o.PromptTokens
	u.CompletionTokens += o.CompletionTokens
	u.EstimatedCalls += o.EstimatedCalls
	u.CachedTokens += o.CachedTokens
	u.CachedCalls += o.CachedCalls
	u.ToolCalls += o.ToolCalls
}

// usageFromTask 把任务级用量转成报告用的形状。唯一的转换点。
func usageFromTask(t types.TaskUsage) *CaseUsage {
	return &CaseUsage{
		LLMCalls:         t.LLMCalls,
		PromptTokens:     t.PromptTokens,
		CompletionTokens: t.CompletionTokens,
		EstimatedCalls:   t.EstimatedCalls,
		CachedTokens:     t.CachedTokens,
		CachedCalls:      t.CachedCalls,
		ToolCalls:        t.ToolCalls,
	}
}

// CaseResult 一条用例的实际观测与判定。
type CaseResult struct {
	ID         string   `json:"id"`
	Goal       string   `json:"goal"`
	Passed     bool     `json:"passed"`
	Checks     []Check  `json:"checks"`
	Tools      []string `json:"tools,omitempty"`
	Steps      int      `json:"steps,omitempty"`
	Acceptance int      `json:"acceptance,omitempty"`
	Status     string   `json:"status,omitempty"` // full 深度
	Score      int      `json:"score,omitempty"`  // full 深度
	// Note 从用例带过来的"这条在守什么"。失败时随报告一起打印——
	// 红的用例必须能自己解释自己，否则读报告的人还得回去翻用例文件。
	Note string `json:"note,omitempty"`
	// Known 这条用例是已知问题（用例里标了 known_issue）且本次确实没过。
	Known bool `json:"known,omitempty"`
	// Holdout 这条属于保留池：计入聚合，但调试视图不显示单条结果。
	Holdout bool `json:"holdout,omitempty"`
	// PromptChars 本次规划用的系统提示词字符数。评测要能回答"这刀减法省了多少、
	// 有没有换来质量变化"，所以提示词成本与通过率必须记在同一行。
	PromptChars int    `json:"prompt_chars,omitempty"`
	DurationMS  int64  `json:"duration_ms"`
	Error       string `json:"error,omitempty"`
	// Rules 这条用例场景下**实际生效**的规则 ID，按提示词里的出现顺序。
	// 用例之间会不同（只有 code 模式的用例才带 code.* 规则），
	// 它解释了"为什么这条用例的提示词比别条长/短"，也是回归时"规则有没有变"的证据。
	Rules []string `json:"rules,omitempty"`
	// FailureKinds 本次执行暴露的失败归因分布（ErrorKind -> 步骤数，full 深度）。
	// 一条用例绿不代表过程没有失败——失败的步骤可能被重规划救回来了，
	// 归因分布让这些"过程失败"也留在数据里。
	FailureKinds map[string]int `json:"failure_kinds,omitempty"`
	// RetryOK 经历自动重试后才成功的步骤数（full 深度）。
	// "一次就对"与"重试三次才对"在通过率里长得一样，这里把它们分开。
	RetryOK int `json:"retry_ok,omitempty"`
	// Runs 这条用例实际重跑的遍数（--repeat N，默认 1）。
	Runs int `json:"runs,omitempty"`
	// Agreed 与"最常见的那种结果"一致的遍数。Runs==1 时恒等于 1。
	Agreed int `json:"agreed,omitempty"`
	// Stable 各遍结果是否完全一致。**这一列回答的是"稳不稳"，与 Passed 回答的
	// "对不对"是两个数，不能互相替代**：一条 Stable=false 的用例，它这次的绿
	// 可能只是掷硬币掷出来的，不该拿来当改动有效的证据。
	Stable bool `json:"stable,omitempty"`
	// Variants 不稳定用例的差异明细（每种结果各出现几次）。
	// 只在 Stable=false 时填：它回答"抖的是哪一部分"——通过与否、步数、还是用到的工具。
	// 不给出这个，读报告的人只能自己再手工重跑几遍，那就白跑了。
	Variants []string `json:"variants,omitempty"`
	// Usage 这条用例的资源消耗。**只有 full 深度有值**（其余深度为 nil）。
	//
	// 用指针而不是零值：nil 表示"没计量"，零值结构表示"计量了但确实是 0"。
	// 两者在报告里必须能分开——一个 select 深度的 0 与一次"模型没被调用"的 0
	// 长得一样的话，读的人会以为这批评测没花钱。
	Usage *CaseUsage `json:"usage,omitempty"`
}

// Diff 与基线的对比。
type Diff struct {
	BaselineAt time.Time `json:"baseline_at"`
	// Fixed 基线红、现在绿——说明改动确实修好了东西。
	Fixed []string `json:"fixed,omitempty"`
	// Broke 基线绿、现在红——**回归**，决定退出码。
	Broke []string `json:"broke,omitempty"`
	// Added / Removed 用例集本身的变化。不参与回归判定，但要报出来：
	// 用例被删掉会让通过率变好看，这是评测最容易被绕过的地方。
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	// PromptDelta 提示词字符数合计的变化（正=变长）。
	PromptDelta int `json:"prompt_delta"`
	// RuleSetFrom / RuleSetTo 规则集版本指纹的变化。**只报不卡门禁**——
	// 改规则是正常动作，不该让门禁变红；但回归报告里必须看得见：
	// 有回归时第一条该怀疑的是"规则改了"，而不是"代码改坏了"。
	RuleSetFrom string `json:"rule_set_from,omitempty"`
	RuleSetTo   string `json:"rule_set_to,omitempty"`
}

// Report 一次评测的总报告。
type Report struct {
	GeneratedAt time.Time `json:"generated_at"`
	Depth       Depth     `json:"depth"`
	Model       string    `json:"model"` // mock / 真实模型名
	// LocalSelect select 深度是否走了本地确定性打分（Helper 置空）。
	// 如实记下来：配了辅助模型的生产环境筛选是走模型的，两者不等价。
	LocalSelect bool `json:"local_select"`
	// RelevantOnly select 深度的 tools 是"打分相关集"而非"最终菜单"（未补齐）。
	// 两者不是一回事，报告里必须说清楚，否则看到工具数只有几个会以为筛选坏了。
	RelevantOnly bool `json:"relevant_only,omitempty"`
	// Tier 本次评测**模拟**的生效模型档位（空 = 按真实配置，未模拟）。
	// 它只影响提示词构建，用来量测按档位条件化的规则到底改了多少字。
	Tier string `json:"tier,omitempty"`
	// RuleSet 本次评测所用规则集的版本指纹（由规则表内容派生，改一字就变）。
	// 基线把它一起存下来，才能回答"这份 16/16 是哪一版规则跑出来的"。
	RuleSet string `json:"rule_set,omitempty"`
	Total   int    `json:"total"`
	Passed  int    `json:"passed"`
	Failed  int    `json:"failed"`
	// Known 已知问题里本次仍未过的条数（不计入 Failed，不参与门禁）。
	Known int `json:"known,omitempty"`
	// Resolved 标了 known_issue 但本次通过了的用例——说明根因已修，
	// 该把标记摘掉了。不报出来就会一直挂着一个过时的"已知问题"。
	Resolved []string `json:"resolved,omitempty"`
	// Unsolvable 不可解用例总数；Handled 其中被正确识别（没有硬猜成功）的条数。
	// 处理率 = Handled / Unsolvable，**独立于通过率**——混进通过率会激励硬猜。
	Unsolvable        int `json:"unsolvable,omitempty"`
	UnsolvableHandled int `json:"unsolvable_handled,omitempty"`
	// Attempted 通过率的分母 = Total - Unsolvable。
	// 分母陷阱：全样本口径与"可解样本"口径能差出二十个百分点，
	// 报通过率时必须把分母说清楚（分子 Passed、分母 Attempted、边界 full 深度）。
	Attempted int `json:"attempted,omitempty"`
	// RetryOK 各用例"重试后才成功"的步骤数合计（full 深度）。
	RetryOK int `json:"retry_ok,omitempty"`
	// 保留池聚合：单条结果不进调试视图，但聚合通过率必须可见——
	// 全部样本都绿而保留池红了，说明改动只过拟合到了看得见的用例。
	HoldoutTotal  int `json:"holdout_total,omitempty"`
	HoldoutPassed int `json:"holdout_passed,omitempty"`
	// FailureBreakdown 全部用例的失败归因分布合计（ErrorKind -> 步骤数，full 深度）。
	// 失败率回答"坏了多少"，这份分布回答"这周该先修哪一层"。
	FailureBreakdown map[string]int `json:"failure_breakdown,omitempty"`
	PromptChars      int            `json:"prompt_chars"`
	// Usage 整批用例的用量合计。**只有 full 深度有值**（其余为 nil，理由见 CaseResult.Usage）。
	//
	// 它补的是 PromptChars 的另一半：PromptChars 是**成本代理**（提示词多长），
	// 用量是**成本本体**（实际花了多少 token）。评测的存在理由之一是"给提示词做减法"，
	// 而减法要算账——只有代理没有本体，账就只能在终端回滚缓冲里看。
	Usage *CaseUsage `json:"usage,omitempty"`
	// RepeatN 每条用例重跑的遍数（1 = 未度量可重复性，报告里不出现一致率）。
	RepeatN int `json:"repeat_n,omitempty"`
	// StableCases / UnstableCases 重跑结果完全一致的用例数 / 不一致的用例 ID。
	StableCases   int      `json:"stable_cases,omitempty"`
	UnstableCases []string `json:"unstable_cases,omitempty"`
	// Repeatability 一致用例数 / 总用例数。**与通过率是两个数，不能互相替代**：
	// 通过率回答"对不对"，可重复性回答"稳不稳"。
	//
	// 为什么必须单独有它：上一轮"拆工具菜单"的减法实验里，同一配置重跑 16 条用例，
	// 有 3.00（菜单×菜单）/ 5.67（全量×全量）条的工具集不同，而处理效应只有 4.83——
	// **噪声比效应还大，结论根本不可归因**。当时没有任何工具能事先看出这件事，
	// 只能靠手工重复采样。一个 100% 通过但可重复性 0.6 的改动，
	// 说明这次的绿还不能当证据用。
	//
	// 用指针是为了区分"没度量"（nil）与"度量了但一致率为 0"——后者是真实结论。
	Repeatability *float64     `json:"repeatability,omitempty"`
	Cases         []CaseResult `json:"cases"`
	Diff          *Diff        `json:"diff,omitempty"`
}

// SummaryLine 一行摘要，给 CLI 与日志用。
func (r Report) SummaryLine() string {
	s := fmt.Sprintf("评测（%s）%d/%d 通过，%d 项未过", r.Depth, r.Passed, r.Total, r.Failed)
	if r.Known > 0 {
		s += fmt.Sprintf("，%d 项已知问题", r.Known)
	}
	if r.Attempted > 0 && r.Attempted != r.Total {
		s += fmt.Sprintf("；通过率分母 %d（不含 %d 条不可解用例）", r.Attempted, r.Total-r.Attempted)
	}
	if r.Diff != nil && len(r.Diff.Broke) > 0 {
		s += fmt.Sprintf("；⚠ 回归 %d 项", len(r.Diff.Broke))
	}
	// 可重复性与通过率并列报出，且**分开说**——两个数不能互相替代。
	if r.Repeatability != nil {
		s += fmt.Sprintf("；可重复性 %.0f%%（%d 条 ×%d 遍，%d 条不稳）",
			*r.Repeatability*100, r.Total, r.RepeatN, len(r.UnstableCases))
	}
	return s
}

// DefaultTopN select 深度默认只看相关集的前 5 名。
//
// 5 这个数的来由：菜单上限是 12（MaxToolSchemas），本地打分只负责给出"最像的那几个"，
// 剩下的靠兜底补齐。前 5 名之外的排序基本是噪音（二元组偶然撞词），
// 拿它当断言面会让评测变成一片永远红的墙。
const DefaultTopN = 5

// judge 对一条用例的观测结果跑全部期望断言。
//
// 深度决定哪些断言适用：select 深度没有计划，就不能拿步数与验收标准去判它，
// 否则每条 select 用例都会"失败"，评测就成了噪音。
// 不可解用例（full 深度）反过来：**只判"有没有硬猜成功"**，
// 任何"希望它做成什么"的期望对不可解目标都没有意义。
func judge(c Case, obs observation, d Depth) []Check {
	var out []Check

	// 不可解用例（full 深度）：唯一的期望是"识别为不可解"。
	// 状态不是 success 即算识别——"怎么拒绝"可以多样，"硬猜成功"只有一种形态。
	if c.Unsolvable && d == DepthFull {
		handled := obs.Status != "success"
		detail := "实际 " + obs.Status
		if handled {
			detail = "如实报告为 " + obs.Status + "（没有硬猜成功）"
		} else {
			detail = "对不可解目标报了 success——指标这样记会激励硬猜"
		}
		out = append(out, Check{Name: "识别为不可解（不硬猜）", Passed: handled, Detail: detail})
		return out
	}

	if obs.Error != "" {
		out = append(out, Check{Name: "执行无错误", Passed: false, Detail: obs.Error})
	} else {
		out = append(out, Check{Name: "执行无错误", Passed: true})
	}

	// select 深度只判相关集的前 TopN 名（见 DefaultTopN 的说明）；
	// plan/full 深度判完整集合——那里的集合是模型真的挑出来的，每一项都算数。
	window := obs.Tools
	if d == DepthSelect {
		n := c.TopN
		if n <= 0 {
			n = DefaultTopN
		}
		if len(window) > n {
			window = window[:n]
		}
	}

	for _, want := range c.WantTools {
		if idx := indexOf(window, want); idx >= 0 {
			detail := ""
			if d == DepthSelect {
				detail = fmt.Sprintf("相关度第 %d 名", idx+1)
			}
			out = append(out, Check{Name: "应选到 " + want, Passed: true, Detail: detail})
		} else {
			out = append(out, Check{
				Name: "应选到 " + want, Passed: false,
				Detail: whyMissing(want, obs.Tools, window, d),
			})
		}
	}

	for _, bad := range c.NotWantTools {
		if idx := indexOf(window, bad); idx >= 0 {
			detail := "误路由——目标与这个工具无关，选到它多半会做错事"
			if d == DepthSelect {
				detail = fmt.Sprintf("相关度第 %d 名，进了前 %d 名：%s",
					idx+1, len(window), joinOrNone(window))
			}
			out = append(out, Check{Name: "不该选到 " + bad, Passed: false, Detail: detail})
		} else {
			out = append(out, Check{Name: "不该选到 " + bad, Passed: true})
		}
	}

	// 计划类断言只在有计划的深度上判。
	if d == DepthPlan || d == DepthFull {
		if c.MinSteps > 0 || c.MaxSteps > 0 {
			ok := true
			detail := fmt.Sprintf("实际 %d 步", obs.Steps)
			if c.MinSteps > 0 && obs.Steps < c.MinSteps {
				ok = false
			}
			if c.MaxSteps > 0 && obs.Steps > c.MaxSteps {
				ok = false
			}
			name := fmt.Sprintf("步数在 %d..%d 之间", c.MinSteps, c.MaxSteps)
			if c.MinSteps == 0 {
				name = fmt.Sprintf("步数不超过 %d", c.MaxSteps)
			}
			out = append(out, Check{Name: name, Passed: ok, Detail: detail})
		}
		if c.WantAcceptance {
			ok := obs.Acceptance > 0
			detail := fmt.Sprintf("实际 %d 条", obs.Acceptance)
			if !ok {
				detail = "没给验收标准——规划时就该定好「做到什么算完成」，否则反思阶段只能凭感觉打分"
			}
			out = append(out, Check{Name: "给出验收标准", Passed: ok, Detail: detail})
		}
		if len(c.WantStatus) > 0 {
			ok := contains(c.WantStatus, obs.Status)
			out = append(out, Check{
				Name:   "目标状态属于 " + strings.Join(c.WantStatus, "|"),
				Passed: ok,
				Detail: "实际 " + obs.Status,
			})
		}
	}

	return out
}

func contains(list []string, want string) bool {
	return indexOf(list, want) >= 0
}

// indexOf 返回 0 起的下标，找不到返回 -1。
func indexOf(list []string, want string) int {
	for i, s := range list {
		if s == want {
			return i
		}
	}
	return -1
}

// whyMissing 说明"该选到的工具没进窗口"到底差在哪：是排到窗口外了，还是压根没命中。
// 这个区别对排障很重要——前者是排序问题（改描述里的关键词权重），后者是没写清楚。
func whyMissing(want string, all, window []string, d Depth) string {
	if d != DepthSelect {
		return fmt.Sprintf("实际用到 %s", joinOrNone(all))
	}
	if idx := indexOf(all, want); idx >= 0 {
		return fmt.Sprintf("相关度第 %d 名，落在前 %d 名之外；前 %d 名：%s",
			idx+1, len(window), len(window), joinOrNone(window))
	}
	return fmt.Sprintf("本地打分完全没命中；前 %d 名：%s", len(window), joinOrNone(window))
}

func joinOrNone(list []string) string {
	if len(list) == 0 {
		return "（空）"
	}
	return strings.Join(list, "、")
}

// ---------- 可重复性：通过率回答"对不对"，这里回答"稳不稳" ----------

// runSignature 一次运行的可比较指纹。
//
// 只收三个确定性字段：通过与否、步数、用到的工具集合。
//
//   - **工具集合排序后比较**：顺序不算差异。模型把 file.read 排在 file.list 前面
//     只是措辞问题，不是判断不稳；而"用没用某个工具"才是真的不稳——
//     上一轮减法实验正是靠这一条才发现噪声基线比处理效应还大。
//   - **不收分数与耗时**：mock 下分数恒等、耗时本来就会抖，
//     把它们算进来只会制造一堆假不稳定，把真信号淹掉。
func runSignature(passed bool, steps int, tools []string) string {
	sorted := append([]string(nil), tools...)
	sort.Strings(sorted)
	return fmt.Sprintf("%s|%d步|%s", passMark(passed), steps, strings.Join(sorted, ","))
}

func passMark(passed bool) string {
	if passed {
		return "通过"
	}
	return "未过"
}

// modalVariant 找出出现次数最多的一种结果，返回它和它的出现次数。
// 并列时取第一次出现的那个（sigs 按运行顺序传入，结果因此是确定的）。
func modalVariant(sigs []string) (string, int) {
	counts := map[string]int{}
	order := make([]string, 0, len(sigs))
	for _, s := range sigs {
		if counts[s] == 0 {
			order = append(order, s)
		}
		counts[s]++
	}
	best, bestN := "", 0
	for _, s := range order {
		if counts[s] > bestN {
			best, bestN = s, counts[s]
		}
	}
	return best, bestN
}

// describeVariants 把各次运行的结果按"出现次数从多到少"排成人能读的明细。
func describeVariants(sigs []string) []string {
	counts := map[string]int{}
	order := make([]string, 0, len(sigs))
	for _, s := range sigs {
		if counts[s] == 0 {
			order = append(order, s)
		}
		counts[s]++
	}
	// 次数多的排前面；同次数保持首次出现顺序，保证输出稳定
	sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
	out := make([]string, 0, len(order))
	for _, s := range order {
		out = append(out, fmt.Sprintf("%s ×%d", s, counts[s]))
	}
	return out
}
