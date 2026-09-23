package agent

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gleam/internal/harness/memory"
	"gleam/internal/llm"
	"gleam/internal/market"
	"gleam/pkg/types"
)

// 本文件实现「九坑就绪自检」。
//
// 来源：一篇讲 Agent SDK 的文章总结了自建 Agent Runtime 常见的九个坑，
// 结论很扎心——"几个坑叠在一起，结果就是 demo 做了很多，稳定进入生产的 Agent 很少"。
//
// 这句话难反驳，但静态清单也难落地：看一遍就忘，没法随版本回归，更没法在
// 真实运行状态里验证自己到底踩没踩。所以这里不写第二份清单，而是把它做成一次
// 可执行体检——每一项都从**真实运行状态**取证据（工具注册表、门控审计、用量归集、
// 提示词实测、成长日志、记忆库），给 pass / warn / fail 三档结论，并附上"怎么补"。
//
// 三条设计约束：
//  1. 只读：不改配置、不发网络请求、不写文件。随时可跑。
//  2. 无数据不装懂：没有历史数据时给 warn 并注明"暂无数据"，而不是伪造一个绿勾。
//  3. 结论要指向动作：每项 warn/fail 都带 fix，避免"体检报告很好看但没人动"。
//
// 九项与文章的顺序一一对应，便于对照原文。

// ReadinessStatus 单项体检结论。
type ReadinessStatus string

const (
	// ReadyPass 这一坑没踩：证据齐备。
	ReadyPass ReadinessStatus = "pass"
	// ReadyWarn 机制在但没用起来，或数据不足以下结论。
	ReadyWarn ReadinessStatus = "warn"
	// ReadyFail 结构性缺口：这一项不补，后面几项都会各修一遍。
	ReadyFail ReadinessStatus = "fail"
)

// ReadinessItem 一个坑的体检结果。
type ReadinessItem struct {
	Index    int             `json:"index"`
	Key      string          `json:"key"`
	Title    string          `json:"title"`
	Status   ReadinessStatus `json:"status"`
	Summary  string          `json:"summary"`
	Evidence []string        `json:"evidence"`
	Fix      string          `json:"fix,omitempty"`
}

// ReadinessReport 九坑体检报告。
type ReadinessReport struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Total       int             `json:"total"`
	Passed      int             `json:"passed"`
	Warned      int             `json:"warned"`
	Failed      int             `json:"failed"`
	Verdict     string          `json:"verdict"` // ready | needs_work | not_ready
	Items       []ReadinessItem `json:"items"`
}

// readinessPitfalls 九坑的键名与标题，顺序即文章顺序。
var readinessPitfalls = []struct {
	Key   string
	Title string
}{
	{"runtime", "Runtime 重复建设"},
	{"delivery", "只会聊不会交付"},
	{"model_lock", "模型与能力锁定"},
	{"audit_split", "权限与审计割裂"},
	{"practice_reuse", "最佳实践不可复制"},
	{"business_gap", "体验与业务脱节"},
	{"cache_control", "模型调用缓存不可控"},
	{"iteration_arch", "缺少健康迭代的简单架构"},
	{"extensible_memory", "可扩展架构与压缩记忆"},
}

// Readiness 执行九坑就绪体检，返回只读报告。
func (a *Agent) Readiness() ReadinessReport {
	checks := []func() ReadinessItem{
		a.checkRuntimeKernel,
		a.checkDelivery,
		a.checkModelLock,
		a.checkAuditSplit,
		a.checkPracticeReuse,
		a.checkBusinessGap,
		a.checkCacheControl,
		a.checkIterationArch,
		a.checkExtensibleMemory,
	}
	rep := ReadinessReport{GeneratedAt: time.Now(), Total: len(checks)}
	for i, fn := range checks {
		it := fn()
		it.Index = i + 1
		it.Key = readinessPitfalls[i].Key
		it.Title = readinessPitfalls[i].Title
		if it.Evidence == nil {
			it.Evidence = []string{}
		}
		switch it.Status {
		case ReadyPass:
			rep.Passed++
		case ReadyWarn:
			rep.Warned++
		default:
			rep.Failed++
		}
		rep.Items = append(rep.Items, it)
	}
	switch {
	case rep.Failed > 0:
		rep.Verdict = "not_ready"
	case rep.Warned > 0:
		rep.Verdict = "needs_work"
	default:
		rep.Verdict = "ready"
	}
	return rep
}

// ---------- 坑 1：Runtime 重复建设 ----------

