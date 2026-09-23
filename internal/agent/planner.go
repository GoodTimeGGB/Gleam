package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"

	"gleam/internal/config"
	"gleam/internal/harness/memory"
	"gleam/internal/harness/registry"
	"gleam/internal/llm"
	"gleam/internal/textmatch"
	"gleam/pkg/types"
)

// Planner 规划器：将用户目标拆解为结构化 JSON 计划，并做规划校验。
type Planner struct {
	LLM      llm.Client
	Reg      *registry.Registry
	MaxSteps int
	Style    string // 协作风格：rigorous | gentle | efficient
	Role     string // 专家角色 ID（general/analyst/writer/coder/pm/researcher/ops）
	TaskMode string // 任务模式：work | code（chat 不经过规划器）
	TaskID   string // 任务 ID：把这次调用的用量归集到消耗看板
	// MaxToolSchemas 单次规划最多注入多少个工具的完整 schema；0 表示不限（全量注入）。
	// 工具一多，全量 schema 既烧 token 又让模型挑不清——超限时改为「能力菜单 + 按需筛选」。
	MaxToolSchemas int
	// Helper 可选：辅助模型（配了 fast_model 时用它做工具筛选，省主模型的钱）
	Helper llm.Client
	// ForceTools 必须附上完整 schema 的工具名。
	// 菜单模式存在漏筛风险：模型可能想用菜单里没给 schema 的工具，参数全靠猜。
	// 因此把本任务此前各轮真正用过的工具固定保留——用过的，下一轮多半还得用。
	ForceTools []string
	// RoleTools 本场景（专家角色模板）的核心工具，同样强制附完整 schema。
	// 与 ForceTools 分开，因为理由不同：一个是"场景需要"，一个是"上次用过"，
	// 提示词里要分开说，用户才知道某个工具为什么会出现。
	RoleTools []string
	// Tier 本次规划**实际生效**的模型档位名（未配档位、回退主模型时为空）。
	//
	// 它只用于规则表做范围判断（见 rules.go）：规则开关必须挂在稳定维度上，
	// 档位正是其中之一——同一档位下提示词逐字节不变，前缀缓存不受影响。
	//
	// 注意是"生效档位"而不是"角色声明的档位"：声明只说明这个场景**建议**用哪一档，
	// 没配时角色照样跑在主模型上。拿声明档位切规则会切出"跑着主模型、
	// 却按强模型的口径删规则"这种错配。
	Tier string
	// OnLLMDelta 可选：流式规划文本回调（由 Agent 接到进度通道）
	OnLLMDelta func(text string)
	// LastBreakdown 最近一次构建系统提示词的分段计量（见 buildSystemPromptMeasured）。
	// Planner 每个任务新建一份，规划循环逐轮覆盖读取是安全的。
	LastBreakdown types.ContextBreakdown
	// Pinned 钉住区：不可压缩的用户约束/关键结论（来自 Manager.Pin，P2-1）。
	// 注入时排在易变段最前、按原顺序渲染、绝不重排（D6：重排会让缓存前缀与
	// 模型看到的约束顺序一起漂移）。装配规划器时设置一次即可。
	Pinned []string
}

// Plan 生成计划。recent 为短期记忆，relevant 为长期记忆命中，summary 为
// 上下文压缩产生的滚动摘要（早期对话），feedback 为重规划反馈。
func (p *Planner) Plan(ctx context.Context, goal, cwd string, recent []memory.Turn, relevant []memory.Hit, summary, feedback string) (types.Plan, error) {
	sys := p.buildSystemPrompt(goal, cwd, recent, relevant, summary, feedback)
	req := llm.ChatRequest{System: sys, Messages: []llm.Message{{Role: llm.RoleUser, Content: goal}}, TaskID: p.TaskID}

	var text string
	var err error
	if p.OnLLMDelta != nil {
		text, err = p.LLM.ChatStream(ctx, req, p.OnLLMDelta)
	} else {
		text, err = p.LLM.Chat(ctx, req)
	}
	if err != nil {
		return types.Plan{}, fmt.Errorf("规划调用失败: %w", err)
	}

	raw, err := extractJSON(text)
	if err != nil {
		return types.Plan{}, fmt.Errorf("规划输出不是有效 JSON: %w", err)
	}
	plan, err := p.validate(raw, goal)
	if err != nil {
		return types.Plan{}, fmt.Errorf("计划校验失败: %w", err)
	}
	return plan, nil
}

