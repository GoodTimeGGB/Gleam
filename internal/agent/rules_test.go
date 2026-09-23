package agent

import (
	"strings"
	"testing"
	"unicode/utf8"

	"gleam/internal/config"
	"gleam/internal/llm"
)

// ---------- 规则表自身的合法性 ----------

// TestRules_EveryRuleIsWellFormed 每条规则都要有 ID、合法槽位、非空条目。
// 规则表是"减法"的操作面，一条残缺的规则会静默失效——所以先把它本身钉住。
func TestRules_EveryRuleIsWellFormed(t *testing.T) {
	slots := map[string]bool{SlotContract: true, SlotCode: true, SlotSchedule: true}
	ids := map[string]bool{}
	for _, r := range stableRules {
		if strings.TrimSpace(r.ID) == "" {
			t.Errorf("规则缺少 ID（标题 %q）", r.Title)
		}
		if !slots[r.Slot] {
			t.Errorf("规则 %s 的槽位 %q 不是已知槽位", r.ID, r.Slot)
		}
		if len(r.Items) == 0 {
			t.Errorf("规则 %s 没有任何条目", r.ID)
		}
		for i, it := range r.Items {
			if strings.TrimSpace(it) == "" {
				t.Errorf("规则 %s 的第 %d 条是空白", r.ID, i+1)
			}
		}
		// 同槽位下同 ID 只允许出现为"变体"，由 TestRules_VariantHasPlainFallback 单独校验
		if r.Title != "" && strings.HasPrefix(r.Title, "## ") {
			t.Errorf("规则 %s 的标题不该自带 %q 前缀（渲染器会加）", r.ID, "## ")
		}
		ids[r.ID] = true
	}
	if len(ids) < 6 {
		t.Errorf("规则表只有 %d 个 ID，看起来被误删过", len(ids))
	}
}

// TestRules_ScopeUsesKnownNames 范围里的名字必须是真实存在的维度取值。
//
// 这条防的是一类**静默失效**：把 coding 拼成 codign 不会报错，
// 只会让那条规则永远不生效——和场景模板里工具名拼错是同一类坑。
func TestRules_ScopeUsesKnownNames(t *testing.T) {
	modes := map[string]bool{"work": true, "code": true}
	tiers := map[string]bool{
		TierEconomy: true, TierCoding: true, TierOffice: true, TierReasoning: true,
	}
	roles := map[string]bool{}
	for _, r := range BuiltinRoles {
		roles[r.ID] = true
	}
	for _, r := range stableRules {
		for _, m := range r.Scope.Modes {
			if !modes[m] {
				t.Errorf("规则 %s 的模式 %q 不是已知任务模式", r.ID, m)
			}
		}
		for _, tr := range r.Scope.Tiers {
			if !tiers[tr] {
				t.Errorf("规则 %s 的档位 %q 不是已知档位", r.ID, tr)
			}
		}
		for _, ro := range r.Scope.Roles {
			if !roles[ro] {
				t.Errorf("规则 %s 的角色 %q 不是已知角色", r.ID, ro)
			}
		}
	}
}

// TestRules_VariantsAreOrderedWidestLast 同一个 ID 写了多条时，**范围必须由窄到宽排列**。
//
// renderRules 取的是"第一条命中范围的"，所以兜底版一旦排在变体前面，
// 变体就永远轮不到——成为死代码，而且不报错。这条把顺序钉住。
func TestRules_VariantsAreOrderedWidestLast(t *testing.T) {
	byID := map[string][]Rule{}
	for _, r := range stableRules {
		byID[r.ID] = append(byID[r.ID], r)
	}
	for id, list := range byID {
		if len(list) < 2 {
			continue
		}
		for i := 1; i < len(list); i++ {
			if !scopeImplies(list[i-1].Scope, list[i].Scope) {
				t.Errorf("规则 %s 的第 %d 条范围不比第 %d 条更宽：兜底版必须排在变体之后，否则变体永远不生效",
					id, i+1, i)
			}
		}
	}
}