// checkRuntimeKernel 判断依据不是"写了多少代码"，而是"有多少子系统挂在同一个内核上"。
// 会话、循环、工具、状态、错误处理各做一套，才叫重复建设；
// 五条入口（任务循环 / 对话 / 调度触发 / 技能运行 / 工具直调）共用同一个 Agent，
// 工具 schema 与权限出自同一张表，才叫没重复建设。
func (a *Agent) checkRuntimeKernel() ReadinessItem {
	core := []struct {
		name string
		ok   bool
	}{
		{"工具注册表", a.Reg != nil},
		{"门控", a.Gate != nil},
		{"记忆", a.Mem != nil},
		{"技能", a.Skills != nil},
	}
	ext := []struct {
		name string
		ok   bool
	}{
		{"调度", a.Sched != nil},
		{"成长日志", a.Growth != nil},
		{"多会话", a.Convos != nil},
		{"微光空间", a.Spaces != nil},
	}
	coreOK, extOK := 0, 0
	var missing []string
	for _, c := range core {
		if c.ok {
			coreOK++
		} else {
			missing = append(missing, c.name)
		}
	}
	for _, c := range ext {
		if c.ok {
			extOK++
		}
	}

	toolCount, noSchema := 0, 0
	if a.Reg != nil {
		for _, t := range a.Reg.List() {
			toolCount++
			if len(t.Schema()) == 0 {
				noSchema++
			}
		}
	}

	ev := []string{
		fmt.Sprintf("统一工具注册表：%d 个工具（schema 与权限出自同一张表）", toolCount),
		fmt.Sprintf("核心子系统：%d/4（工具注册表 / 门控 / 记忆 / 技能）", coreOK),
		fmt.Sprintf("扩展子系统：%d/4（调度 / 成长日志 / 多会话 / 微光空间）", extOK),
		"五条入口共用同一内核：任务循环 / 对话 / 调度触发 / 技能运行 / 工具直调",
		fmt.Sprintf("当前模型客户端：%s", a.LLMName()),
	}
	if noSchema > 0 {
		ev = append(ev, fmt.Sprintf("其中 %d 个工具未声明 schema（模型无法正确传参）", noSchema))
	}

	it := ReadinessItem{Evidence: ev}
	switch {
	case a.Reg == nil || a.Gate == nil:
		it.Status = ReadyFail
		it.Summary = "核心内核不完整：" + strings.Join(missing, "、") + " 未装配"
		it.Fix = "先补齐缺失子系统——内核不统一，后面八项都会各修一遍。"
	case noSchema > 0:
		it.Status = ReadyFail
		it.Summary = fmt.Sprintf("%d 个工具没有 schema，模型调用时只能猜参数", noSchema)
		it.Fix = "给这些工具的 Schema() 补上参数定义，或把它们从注册表里摘掉。"
	case coreOK == 4 && extOK == 4:
		it.Status = ReadyPass
		it.Summary = "单内核单注册表：9 个子系统挂在同一个 Agent 上，五条入口复用同一套工具与门控"
	default:
		it.Status = ReadyPass
		it.Summary = fmt.Sprintf("核心内核完整（%d/4），扩展子系统 %d/4 已装配", coreOK, extOK)
	}
	return it
}

// effectivePermissionOf 取工具的有效权限：门控在就用门控（含用户覆盖），
// 门控缺席时退回工具自身的声明——体检本身不能把引擎跑挂。
func (a *Agent) effectivePermissionOf(t types.Tool) types.Permission {
	if a.Gate != nil {
		return a.Gate.EffectivePermission(t)
	}
	return t.Permission()
}

// ---------- 坑 2：只会聊不会交付 ----------

// checkDelivery 交付能力 = 真实工具（能改能跑）+ 验收门禁（知道什么叫完成）+ 结构化结果。
// 三者缺一，产出就只剩"一段看起来像答案的文本"。
func (a *Agent) checkDelivery() ReadinessItem {
	readonly, writable, executable := 0, 0, 0
	toolCount := 0
	if a.Reg != nil {
		for _, t := range a.Reg.List() {
			toolCount++
			switch a.effectivePermissionOf(t) {
			case types.PermissionReadOnly:
				readonly++
			case types.PermissionUserApproved:
				writable++
			default:
				executable++
			}
		}
	}
	chatCheck := "关闭"
	if a.Cfg.Agent.ChatAcceptance {
		chatCheck = "开启"
	}
	ev := []string{
		fmt.Sprintf("真实工具：%d 个（只读 %d / 需审批 %d / 完全放行 %d）", toolCount, readonly, writable, executable),
		"验收门禁：任务模式内置三闸门（目标覆盖 / 可判定 / 证据回填）",
		fmt.Sprintf("对话模式独立自检：%s", chatCheck),
		"结构化结果：GoalResult（status / score / summary / artifacts / usage）",
	}

	it := ReadinessItem{Evidence: ev}
	switch {
	case toolCount == 0:
		it.Status = ReadyFail
		it.Summary = "一个工具都没有，只能聊"
		it.Fix = "至少接入文件读写与命令执行，否则 Agent 无法产出可交付物。"
	case writable+executable == 0:
		it.Status = ReadyWarn
		it.Summary = "只有只读工具：能查不能改，交付不了东西"
		it.Fix = "补上 file.write / shell.exec 这类产出型工具。"
	case !a.Cfg.Agent.ChatAcceptance:
		it.Status = ReadyWarn
		it.Summary = "工具齐备，但对话模式没有独立自检，问答结果无人把关"
		it.Fix = "打开设置里的 agent.chat_acceptance（对话模式回答后由辅助模型做一次独立自检）。"
	default:
		it.Status = ReadyPass
		it.Summary = fmt.Sprintf("有真实工具（%d 个可写/可执行）、有验收门禁、有结构化结果", writable+executable)
	}
	return it
}