// ---------- 能力菜单（薄 Harness：工具多了不再全量注入） ----------

// toolMenuSection 生成「能力菜单」：全部工具名 + 一句话说明。
// 它只依赖工具注册表、不依赖目标，因此属于**稳定段**——放在提示词前部，
// 让服务端的前缀缓存能跨任务命中。完整 schema 见 toolSchemaSection（那部分随目标变化）。
// 工具数在上限之内时返回空：schema 本来就全给，再列一遍菜单纯属重复。
func (p *Planner) toolMenuSection() string {
	if p.MaxToolSchemas <= 0 {
		return ""
	}
	tools := p.Reg.List()
	if len(tools) <= p.MaxToolSchemas {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n## 能力菜单（共 %d 个工具，完整 schema 见后文）\n", len(tools))
	for _, t := range tools {
		fmt.Fprintf(&b, "- %s：%s\n", t.Name(), firstLine(t.Description()))
	}
	return b.String()
}

// toolSchemaSection 生成「本次可用工具」的完整 schema。
// 它会随目标变化（菜单模式下按目标筛选），属于**易变段**，必须排在稳定段之后。
func (p *Planner) toolSchemaSection(goal string) string {
	tools := p.Reg.List()
	if p.MaxToolSchemas <= 0 || len(tools) <= p.MaxToolSchemas {
		var b strings.Builder
		b.WriteString("\n## 可用工具\n")
		for _, t := range tools {
			schema, _ := json.Marshal(t.Schema())
			fmt.Fprintf(&b, "- %s：%s 参数schema: %s\n", t.Name(), t.Description(), schema)
		}
		return b.String()
	}
	picked := p.selectTools(goal, tools, p.MaxToolSchemas)
	var b strings.Builder
	if len(p.RoleTools) > 0 {
		b.WriteString("\n（本场景核心工具：" + strings.Join(p.RoleTools, "、") + "，已附完整 schema）\n")
	}
	if len(p.ForceTools) > 0 {
		b.WriteString("\n（本任务此前已用过：")
		for i, n := range p.ForceTools {
			if i > 0 {
				b.WriteString("、")
			}
			b.WriteString(n)
		}
		b.WriteString("，已附完整 schema）\n")
	}
	fmt.Fprintf(&b, "\n## 可用工具（以下 %d 个与本次目标最相关，附完整参数 schema）\n", len(picked))
	for _, t := range picked {
		schema, _ := json.Marshal(t.Schema())
		fmt.Fprintf(&b, "- %s：%s 参数schema: %s\n", t.Name(), t.Description(), schema)
	}
	b.WriteString("只能使用上面附了 schema 的工具；菜单里的其他工具本次不可用。\n")
	return b.String()
}

// selectTools 挑出与本次目标最相关的若干工具：优先用辅助模型快筛，没有就退回本地关键词打分。
// reply 始终保留——它是几乎所有计划的收尾步骤。
func (p *Planner) selectTools(goal string, tools []types.Tool, n int) []types.Tool {
	var picked []types.Tool
	pickedSet := map[string]bool{}
	keep := func(t types.Tool) {
		if !pickedSet[t.Name()] && len(picked) < n {
			pickedSet[t.Name()] = true
			picked = append(picked, t)
		}
	}
	// 收尾工具、字面命中目标的工具，以及本任务此前用过的工具优先保留
	for _, t := range tools {
		if t.Name() == "reply" || strings.Contains(strings.ToLower(goal), t.Name()) || p.forced(t.Name()) {
			keep(t)
		}
	}
	// ForceTools 里可能有当前注册表已不存在的工具（例如 MCP 断连），也要占位提示
	for _, name := range p.ForceTools {
		if t, ok := p.Reg.Get(name); ok {
			keep(t)
		}
	}
	// 场景模板声明的核心工具优先于按目标快筛的结果：
	// 快筛是"猜这次要用什么"，模板是"这个场景本来就靠这几件家伙吃饭"。
	for _, name := range p.RoleTools {
		if t, ok := p.Reg.Get(name); ok {
			keep(t)
		}
	}
	names := p.screenToolNames(goal, tools, n)
	if len(names) == 0 {
		names = scoreToolNames(goal, tools, n) // 没配辅助模型或快筛失败：退回本地关键词打分
	}
	for _, name := range names {
		if t, ok := p.Reg.Get(name); ok {
			keep(t)
		}
	}
	// 兜底：按注册顺序补齐，保证不会因为筛选失败而无工具可用
	for _, t := range tools {
		keep(t)
	}
	return picked
}

// SelectTools 返回「本次会附上完整 schema 的工具名」，按选择顺序。
//
// 暴露它是为了评测与排障：工具筛选是**纯本地、确定性**的（没配辅助模型时走关键词打分），
// 所以可以用它断言「某个目标会不会选到某个工具」——这正是提示词与工具描述改动最容易
// 悄悄弄坏、又最难靠人工发现的地方（比如把 schedule.create 的描述改短到丢了关键词，
// 定时类目标就再也选不到它了，而计划看起来仍然"正常"）。
//
// 注意：配了辅助模型（Helper）时筛选会走模型，结果不再确定；评测要确定性就把 Helper 置空。
// 非菜单模式（MaxToolSchemas<=0 或工具数不超限）下全部工具都带 schema，直接返回全部。
func (p *Planner) SelectTools(goal string) []string {
	tools := p.Reg.List()
	if p.MaxToolSchemas <= 0 || len(tools) <= p.MaxToolSchemas {
		out := make([]string, 0, len(tools))
		for _, t := range tools {
			out = append(out, t.Name())
		}
		return out
	}
	picked := p.selectTools(goal, tools, p.MaxToolSchemas)
	out := make([]string, 0, len(picked))
	for _, t := range picked {
		out = append(out, t.Name())
	}
	return out
}

// SystemPromptFor 构建一次规划用的系统提示词（不带历史、记忆、摘要）。
//
// 供评测与体检量测提示词体积与布局——提示词是拼出来的，光读代码数不出字节数。
// 只传 goal 与 cwd，正是为了让"同一角色下换目标"这种量法可复现。
func (p *Planner) SystemPromptFor(goal, cwd string) string {
	return p.buildSystemPrompt(goal, cwd, nil, nil, "", "")
}

// ActiveRules 本次规划**实际生效**的规则 ID，按它们在提示词里出现的顺序。
//
// 用来给产出打上"用了哪版规则"的标记（见 RuleSetVersion 与 ActiveRuleIDs）。
func (p *Planner) ActiveRules() []string { return ActiveRuleIDs(p.TaskMode, p.Tier, p.Role) }

// RelevantTools 返回本地关键词打分认为与目标相关的工具（得分 > 0），按相关度排序。
//
// 与 SelectTools 的区别很关键：SelectTools 返回的是**最终菜单**，而 selectTools 末尾有一条
// 「按注册顺序补齐」的兜底，菜单永远会被填满 MaxToolSchemas 个。于是"某工具在菜单里"
// 既可能因为它相关，也可能只因为它注册得早——拿菜单做回归断言会得到虚假的安心：
// 把某个工具的说明改到丢光关键词，它仍可能被兜底塞回菜单，评测却全绿。
//
// 这里刻意只返回打分命中的那一层（补齐之前），并且不带 RoleTools / ForceTools / 字面命中
// 这些强制保留项——那些是场景模板与调用方的决定，不是"描述写没写清楚"的信号。
// 所以这个集合对工具描述改动恰好是最敏感的：描述丢了关键词，工具就掉出这个集合。
func (p *Planner) RelevantTools(goal string) []string {
	tools := p.Reg.List()
	if len(tools) == 0 {
		return nil
	}
	n := p.MaxToolSchemas
	if n <= 0 || n > len(tools) {
		n = len(tools)
	}
	return scoreToolNames(goal, tools, n)
}

// routingTextLimit 路由打分只看工具描述的**开头这一段**（字符数）。
//
// 为什么要有这个上限：工具描述里混着两类文字——「这是什么、什么时候用」与
// 「参数怎么填、用的时候注意什么」。后者是写给**已经选中该工具**的模型看的，
// 不是路由信号；但它同样含词，会被关键词打分当成相关度。实测到的两处污染：
// schedule.create 的「不要自己拼 cron」让它在闲聊目标「你好，简单介绍一下你自己」里排到第 1，
// 示例「每日下载目录整理」让它在「把上季度销售数据整理成表格并汇总」里排到第 1。
// 给一个上限，就把「模型读得到」与「路由看得到」分开了——**描述该写多长就写多长，
// 路由只认前一段**。
//
// 100 这个数的依据：要容下"什么时候用"那一句（schedule.create 的
// 「当用户说“每天/每周/每月/工作日/周末/每隔多久/到点提醒我…」在第 89 字结束），
// 又要排除紧随其后的参数说明与用法提醒（第 120 字起）。
// 这条耦合由评测的定时用例守着：谁把触发词挪到 100 字之后，`gleam eval` 的
// schedule-* 用例会立刻红——这正是评测该干的活，不该靠人记住这个数字。
const routingTextLimit = 100

// routingText 返回工具用于相关度打分的文本：名称 + 描述开头一段。
func routingText(t types.Tool) string {
	return strings.ToLower(t.Name() + " " + types.Shorten(t.Description(), routingTextLimit))
}

// scoreToolNames 纯本地的相关度打分：中文按二元组、英文按整词切分目标，
// 统计每个工具的自述命中了哪些词，命中越多越相关（名称命中权重更高）。
// 零依赖、零额外调用，是没配辅助模型时的兜底路径。
//
// 两条口径都是踩过坑才定下来的：
//
//  1. **打分文本只取自述的开头一段**（见 routingTextLimit）——描述里的举例与用法提醒
//     不算路由信号。
//  2. **英文/数字按整词匹配，且至少 3 个字符**——目标是 "go" 时不该命中描述里的 "goal"。
//     子串匹配会把 `go`/`in`/`is` 这类两字母词变成到处都是的噪音。
func scoreToolNames(goal string, tools []types.Tool, n int) []string {
	terms := textmatch.Terms(goal)
	if len(terms) == 0 {
		return nil
	}
	type scored struct {
		name  string
		score int
	}
	var list []scored
	for _, t := range tools {
		// 名称命中权重更高：工具名是作者对"这东西干什么"最凝练的表述。
		// 判据本身（切词与匹配方式）与技能筛选共用 textmatch 一份实现——
		// 两处各写一份，"相关"就会有两个含义。
		name := strings.ToLower(t.Name())
		s := textmatch.Score(terms, routingText(t)) + 2*textmatch.Score(terms, name)
		if s > 0 {
			list = append(list, scored{t.Name(), s})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].name < list[j].name // 同分按名字排序，保证结果稳定
	})
	if len(list) > n {
		list = list[:n]
	}
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.name)
	}
	return out
}