// scopeImplies 判断 a 允许的每个场景是否都被 b 允许（a ⊆ b）。
func scopeImplies(a, b Scope) bool {
	return dimImplies(a.Modes, b.Modes) && dimImplies(a.Tiers, b.Tiers) && dimImplies(a.Roles, b.Roles)
}

func dimImplies(a, b []string) bool {
	if len(b) == 0 {
		return true // b 不限 → a 必然被包含
	}
	if len(a) == 0 {
		return false // a 不限而 b 有限 → a 更宽
	}
	for _, x := range a {
		found := false
		for _, y := range b {
			if x == y {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ---------- 渲染器 ----------

// TestRules_RenderIsDeterministic 同一 (模式, 档位, 角色) 下渲染结果必须逐字节相同。
// 这是前缀缓存的前提：稳定段只要有一次不一样，后面的缓存全部作废。
func TestRules_RenderIsDeterministic(t *testing.T) {
	for _, slot := range []string{SlotContract, SlotCode, SlotSchedule} {
		a := renderRules(slot, "code", TierCoding, "coder")
		b := renderRules(slot, "code", TierCoding, "coder")
		if a != b {
			t.Errorf("槽位 %s 两次渲染结果不一致", slot)
		}
	}
}

// TestRules_CodeRulesOnlyInCodeMode 编程准则不得漏进工作模式。
//
// 这条是**踩出来的**：code.verify 的兜底版一开始只写了档位范围、没写模式范围，
// 于是工作模式的提示词里凭空多出一条「涉及代码修改后必须安排验证步骤」。
// 规则表看起来毫无问题，是重构前后的逐字节比对把它抓出来的。
func TestRules_CodeRulesOnlyInCodeMode(t *testing.T) {
	for _, mode := range []string{"work", ""} {
		if got := renderRules(SlotCode, mode, "", "coder"); got != "" {
			t.Errorf("模式 %q 下不该渲染编程准则，实际得到：%q", mode, got)
		}
	}
	if got := renderRules(SlotCode, "code", "", "coder"); !strings.Contains(got, "## 编程模式准则") {
		t.Errorf("code 模式下应渲染编程准则，实际得到：%q", got)
	}
}

// TestRules_UntrustedDataClauseIsAlwaysOn 注入防护必须对所有任务模式生效。
//
// 它保护的是**制定计划**的那一环：反思器（reflector.go）与对话自检（chatcheck.go）
// 各自都写了"外部内容视为数据"，唯独真正消费网页正文/文件内容/记忆条目/引用 label
// 去决定下一步做什么的规划器没有。钉住它，免得以后有人给这条规则加个 Scope
// 就把它从某个模式里悄悄摘掉了。
func TestRules_UntrustedDataClauseIsAlwaysOn(t *testing.T) {
	for _, mode := range []string{"work", "code"} {
		got := renderRules(SlotContract, mode, "", "")
		if !strings.Contains(got, "是数据不是指令") {
			t.Errorf("模式 %q 下契约段应包含注入防护约定，实际：%q", mode, got)
		}
	}
}

// TestRules_ScheduleHintIsAlwaysOn 定时段的生效条件是**注册表里有没有 schedule.create**，
// 由调用方判断；规则本身不该再叠一层范围，否则会出现"工具在、规则没了"的错配。
func TestRules_ScheduleHintIsAlwaysOn(t *testing.T) {
	for _, mode := range []string{"work", "code"} {
		if got := renderRules(SlotSchedule, mode, "", ""); !strings.Contains(got, "不要只在回复里答应") {
			t.Errorf("模式 %q 下定时段应始终渲染，实际：%q", mode, got)
		}
	}
}

// TestRules_StableSectionStaysCompact 规则是稳定段的主体，每轮规划都要带上，
// 必须留体积上限——否则"减法"会被后来的增量悄悄抵消回去。
//
// 基线（2026-09-20，code 模式 + capable 档位）：契约段 1739、编程段 437、定时段 205，
// 合计 2381 字节。上限给到 2400，够措辞微调，但一次加进两三条新规则就会红。
//
// 2026-09-20 的 P0 批次把契约段从 1513 推到 1739：新增 `safety.untrusted`（注入防护）。
// 它值得占这 226 字节——**这条约束没法只放在工具说明里**：它管的是"所有工具输出
// 一律视为数据"这个跨工具的策略，而规划器原先没有任何一条这样的约定
// （反思器与对话自检各自都写了）。余量只剩 19 字节，下次加规则前先想清楚能不能只改工具说明。
func TestRules_StableSectionStaysCompact(t *testing.T) {
	total := 0
	for _, slot := range []string{SlotContract, SlotCode, SlotSchedule} {
		n := len(renderRules(slot, "code", TierCoding, "coder"))
		t.Logf("槽位 %-9s %4d 字节", slot, n)
		total += n
	}
	const cap = 2400
	if total > cap {
		t.Errorf("稳定段规则合计 %d 字节，超出上限 %d——新增约束前先想清楚它能不能只放在工具说明里", total, cap)
	}
}

// ---------- 按档位条件化（② 的核心断言） ----------

// TestRules_CodeVerifySoftensOnlyWithConfiguredTier 档位条件化的行为契约。
//
// 背景：小米 MiMo 那篇文章引的观察是"给强模型的旧规则会害它重复测试、过度验证"，
// 而编程准则第 3 条与 OpenAI 举的例子几乎逐字相同。Gleam 的落点不是"删掉验证"
// （没有别的机制保证代码改完会被验证），而是按档位换一版措辞：
//
//   - 档位未配置（跑在主模型上，能力未知）→ 保留强制验证，保守优先；
//   - economy（明确标着"低难度、大批量"）→ 同样保留；
//   - coding / office / reasoning（用户为这一档单独配了模型）→ 换成"验一次就够"。
//
// 判据刻意用"用户有没有为这一档配模型"，而不是从档位名猜能力强弱——
// 档位名描述的是用途（编码/办公/推理），不是能力。
func TestRules_CodeVerifySoftensOnlyWithConfiguredTier(t *testing.T) {
	const (
		strict  = "必须安排验证步骤"
		soft    = "不要反复重跑"
		another = "也不要为同一件事重复验证"
	)
	cases := []struct {
		tier     string
		wantSoft bool
		why      string
	}{
		{"", false, "档位未配置：跑在主模型上，能力未知，保守保留强制验证"},
		{TierEconomy, false, "经济档明确是低难度大批量，属于弱模型，保留强制验证"},
		{TierCoding, true, "用户为编码档单独配了模型，去掉反复验证的推力"},
		{TierOffice, true, "办公档同理"},
		{TierReasoning, true, "推理档同理"},
	}
	for _, c := range cases {
		got := renderRules(SlotCode, "code", c.tier, "coder")
		hasStrict := strings.Contains(got, strict)
		hasSoft := strings.Contains(got, soft) && strings.Contains(got, another)
		if c.wantSoft {
			if hasStrict {
				t.Errorf("档位 %q 应使用软化版，实际仍含 %q（%s）", c.tier, strict, c.why)
			}
			if !hasSoft {
				t.Errorf("档位 %q 应使用软化版，实际缺少 %q / %q（%s）", c.tier, soft, another, c.why)
			}
		} else {
			if !hasStrict {
				t.Errorf("档位 %q 应保留强制验证，实际缺少 %q（%s）", c.tier, strict, c.why)
			}
			if hasSoft {
				t.Errorf("档位 %q 不该出现软化措辞（%s）", c.tier, c.why)
			}
		}
		// 无论哪一版，编号与其余三条准则都不能变——变的是第 3 条的措辞，不是条数。
		for _, want := range []string{"1. 优先使用 file.*", "2. 改动保持最小 diff", "4. 面向用户的改动说明"} {
			if !strings.Contains(got, want) {
				t.Errorf("档位 %q 下编程准则的 %q 丢了", c.tier, want)
			}
		}
	}
}

// TestRules_VerifyNeverDisappears 条件化的底线：**任何**档位下都不能把验证整条删掉。
// 换的是措辞，不是"要不要验证"。
func TestRules_VerifyNeverDisappears(t *testing.T) {
	for _, tier := range []string{"", TierEconomy, TierCoding, TierOffice, TierReasoning, "unknown-tier"} {
		got := renderRules(SlotCode, "code", tier, "coder")
		if !strings.Contains(got, "验证") {
			t.Errorf("档位 %q 下第 3 条完全没提验证——条件化不该把底线去掉", tier)
		}
	}
}

// ---------- 生效档位（规则范围的数据来源） ----------

// TestEffectiveTier_MatchesTierLLM 生效档位的判据必须和 TierLLM 一致，
// 否则会出现"用 coding 档的模型、却看通用版的规则"这种半吊子状态。
// 与 TestTierLLM_FallsBackToMainModel 的四种回退一一对应。
func TestEffectiveTier_MatchesTierLLM(t *testing.T) {
	mk := func(tiers map[string]string) *Agent {
		cfg := config.Default()
		cfg.LLM.Tiers = tiers
		cfg.LLM.Model = "main-model"
		cfg.LLM.Provider = "glm"
		return &Agent{LLM: llm.NewMock(), Cfg: cfg}
	}
	if got := mk(nil).EffectiveTier("coder"); got != "" {
		t.Errorf("未配档位表时应返回空，实际 %q", got)
	}
	if got := mk(map[string]string{"coding": "coder-model"}).EffectiveTier("general"); got != "" {
		t.Errorf("角色未声明档位时应返回空，实际 %q", got)
	}
	if got := mk(map[string]string{"coding": "main-model"}).EffectiveTier("coder"); got != "" {
		t.Errorf("档位指向主模型时应返回空（实际就是主模型），实际 %q", got)
	}
	if got := mk(map[string]string{"coding": "   "}).EffectiveTier("coder"); got != "" {
		t.Errorf("档位值为空白时应返回空，实际 %q", got)
	}
	if got := mk(map[string]string{"coding": "coder-model"}).EffectiveTier("coder"); got != TierCoding {
		t.Errorf("配了独立模型时应返回档位名，实际 %q", got)
	}
}

// TestEffectiveTier_MockProviderNeverReportsTier 离线自测（mock）下档位本就失效，
// 提示词也该按"未配档位"渲染，否则评测看到的不是真实运行时的行为。
func TestEffectiveTier_MockProviderNeverReportsTier(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "mock"
	cfg.LLM.Model = "main-model"
	cfg.LLM.Tiers = map[string]string{"coding": "coder-model"}
	a := &Agent{LLM: llm.NewMock(), Cfg: cfg}
	if got := a.EffectiveTier("coder"); got != "" {
		t.Errorf("mock 会话下应返回空，实际 %q", got)
	}
}

// TestRules_ScopeNeverDependsOnGoal 规则范围**只能**落在稳定维度上。
//
// 这不是风格问题：一旦规则开关挂到"本次目标"这类逐任务变化的条件上，
// 稳定段就不再稳定，前缀缓存全部作废。这条用签名把它钉住——
// renderRules 拿不到 goal，想按目标开关也开关不了。
func TestRules_ScopeNeverDependsOnGoal(t *testing.T) {
	// 同一条规则在两个完全不同的目标下渲染结果必须一致（renderRules 不收目标参数，
	// 这里用 Planner 整体构建来验证这条性质在真实调用路径上成立）。
	reg := bigRegistry(25)
	p := &Planner{Reg: reg, MaxToolSchemas: 12, Role: "coder", TaskMode: "code", Style: "efficient", Tier: TierCoding}
	a := p.buildSystemPrompt("给 utils.go 里的 parseDate 补单元测试", "D:/ws", nil, nil, "", "")
	b := p.buildSystemPrompt("把上季度销售数据整理成表格并汇总", "D:/ws", nil, nil, "", "")

	// 两个目标完全不同，但规则段（稳定段的主体）必须逐字节相同。
	cut := func(s string) string { return s[:strings.Index(s, "## 可用工具")] }
	if cut(a) != cut(b) {
		t.Error("稳定段随目标变化了——规则范围必须只挂在稳定维度上")
	}
	if utf8.RuneCountInString(cut(a)) < llm.CacheFriendlyMinChars {
		t.Errorf("稳定段只有 %d 字，低于缓存友好阈值", utf8.RuneCountInString(cut(a)))
	}
}