// ---------- 坑 3：模型与能力锁定 ----------

// checkModelLock 锁不锁死，看的是"换模型要不要改代码"。
// 厂商目录 + 多协议 + 场景档位这三样都在配置里，换厂商就是改一行配置。
// 真正会被锁死的是"只用了一个模型"——机制在，但没用起来。
func (a *Agent) checkModelLock() ReadinessItem {
	tiers := a.Cfg.LLM.Tiers
	names := make([]string, 0, len(tiers))
	for k := range tiers {
		names = append(names, k)
	}
	sort.Strings(names)
	tierDesc := "未配置"
	if len(names) > 0 {
		parts := make([]string, 0, len(names))
		for _, n := range names {
			parts = append(parts, n+"→"+tiers[n])
		}
		tierDesc = strings.Join(parts, "，")
	}

	protoSet := map[string]bool{}
	for _, p := range llm.Providers {
		for _, pl := range p.Plans {
			protoSet[pl.Protocol] = true
		}
	}
	fast := "未配置（辅助调用走主模型）"
	if a.FastLLM != nil {
		fast = a.FastLLM.Name()
	}
	// 验收模型单独报一行：回退与换成功都会返回一个可用客户端，光看有没有客户端看不出区别，
	// 所以这里明说"换没换成"——独立验收要么是可核对的事实，要么就承认没有。
	verifyDesc := fmt.Sprintf("%s（与执行者同源：%s 档未配置或与执行者同模型）", a.VerifyModel(""), DefaultVerifyTier)
	if a.verifyIndependent("") {
		verifyDesc = fmt.Sprintf("%s（与执行者不同档，独立验收生效）", a.VerifyModel(""))
	}

	ev := []string{
		fmt.Sprintf("内置厂商目录：%d 家（换厂商只改配置，不动代码）", len(llm.Providers)),
		fmt.Sprintf("可切换协议：%d 种（OpenAI Chat / OpenAI Responses / Anthropic）", len(protoSet)),
		fmt.Sprintf("已配置模型档位：%d 档（%s）", len(tiers), tierDesc),
		fmt.Sprintf("当前主模型：%s", a.LLMName()),
		fmt.Sprintf("辅助模型：%s", fast),
		fmt.Sprintf("验收模型：%s", verifyDesc),
		"场景模板自带档位：角色声明用哪一档，档位与模型的对应关系由配置决定",
	}

	it := ReadinessItem{Evidence: ev}
	switch {
	case a.LLM == nil:
		it.Status = ReadyFail
		it.Summary = "没有可用的模型客户端"
		it.Fix = "先在设置页配置厂商与模型。"
	case len(tiers) < 2:
		it.Status = ReadyWarn
		it.Summary = "可切换的机制齐备，但只配了主模型：所有场景共用一个档位"
		it.Fix = "在设置里配置 llm.tiers（如 coding / office / reasoning），让不同场景挑不同档位。"
	default:
		it.Status = ReadyPass
		it.Summary = fmt.Sprintf("模型可换可挑：%d 家厂商、%d 种协议、%d 档已配置", len(llm.Providers), len(protoSet), len(tiers))
	}
	return it
}

// ---------- 坑 4：权限与审计割裂 ----------