// forced 判断工具是否在强制保留名单里。
func (p *Planner) forced(name string) bool {
	for _, n := range p.ForceTools {
		if n == name {
			return true
		}
	}
	return false
}

// screenToolNames 用辅助模型做一次极短的快筛：从菜单里挑出本次可能用到的工具名。
// 失败或没配辅助模型时返回 nil，由调用方退回本地打分。
func (p *Planner) screenToolNames(goal string, tools []types.Tool, n int) []string {
	if p.Helper == nil {
		return nil
	}
	var menu strings.Builder
	for _, t := range tools {
		fmt.Fprintf(&menu, "%s：%s\n", t.Name(), firstLine(t.Description()))
	}
	sys := llm.MarkerToolPick + "\n你是工具筛选器。根据用户目标，从工具菜单里挑出最可能用到的工具，" +
		"只输出 JSON 数组（工具名字符串），最多 " + fmt.Sprint(n) + " 个，不要解释。"
	req := llm.ChatRequest{
		System: sys,
		Messages: []llm.Message{{Role: llm.RoleUser,
			Content: "目标：" + types.Shorten(goal, 300) + "\n\n工具菜单：\n" + menu.String()}},
		Temperature: 0, MaxTokens: 200, TaskID: p.TaskID,
	}
	text, err := p.Helper.Chat(context.Background(), req)
	if err != nil {
		return nil
	}
	raw, err := extractJSON(text)
	if err != nil {
		return nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil
	}
	return names
}