// checkAuditSplit 坑的原话是"不同的入口使用的权限跟日志其实是不同的"。
// 所以要看两件事：工具是否全在同一张权限表里，以及被拦截的动作是否留了痕。
func (a *Agent) checkAuditSplit() ReadinessItem {
	if a.Gate == nil {
		return ReadinessItem{
			Status:  ReadyFail,
			Summary: "没有统一门控：权限判断散落在各入口，无从审计",
			Fix:     "把所有工具调用收敛到同一个 Gate 上裁决。",
		}
	}
	readonly, writable, executable := 0, 0, 0
	toolCount := 0
	if a.Reg != nil {
		for _, t := range a.Reg.List() {
			toolCount++
			switch a.effectivePermissionOf(t) {
			case types.PermissionReadOnly:
				readonly++
			case types.PermissionUserApproved:
				writable++
			default:
				executable++
			}
		}
	}
	// ToolList 是界面与门控共用的那份视图：两者条数不一致，说明有工具漏出了权限表。
	listed := len(a.ToolList())

	audit := a.Gate.RecentAudit(1 << 20)
	approved, denied := 0, 0
	for _, e := range audit {
		switch e.Action {
		case "denied", "reviewed_block":
			denied++
		case "approved":
			approved++
		}
	}
	// 完全放行的工具始终需要人工批准；没有审批通道时它们会被自动拒绝。
	_, nop := a.Notifier.(NopNotifier)
	hasChannel := a.Notifier != nil && !nop
	channelDesc := "已接入"
	if !hasChannel {
		channelDesc = "未接入"
	}

	ev := []string{
		fmt.Sprintf("统一门控：%d 个工具全部经同一 Gate 裁决（门面视图 %d 条）", toolCount, listed),
		fmt.Sprintf("权限分布：只读 %d / 需审批 %d / 完全放行 %d", readonly, writable, executable),
		fmt.Sprintf("安全模式：%s（审批超时 %d 秒）", a.Cfg.Safety.Mode, a.Cfg.Safety.ApprovalTimeoutSecs),
		fmt.Sprintf("审批通道：%s", channelDesc),
		fmt.Sprintf("审计留痕：%d 条（放行 %d / 拦截 %d）", len(audit), approved, denied),
		"审计覆盖的是需要人裁决的动作：审批结果与审核模型拦截",
	}

	it := ReadinessItem{Evidence: ev}
	switch {
	case listed != toolCount:
		it.Status = ReadyFail
		it.Summary = fmt.Sprintf("有 %d 个工具漏出了权限表（注册表 %d，门面视图 %d）", toolCount-listed, toolCount, listed)
		it.Fix = "让 ToolList 遍历与注册表保持同一数据源，否则界面看到的权限不是真实生效的权限。"
	case executable > 0 && !hasChannel:
		it.Status = ReadyWarn
		it.Summary = fmt.Sprintf("有 %d 个需要人工批准的工具，但没有审批通道：这些操作会被自动拒绝", executable)
		it.Fix = "接上审批通道（Web UI 审批弹窗 / 桌面端通知），否则高风险步骤永远走不通。"
	default:
		it.Status = ReadyPass
		it.Summary = fmt.Sprintf("权限与日志同源：%d 个工具一张表，%d 条审计留痕可回溯", toolCount, len(audit))
	}
	return it
}

// ---------- 坑 5：最佳实践不可复制 ----------

// checkPracticeReuse 最佳实践要能横向复制，得先有"可复制的单位"。
// Gleam 里有两个：场景模板（一次说清某个场景的提示/验收/格式/工具/档位）
// 和技能（一次沉淀一套可重复执行的做法）。
func (a *Agent) checkPracticeReuse() ReadinessItem {
	roles := RoleList()
	complete := 0
	var incomplete []string
	for _, r := range roles {
		if r.SystemPrompt != "" && r.AcceptanceFocus != "" && r.OutputFormat != "" {
			complete++
		} else {
			incomplete = append(incomplete, r.ID)
		}
	}
	skills, skillUses := 0, 0
	if a.Skills != nil {
		skills = len(a.Skills.List())
	}
	if a.Growth != nil {
		skillUses = a.Growth.Stats().SkillUses
	}

	ev := []string{
		fmt.Sprintf("场景模板：%d 个，五要素齐全 %d 个（系统提示 / 验收侧重 / 产出格式 / 核心工具 / 模型档位）", len(roles), complete),
		fmt.Sprintf("已沉淀技能：%d 个（复用 %d 次）", skills, skillUses),
		fmt.Sprintf("可安装生态：技能模板 %d 个 / MCP 预设 %d 个", len(market.SkillCatalog), len(market.MCPCatalog)),
		"新增场景只加模板，不改 Runtime：差异沉淀在模板里，底层不用动",
	}

	it := ReadinessItem{Evidence: ev}
	switch {
	case complete < len(roles):
		it.Status = ReadyFail
		it.Summary = fmt.Sprintf("有 %d 个场景模板缺要素：%s", len(incomplete), strings.Join(incomplete, "、"))
		it.Fix = "补全缺失的 SystemPrompt / AcceptanceFocus / OutputFormat——模板缺要素，复制到新场景就会走样。"
	case skills == 0:
		it.Status = ReadyWarn
		it.Summary = "场景模板齐备，但还没有沉淀出可复用的技能"
		it.Fix = "把跑通过的做法存成技能（技能列表可保存），下次同类任务直接复用。"
	default:
		it.Status = ReadyPass
		it.Summary = fmt.Sprintf("%d 个场景模板要素齐全，%d 个技能已沉淀并可复用", len(roles), skills)
	}
	return it
}