// firstLine 取描述的第一行，保证菜单里一行一个工具。
func firstLine(s string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	return types.Shorten(s, 60)
}

func styleInstruction(style string) string {
	switch style {
	case "rigorous":
		return "严谨克制：描述精确，不夸大，不遗漏限制条件。"
	case "gentle":
		return "温和自然：像一位可靠的同事，平实友好。"
	default:
		return "高效简洁：直击要点，不做冗余解释。"
	}
}

// 规划器提示词的固定段落。抽成常量不只是为了整洁——它们标记了**稳定段的边界**：
// 这些文本在任何一次请求里都逐字节相同，正是服务端前缀缓存能复用的部分。
const promptIdentity = "你是 Gleam（微光），一个本地优先的桌面智能体的规划器。" +
	"你的职责是把用户目标拆解为可执行的工具调用步骤。"

// buildSystemPrompt 组装规划器系统提示词。
//
// 布局遵循一条硬约束——**稳定段在前、易变段在后**：
// 服务端前缀缓存要求逐字节相同的最长公共前缀，只要有易变内容（工作目录、时间、
// 摘要、历史对话）夹在中间，它后面的内容就全部作废。因此顺序不是审美问题：
// 把易变段沉底，才能让「身份 + 规则 + 格式 + 角色 + 能力菜单」这一大段跨任务复用。
// 规划器注入上限（一处定义，buildSystemPrompt 与就绪体检的满载样本共用）。
// 体检用它构造满载状态样本测「状态占比 > 60%」判据——样本不跟常量走的话，
// 上限被调大后谁也不会重新看一眼占比，膨胀就是静默发生的。
const (
	recentTurnsInjected = 6   // 注入的最近对话轮数
	turnContentCap      = 160 // 每轮对话内容的截断长度
	summaryInjectCap    = 600 // 滚动摘要的截断长度
	memoryHitsInjected  = 3   // 注入的长期记忆命中条数
	memoryHitContentCap = 120 // 每条记忆内容的截断长度
	pinnedInjected      = 8   // 注入的钉住条数（钉住区最多 20 条，注入取最近的 8 条）
	pinnedInjectCap     = 200 // 每条钉住内容的截断长度（rune）
)