// ---------- 坑 6：体验与业务脱节 ----------

// checkBusinessGap 坑的原话是"Agent 不理解当前的用户对象、流程和审批"。
// 对应的可测信号：角色（服务对象与产出格式）+ 工作区（业务上下文边界）+ 空间（按业务隔离）。
func (a *Agent) checkBusinessGap() ReadinessItem {
	roles := RoleList()
	spaces, convos := 0, 0
	if a.Spaces != nil {
		if l, err := a.Spaces.List(); err == nil {
			spaces = len(l)
		}
	}
	if a.Convos != nil {
		if l, err := a.Convos.List(); err == nil {
			convos = len(l)
		}
	}
	ws := strings.TrimSpace(a.Cfg.Workspace)
	wsDesc := ws
	if wsDesc == "" {
		wsDesc = "未设置（使用默认目录）"
	}

	ev := []string{
		fmt.Sprintf("专家角色：%d 个，每个角色声明了服务对象、验收侧重与产出格式", len(roles)),
		fmt.Sprintf("当前工作区：%s", wsDesc),
		fmt.Sprintf("微光空间：%d 个（按业务文件夹隔离会话与上下文）", spaces),
		fmt.Sprintf("历史会话：%d 个", convos),
		"任务可携带角色：规划器按角色注入领域知识与产出格式",
	}

	it := ReadinessItem{Evidence: ev}
	switch {
	case len(roles) == 0:
		it.Status = ReadyFail
		it.Summary = "没有场景模板，Agent 不知道自己在为谁服务"
		it.Fix = "至少保留通用角色，并按主要业务补几个专用角色。"
	case ws == "":
		it.Status = ReadyWarn
		it.Summary = "工作区未设置：Agent 看不到你的业务文件，上下文只能靠对话里说"
		it.Fix = "在设置里指定工作区目录（业务项目根目录），文件工具与门控都以此为边界。"
	default:
		it.Status = ReadyPass
		it.Summary = fmt.Sprintf("有业务上下文：%d 个场景模板 + 工作区已设置 + %d 个空间隔离", len(roles), spaces)
	}
	return it
}

// ---------- 坑 7：模型调用缓存不可控 ----------

// checkCacheControl 坑的原话是"很难充分复用服务端的缓存机制来提高缓存率"。
// 提示词前缀缓存要求**逐字节相同的最长公共前缀**，所以这一项的根子是提示词布局：
// 稳定段必须在易变段之前。这里用两个不同目标 + 两个不同工作目录实测，
// 让布局退化能被回归发现。
func (a *Agent) checkCacheControl() ReadinessItem {
	// 没有注册表就构建不出提示词，这一项无从实测。
	// 要如实说"测不了"，而不是把它算成"布局退化"——归因错了比不报更糟。
	if a.Reg == nil {
		return ReadinessItem{
			Status:  ReadyFail,
			Summary: "无法实测提示词布局：工具注册表未装配，构建不出系统提示词",
			Fix:     "先装配工具注册表（第 1 项），这一项才有意义。",
		}
	}

	stable, shortest, lenA, lenB, layoutOK := a.measurePromptPrefix()
	ratio := 0.0
	if shortest > 0 {
		ratio = float64(stable) / float64(shortest)
	}
	u := a.aggregateUsage()

	ev := []string{
		fmt.Sprintf("提示词公共前缀实测：%d 字符 / 较短提示词的 %.0f%%", stable, ratio*100),
		fmt.Sprintf("实测样本：同角色、不同目标 + 不同工作目录的系统提示词（%d / %d 字符）", lenA, lenB),
	}
	if layoutOK {
		ev = append(ev, "布局校验：稳定段（身份 / 输出契约 / 能力菜单 / 角色知识）全部落在公共前缀之内")
	} else {
		ev = append(ev, "布局校验：稳定段被易变内容打断，其后的内容无法参与缓存")
	}
	ev = append(ev, "布局约束：稳定段在前，易变段（上下文 / 可用工具 schema）在后")
	if u.LLMCalls > 0 && u.CachedCalls > 0 {
		ev = append(ev, fmt.Sprintf("累计缓存命中：%d / %d 次调用报告命中，命中输入 token %d（命中率 %.0f%%）",
			u.CachedCalls, u.LLMCalls, u.CachedTokens, u.CacheHitRate()*100))
	} else if u.LLMCalls > 0 {
		ev = append(ev, fmt.Sprintf("累计缓存命中：%d 次调用均未报告缓存字段（厂商未返回，或缓存尚未生效）", u.LLMCalls))
	} else {
		ev = append(ev, "累计缓存命中：暂无调用记录")
	}

	it := ReadinessItem{Evidence: ev}
	switch cacheStatus(layoutOK, stable, ratio) {
	case ReadyFail:
		it.Status = ReadyFail
		it.Summary = "提示词布局退化：稳定段被易变内容打断，服务端缓存从断点往后全部作废"
		it.Fix = "把随目标变化的内容（上下文、可用工具 schema）移到提示词末尾，稳定段放前面。"
	case ReadyWarn:
		it.Status = ReadyWarn
		if stable < llm.CacheFriendlyMinChars {
			it.Summary = fmt.Sprintf("公共前缀只有 %d 字符，低于值得缓存的量级（%d）", stable, llm.CacheFriendlyMinChars)
			it.Fix = "让稳定段更充实（身份 + 输出契约 + 能力菜单 + 角色知识），缓存才有复用价值。"
		} else {
			it.Summary = fmt.Sprintf("公共前缀 %d 字符可缓存，但只覆盖了较短提示词的 %.0f%%：易变段占比过高", stable, ratio*100)
			it.Fix = "检查易变段里有没有本可以稳定的内容（如固定的工具 schema 顺序）。"
		}
	default:
		it.Status = ReadyPass
		it.Summary = fmt.Sprintf("提示词布局缓存友好：稳定段 %d 字符落在公共前缀内（占较短提示词的 %.0f%%）", stable, ratio*100)
	}
	return it
}

const (
	// cacheCoverageWarn 公共前缀占较短提示词的比例下限。
	// 低于它说明请求里大部分内容都是一次性的，缓存省不下多少——
	// 但这只是收益问题，不是机制失效，所以是 warn。
	cacheCoverageWarn = 0.25
)

// stablePromptMarkers 稳定段的标志性小节标题。
// 它们必须全部落在公共前缀之内：只要有一个落到公共前缀之外，
// 就说明它前面被塞进了易变内容，缓存从那里往后全部作废。
//
// 注意这里**不含**末尾的输出提醒——它是刻意放在易变段之后的，
// 为的是让"只输出 JSON"这条约束离生成点更近。
var stablePromptMarkers = []string{
	"## 输出格式",
	"## 规划规则",
	"## 验收标准",
	"## 能力菜单",
	"## 定时/周期任务",
	"## 专家角色：",
}

// cacheStatus 判定提示词缓存的健康状况。
//
// 判定顺序是有意为之，也修正过一个错判：先看**布局对不对**（这才是"缓存不可控"的根因），
// 再看稳定段**够不够大**（决定值不值得缓存），最后才看**占比**（决定省得多不多）。
//
// 早先只看占比，结果 21 个真实工具时稳定段 2448 字符、布局完全正确，
// 却因为按目标筛出的工具 schema 尾巴很长而被判 warn——占比低不等于布局坏。
func cacheStatus(layoutOK bool, stable int, ratio float64) ReadinessStatus {
	switch {
	case !layoutOK:
		return ReadyFail
	case stable < llm.CacheFriendlyMinChars:
		return ReadyWarn
	case ratio < cacheCoverageWarn:
		return ReadyWarn
	default:
		return ReadyPass
	}
}

// layoutCoversStable 判断公共前缀是否覆盖了全部稳定段。
func layoutCoversStable(prefix, prompt string) bool {
	for _, m := range stablePromptMarkers {
		if strings.Contains(prompt, m) && !strings.Contains(prefix, m) {
			return false
		}
	}
	return true
}

// measurePromptPrefix 实测系统提示词的公共前缀。
//
// 两个样本刻意同时变两样东西：目标（真实使用中最常见的变化）
// 和工作目录（换了项目就会变）。只变目标的话，如果哪天上游把工作目录
// 挪到了提示词前部，两份样本的工作目录相同、公共前缀照样能穿过去，
// 这个检查就会漏掉——同时变两样才拦得住。
//
// 返回：公共前缀字符数、较短提示词长度、两份提示词各自长度、布局是否覆盖完整稳定段。
func (a *Agent) measurePromptPrefix() (stable, shortest, lenA, lenB int, layoutOK bool) {
	if a.Reg == nil {
		return 0, 0, 0, 0, false
	}
	role := "general"
	build := func(goal, cwd string) string {
		p := &Planner{
			Reg:            a.Reg,
			MaxSteps:       a.Cfg.Agent.MaxSteps,
			Style:          a.Cfg.Persona.Style,
			TaskMode:       string(types.TaskWork),
			Role:           role,
			MaxToolSchemas: a.Cfg.Agent.MaxToolSchemas,
			RoleTools:      FindRole(role).Tools,
		}
		return p.buildSystemPrompt(goal, cwd, nil, nil, "", "")
	}
	cwdA := a.Cfg.Workspace
	pa := build("分析上季度销售数据，输出一份带结论的报告", cwdA)
	pb := build("重构登录模块的鉴权逻辑，并补充单元测试", filepath.Join(cwdA, "sub"))

	ra, rb := []rune(pa), []rune(pb)
	stable = llm.StablePrefixLen(pa, pb)
	shortest = len(ra)
	if len(rb) < shortest {
		shortest = len(rb)
	}
	if stable > len(ra) {
		stable = len(ra)
	}
	layoutOK = layoutCoversStable(string(ra[:stable]), pa)
	return stable, shortest, len(ra), len(rb), layoutOK
}