func (p *Planner) buildSystemPrompt(goal, cwd string, recent []memory.Turn, relevant []memory.Hit, summary, feedback string) string {
	s, _ := p.buildSystemPromptMeasured(goal, cwd, recent, relevant, summary, feedback)
	return s
}

// buildSystemPromptMeasured 组装系统提示词，同时返回分段计量（见 types.ContextBreakdown）。
//
// 只记总字符数时，「上下文膨胀」类问题无从定位——站点判据是状态段占比 > 60% 即膨胀，
// 而占比只有在分段计量里才看得见。计量随写入就地做（b.Len() 差值），不复制字符串。
func (p *Planner) buildSystemPromptMeasured(goal, cwd string, recent []memory.Turn, relevant []memory.Hit, summary, feedback string) (string, types.ContextBreakdown) {
	var b strings.Builder
	var bd types.ContextBreakdown
	// seg 执行一段写入并返回这段的字符数
	seg := func(fn func()) int {
		start := b.Len()
		fn()
		return b.Len() - start
	}

	// ================= 稳定段 =================
	// 这一段的内容全部来自规则表（rules.go）：规则按 Slot 落位、按 Scope 生效。
	// 换行由这里决定而不是渲染器——稳定段的换行是布局契约的一部分。
	bd.Instruction += seg(func() {
		b.WriteString(promptIdentity)
		b.WriteString(llm.MarkerPlan + "\n")
		b.WriteString("\n" + renderRules(SlotContract, p.TaskMode, p.Tier, p.Role))
	})

	// 角色知识：专家角色不只是一段人设，它是这个领域的领域提示。
	bd.Knowledge += seg(func() {
		role := FindRole(p.Role)
		if p.Role != "" && p.Role != "general" && role.ID != "general" {
			fmt.Fprintf(&b, "\n## 专家角色：%s\n%s\n", role.Name, role.SystemPrompt)
		}
		// 领域提示：通用指南教"怎么写可判定的标准"，这里补"这个领域该盯哪几件事"。
		// 尺度因领域而异——数据要能对账，代码要能验证，文案要能直接交付。
		if role.AcceptanceFocus != "" {
			fmt.Fprintf(&b, "\n本角色（%s）的验收重点：%s\n", role.Name, role.AcceptanceFocus)
		}
		if role.OutputFormat != "" {
			fmt.Fprintf(&b, "\n## 本场景产出格式\n%s\n", role.OutputFormat)
		}
	})
	bd.Instruction += seg(func() {
		b.WriteString("\n## 协作风格\n" + styleInstruction(p.Style))
	})
	// 编程模式准则只在 code 模式下生效——由规则自己的 Scope 声明，
	// 不再在这里写 if：规则的"何时生效"应该和规则放在一起。
	bd.Instruction += seg(func() {
		if blk := renderRules(SlotCode, p.TaskMode, p.Tier, p.Role); blk != "" {
			b.WriteString("\n\n" + blk)
		}
	})
	bd.Capability += seg(func() {
		b.WriteString(p.toolMenuSection())
	})
	bd.Instruction += seg(func() {
		if _, ok := p.Reg.Get("schedule.create"); ok {
			b.WriteString("\n" + renderRules(SlotSchedule, p.TaskMode, p.Tier, p.Role) + "\n")
		}
	})

	// ================= 易变段（以下内容每次都可能不同，必须排在稳定段之后） =================
	bd.Capability += seg(func() {
		b.WriteString(p.toolSchemaSection(goal))
	})
	// 钉住区：易变段的最前、按加入顺序原样渲染（P2-1）。
	// 它是压缩永远不碰的不可压缩区——用户定下的约束必须在每轮上下文里活着。
	bd.State += seg(func() {
		if n := min(len(p.Pinned), pinnedInjected); n > 0 {
			b.WriteString("\n## 用户约束（钉住，永久有效）\n")
			for _, pin := range p.Pinned[:n] {
				fmt.Fprintf(&b, "- %s\n", types.Shorten(pin, pinnedInjectCap))
			}
		}
	})
	bd.State += seg(func() {
		fmt.Fprintf(&b, "\n## 上下文\n- 工作目录: %s\n- 操作系统: %s\n", cwd, runtime.GOOS)
	})
	bd.State += seg(func() {
		if strings.TrimSpace(summary) != "" {
			fmt.Fprintf(&b, "\n## 早期上下文摘要（已自动压缩）\n%s\n", types.Shorten(summary, summaryInjectCap))
		}
	})
	bd.State += seg(func() {
		if len(relevant) > 0 {
			b.WriteString("\n## 相关长期记忆\n")
			for _, h := range relevant {
				fmt.Fprintf(&b, "- %s\n", types.Shorten(h.Content, memoryHitContentCap))
			}
		}
	})
	bd.State += seg(func() {
		if len(recent) > 0 {
			b.WriteString("\n## 最近对话\n")
			for _, t := range recent {
				fmt.Fprintf(&b, "- %s: %s\n", t.Role, types.Shorten(t.Content, turnContentCap))
			}
		}
	})
	bd.State += seg(func() {
		if feedback != "" {
			fmt.Fprintf(&b, "\n## 重规划反馈（务必修正上次的问题）\n%s\n", feedback)
		}
	})
	// 收尾提醒：格式要求已移到稳定段，离生成点变远了。
	// 这里再压一行放回末尾——它排在易变段之后，不影响缓存前缀，却能把
	// "只输出 JSON"这条最容易违反的约束重新推到模型眼前。
	bd.Instruction += seg(func() {
		b.WriteString("\n## 输出提醒\n只输出一个 JSON 对象，形如 {\"steps\":[...],\"estimated_time\":\"...\",\"acceptance\":[...]}，不要任何其他文字、不要代码块。\n")
	})
	bd.Total = b.Len()
	p.LastBreakdown = bd
	return b.String(), bd
}

// ---------- 规划校验层 ----------

type rawPlan struct {
	Steps []struct {
		ID          string         `json:"id"`
		Description string         `json:"description"`
		Tool        string         `json:"tool"`
		Args        map[string]any `json:"args"`
		DependsOn   []string       `json:"depends_on"`
	} `json:"steps"`
	EstimatedTime string   `json:"estimated_time"`
	Acceptance    []string `json:"acceptance"`
}

// maxAcceptance 验收标准条数上限：够判断完成与否即可，太多会稀释重点。
const maxAcceptance = 4

// normalizeAcceptance 清洗验收标准：去空白、去重、截断过长条目、限制条数。
// 拿不到也不算错误——没有标准时反思器退回原来的整体评分。
func normalizeAcceptance(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, types.Shorten(s, 120))
		if len(out) >= maxAcceptance {
			break
		}
	}
	return out
}

// validate 校验并规范化计划：工具存在性、参数 schema、依赖 DAG 无环、步骤数上限。
func (p *Planner) validate(raw []byte, goal string) (types.Plan, error) {
	var rp rawPlan
	if err := json.Unmarshal(raw, &rp); err != nil {
		return types.Plan{}, fmt.Errorf("JSON 解析失败: %w", err)
	}
	if len(rp.Steps) == 0 {
		return types.Plan{}, fmt.Errorf("计划没有步骤")
	}
	maxSteps := p.MaxSteps
	if maxSteps <= 0 {
		// 兜底值只有一处定义（config.DefaultMaxSteps），不要在这里再写一个字面量——
		// 原先这里和 config 的默认值各写了一份 12，改一处忘一处就变成两份边界。
		maxSteps = config.DefaultMaxSteps
	}
	if len(rp.Steps) > maxSteps {
		return types.Plan{}, fmt.Errorf("步骤数 %d 超过上限 %d", len(rp.Steps), maxSteps)
	}

	plan := types.Plan{Goal: goal, EstimatedTime: rp.EstimatedTime, Acceptance: normalizeAcceptance(rp.Acceptance)}
	ids := map[string]bool{}
	for i, rs := range rp.Steps {
		id := strings.TrimSpace(rs.ID)
		if id == "" {
			id = fmt.Sprintf("s%d", i+1)
		}
		if ids[id] {
			return types.Plan{}, fmt.Errorf("步骤 ID 重复: %s", id)
		}
		ids[id] = true
		tool, ok := p.Reg.Get(strings.TrimSpace(rs.Tool))
		if !ok {
			return types.Plan{}, fmt.Errorf("步骤 %s 引用了不存在的工具 %q", id, rs.Tool)
		}
		args := rs.Args
		if args == nil {
			args = map[string]any{}
		}
		if err := validateArgs(tool, args); err != nil {
			return types.Plan{}, fmt.Errorf("步骤 %s（%s）参数不合法: %w", id, tool.Name(), err)
		}
		plan.Steps = append(plan.Steps, types.Step{
			ID: id, Description: rs.Description, Tool: tool.Name(), Args: args, DependsOn: rs.DependsOn,
		})
	}
	// 依赖校验：引用存在 + 无环 + 无自依赖
	for _, st := range plan.Steps {
		for _, d := range st.DependsOn {
			if d == st.ID {
				return types.Plan{}, fmt.Errorf("步骤 %s 依赖自身", st.ID)
			}
			if !ids[d] {
				return types.Plan{}, fmt.Errorf("步骤 %s 依赖了不存在的步骤 %q", st.ID, d)
			}
		}
	}
	if err := checkAcyclic(plan.Steps); err != nil {
		return types.Plan{}, err
	}
	return plan, nil
}