// aggregateUsage 汇总当前仍保留在内存中的全部任务用量。
// 已淘汰的任务不在其中，所以这是"近期样本"而非"历史总量"。
func (a *Agent) aggregateUsage() types.TaskUsage {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	var out types.TaskUsage
	for _, u := range a.usage {
		if u == nil {
			continue
		}
		out.LLMCalls += u.LLMCalls
		out.PromptTokens += u.PromptTokens
		out.CompletionTokens += u.CompletionTokens
		out.CachedTokens += u.CachedTokens
		out.CachedCalls += u.CachedCalls
		out.EstimatedCalls += u.EstimatedCalls
	}
	return out
}

// ---------- 坑 8：缺少健康迭代的简单架构 ----------

// checkIterationArch 持续迭代的前提是"看得见自己跑得怎么样"。
// 成长日志把每次任务的评分、耗时、token、技能使用都记下来，健康度才有依据。
func (a *Agent) checkIterationArch() ReadinessItem {
	if a.Growth == nil {
		return ReadinessItem{
			Status:  ReadyFail,
			Summary: "没有成长日志：跑得好不好无从判断，也就没有迭代方向",
			Fix:     "启用成长日志，让每次任务的结果留下可对比的记录。",
		}
	}
	st := a.Growth.Stats()
	ev := []string{
		fmt.Sprintf("完成任务：%d 次", st.TotalTasks),
		fmt.Sprintf("平均评分：%.1f", st.AvgScore),
		fmt.Sprintf("连续活跃：%d 天", st.RecentStreak),
		fmt.Sprintf("累计模型调用：%d 次 / token %d（近 7 天 %d）", st.TotalLLMCalls, st.TotalTokens, st.WeekTokens),
		fmt.Sprintf("等级：%s（进度 %.0f%%）", st.Level, st.LevelProgress*100),
	}

	it := ReadinessItem{Evidence: ev}
	switch {
	case st.TotalTasks == 0:
		it.Status = ReadyWarn
		it.Summary = "成长日志已就绪，但还没有任务历史，健康度暂无法评估"
		it.Fix = "跑几个真实任务后回来看这一项。"
	case st.AvgScore > 0 && st.AvgScore < 60:
		it.Status = ReadyWarn
		it.Summary = fmt.Sprintf("平均评分 %.1f 偏低：交付质量还不稳定", st.AvgScore)
		it.Fix = "看看评分低的任务卡在哪：验收标准写不清，还是工具能力不够。"
	default:
		it.Status = ReadyPass
		it.Summary = fmt.Sprintf("有健康数据可迭代：%d 次任务、平均评分 %.1f、连续活跃 %d 天", st.TotalTasks, st.AvgScore, st.RecentStreak)
	}
	return it
}

// ---------- 坑 9：可扩展架构与压缩记忆 ----------