// validateArgs 按 JSON Schema 浅校验：required 存在 + 类型匹配。
func validateArgs(t types.Tool, args map[string]any) error {
	schema := t.Schema()
	req, _ := schema["required"].([]any)
	props, _ := schema["properties"].(map[string]any)
	for _, r := range req {
		key, ok := r.(string)
		if !ok {
			continue
		}
		v, present := args[key]
		if s, isStr := v.(string); isStr {
			if !present || strings.TrimSpace(s) == "" {
				return fmt.Errorf("缺少必填参数 %q", key)
			}
			continue
		}
		if !present || v == nil {
			return fmt.Errorf("缺少必填参数 %q", key)
		}
	}
	for k, v := range args {
		prop, ok := props[k].(map[string]any)
		if !ok {
			continue // 未声明的参数交给工具自行处理
		}
		typ, _ := prop["type"].(string)
		if err := checkType(k, v, typ); err != nil {
			return err
		}
	}
	return nil
}

func checkType(key string, v any, typ string) error {
	switch typ {
	case "string":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("参数 %q 应为字符串", key)
		}
	case "number", "integer":
		switch v.(type) {
		case float64, int64, int:
		default:
			return fmt.Errorf("参数 %q 应为数字", key)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("参数 %q 应为布尔值", key)
		}
	case "array":
		if _, ok := v.([]any); !ok {
			return fmt.Errorf("参数 %q 应为数组", key)
		}
	case "object":
		if _, ok := v.(map[string]any); !ok {
			return fmt.Errorf("参数 %q 应为对象", key)
		}
	}
	return nil
}

// checkAcyclic DFS 检测环。
func checkAcyclic(steps []types.Step) error {
	deps := map[string][]string{}
	for _, st := range steps {
		deps[st.ID] = st.DependsOn
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(id string, stack []string) error
	visit = func(id string, stack []string) error {
		color[id] = gray
		stack = append(stack, id)
		for _, d := range deps[id] {
			switch color[d] {
			case gray:
				return fmt.Errorf("计划存在循环依赖: %s", strings.Join(append(stack, d), " → "))
			case white:
				if err := visit(d, stack); err != nil {
					return err
				}
			}
		}
		color[id] = black
		return nil
	}
	for id := range deps {
		if color[id] == white {
			if err := visit(id, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// extractJSON 从 LLM 输出中提取首个平衡的 JSON 对象（容忍 markdown 代码块）。
// 仅将双引号视为字符串定界符（合法 JSON 不使用单引号，
// 否则字符串中的撇号会破坏括号配平）。
func extractJSON(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	start := strings.Index(s, "{")
	if start < 0 {
		return nil, fmt.Errorf("未找到 JSON 对象")
	}
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"':
			quote = c
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 && i >= start {
				return []byte(s[start : i+1]), nil
			}
		}
	}
	return nil, fmt.Errorf("JSON 对象未闭合")
}

// ---------- 流式节流器：合并 LLM 增量，避免刷屏 ----------

// DeltaThrottle 每 n 个 rune 合并推送一次。
func DeltaThrottle(n int, push func(text string)) (func(delta string), func()) {
	var mu sync.Mutex
	var buf strings.Builder
	emit := func() {
		mu.Lock()
		if buf.Len() > 0 {
			push(buf.String())
			buf.Reset()
		}
		mu.Unlock()
	}
	onDelta := func(delta string) {
		mu.Lock()
		buf.WriteString(delta)
		flush := buf.Len() >= n
		mu.Unlock()
		if flush {
			emit()
		}
	}
	return onDelta, emit
}