// checkExtensibleMemory 长任务撑不撑得住，取决于记忆是不是"压缩"的——
// 超出窗口的对话被摘要而不是丢弃，上下文才不会随任务长度线性膨胀。
func (a *Agent) checkExtensibleMemory() ReadinessItem {
	if a.Mem == nil {
		return ReadinessItem{
			Status:  ReadyFail,
			Summary: "没有记忆系统：长任务一中断就接不上",
			Fix:     "启用短期对话 + 长期记忆，否则跨轮次与跨会话都没有延续性。",
		}
	}
	st := a.Mem.Stats()
	items := a.Mem.Long.Count()
	skills := 0
	if a.Skills != nil {
		skills = len(a.Skills.List())
	}
	compress := "关闭"
	if a.Cfg.Agent.ContextCompress {
		compress = "开启"
	}

	ev := []string{
		fmt.Sprintf("长期记忆：%d 条（上限 %d）", items, a.Cfg.Memory.MaxItems),
		fmt.Sprintf("短期窗口：%d / %d 轮（溢出 %d 轮待压缩）", st.ShortTurns, st.ShortCap, st.Overflow),
		fmt.Sprintf("上下文压缩：%s（累计节省约 %d token）", compress, st.SavedTokens),
		fmt.Sprintf("扩展点：MCP 服务器 %d 个；技能 %d 个；模型档位 %d 档", len(a.Cfg.MCP), skills, len(a.Cfg.LLM.Tiers)),
		"压缩机制：超出窗口的对话被摘要进滚动上下文，而不是直接丢弃",
	}

	// 状态占比实测（站点判据：状态段 > 60% 即上下文膨胀）。
	// 用一份合成的满载样本（6 轮对话 + 满额摘要 + 3 条记忆命中）测提示词四成分的占比——
	// 只记总字符数时这类膨胀无从定位，分段才能回答"是哪一段在涨"。
	// 计量单位是字节（b.Len()），中文一段顶三倍字节，判据线按字节校准：
	// 占比 > 60% **且** 总量 > 20000 字节（≈ 六七千汉字）。只看占比会在小注册表上
	// 误报——工具少时能力段天然小，状态占比必然高，但两千来字节谈不上"淹没"；
	// 60% 这条线是给有分量的提示词准备的。它真正的守卫对象是注入上限
	// （Recent 轮数、摘要上限、每轮截断）被调大后没有人重新看过占比——
	// 那时总量与占比会同时越过线。
	bd := a.sampleStateShare()
	ev = append(ev, fmt.Sprintf("状态占比实测：满载样本下状态段 %d / 提示词总 %d 字节（%.0f%%，判据线 60%% 且总量 > 20000 字节）",
		bd.State, bd.Total, bd.StateShare()*100))
	it := ReadinessItem{Evidence: ev}
	switch {
	case !a.Cfg.Agent.ContextCompress:
		it.Status = ReadyWarn
		it.Summary = "上下文压缩关闭：长任务会撑爆窗口，或把早期对话整段丢掉"
		it.Fix = "打开设置里的 agent.context_compress。"
	case bd.StateShare() > 0.60 && bd.Total > 20000:
		it.Status = ReadyWarn
		it.Summary = fmt.Sprintf("状态段占比 %.0f%%（> 60%% 判据线）：对话与摘要正在淹没指令与能力声明", bd.StateShare()*100)
		it.Fix = "调低短期窗口轮数、缩短摘要上限，或把每轮注入的对话截断长度调小。"
	default:
		it.Status = ReadyPass
		it.Summary = fmt.Sprintf("记忆是压缩的：长期 %d 条、短期窗口 %d 轮、满载样本状态占比 %.0f%%",
			items, st.ShortCap, bd.StateShare()*100)
	}
	return it
}

// sampleStateShare 用合成的满载状态样本实测提示词分段。
// 样本按注入上限取满（每轮取截断上限、摘要取上限、记忆取满命中数）——
// 膨胀总发生在满载时，空载样本只会给出一个永远健康的假数字。
// 返回完整 breakdown：占比之外，总量本身也是判据的一部分。
func (a *Agent) sampleStateShare() types.ContextBreakdown {
	if a.Reg == nil {
		return types.ContextBreakdown{}
	}
	p := &Planner{
		Reg:            a.Reg,
		MaxSteps:       a.Cfg.Agent.MaxSteps,
		Style:          a.Cfg.Persona.Style,
		TaskMode:       string(types.TaskWork),
		Role:           "general",
		MaxToolSchemas: a.Cfg.Agent.MaxToolSchemas,
		RoleTools:      FindRole("general").Tools,
	}
	recent := make([]memory.Turn, 0, recentTurnsInjected)
	for i := 0; i < recentTurnsInjected; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		// 每轮取截断上限：注入的对话都会被 Shorten 到这个长度
		recent = append(recent, memory.Turn{Role: role, Content: strings.Repeat("这", turnContentCap)})
	}
	summary := strings.Repeat("这", summaryInjectCap)
	relevant := make([]memory.Hit, 0, memoryHitsInjected)
	for i := 0; i < memoryHitsInjected; i++ {
		relevant = append(relevant, memory.Hit{Content: strings.Repeat("这", memoryHitContentCap)})
	}
	_, bd := p.buildSystemPromptMeasured("整理下载目录并汇总本周新增文件", a.Cfg.Workspace, recent, relevant, summary, "")
	return bd
}

// SummaryLine 一行摘要，供 CLI 与日志使用。
func (r ReadinessReport) SummaryLine() string {
	return fmt.Sprintf("就绪体检 %d/%d 通过，%d 项待改进，%d 项不合格（%s）",
		r.Passed, r.Total, r.Warned, r.Failed, r.Verdict)
}
