package eval

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gleam/internal/agent"
)

// ---------- judge：深度门控 ----------

// TestJudgeIgnoresPlanChecksAtSelectDepth 是评测可信度的地基。
//
// select 深度没有计划，拿步数与验收标准去判它，会让**每一条** select 用例都失败，
// 评测就成了一条永远红的噪音带——而人一旦习惯红，门禁就废了。
func TestJudgeIgnoresPlanChecksAtSelectDepth(t *testing.T) {
	c := Case{
		ID: "x", Goal: "g",
		WantTools:      []string{"file.write"},
		MinSteps:       3,
		MaxSteps:       5,
		WantAcceptance: true,
		WantStatus:     []string{"success"},
	}
	obs := observation{Tools: []string{"file.write"}, Steps: 0, Acceptance: 0, Status: ""}

	checks := judge(c, obs, DepthSelect)
	for _, ck := range checks {
		if strings.Contains(ck.Name, "步数") || strings.Contains(ck.Name, "验收") ||
			strings.Contains(ck.Name, "状态") {
			t.Fatalf("select 深度不该判计划类断言，却出现了 %q", ck.Name)
		}
	}
	if !allPassed(checks) {
		t.Fatalf("select 深度下这条应当通过，实际 checks=%+v", checks)
	}

	// 同一份观测放到 plan 深度，计划类断言必须全部生效并判红。
	planChecks := judge(c, obs, DepthPlan)
	if allPassed(planChecks) {
		t.Fatal("plan 深度下步数/验收标准都不满足，却判通过了——断言没生效")
	}
}

// ---------- judge：前 N 名窗口 ----------

// TestJudgeSelectUsesTopNWindow 守住"长尾不算数"这条口径。
//
// 本地关键词打分是兜底路径，长尾全是二元组偶然撞词。一个工具排在 21 个里的第 11 名
// 对行为毫无影响，判它"误路由"只会让评测永远红。窗口外的名次必须放行。
func TestJudgeSelectUsesTopNWindow(t *testing.T) {
	c := Case{
		ID: "x", Goal: "g",
		WantTools:    []string{"file.list"},
		NotWantTools: []string{"schedule.create"},
		TopN:         3,
	}
	// file.list 第 1 名；schedule.create 第 11 名 —— 窗口外，应放行。
	obs := observation{Tools: []string{
		"file.list", "file.read", "file.write", "file.move",
		"file.delete", "file.mkdir", "web.fetch", "memory.save",
		"skill.list", "reply", "schedule.create",
	}}
	if !allPassed(judge(c, obs, DepthSelect)) {
		t.Fatalf("窗口外的名次不该判红，实际 checks=%+v", judge(c, obs, DepthSelect))
	}

	// 把 schedule.create 提到窗口内 —— 必须判红，且详情要指出名次。
	obs.Tools = []string{"file.list", "schedule.create", "file.read"}
	checks := judge(c, obs, DepthSelect)
	if allPassed(checks) {
		t.Fatal("误路由工具进了窗口却没判红")
	}
	var detail string
	for _, ck := range checks {
		if !ck.Passed && strings.Contains(ck.Name, "schedule.create") {
			detail = ck.Detail
		}
	}
	if !strings.Contains(detail, "第 2 名") {
		t.Fatalf("失败详情该指出实际名次，实际为 %q", detail)
	}
}

// TestJudgeDistinguishesRankedOutFromMissing 守住排障信息的可用性：
// "排到窗口外"和"压根没命中"是两种完全不同的毛病（排序问题 vs 描述没写清楚），
// 报告里必须分得开，否则看报告的人还得回去手工复现。
func TestJudgeDistinguishesRankedOutFromMissing(t *testing.T) {
	c := Case{ID: "x", Goal: "g", WantTools: []string{"file.write"}, TopN: 1}

	rankedOut := judge(c, observation{Tools: []string{"file.read", "file.write"}}, DepthSelect)
	if !strings.Contains(rankedOut[1].Detail, "落在前 1 名之外") {
		t.Fatalf("排到窗口外应说明是名次问题，实际 %q", rankedOut[1].Detail)
	}

	missing := judge(c, observation{Tools: []string{"file.read"}}, DepthSelect)
	if !strings.Contains(missing[1].Detail, "完全没命中") {
		t.Fatalf("没命中应说明是没命中，实际 %q", missing[1].Detail)
	}
}

// TestJudgeTopNDefault 确认没写 top_n 时用的是 DefaultTopN。
func TestJudgeTopNDefault(t *testing.T) {
	c := Case{ID: "x", Goal: "g", NotWantTools: []string{"bad"}}
	// 正好排在 DefaultTopN 名，进窗口 → 判红。
	obs := observation{Tools: []string{"a", "b", "c", "d", "bad"}}
	if allPassed(judge(c, obs, DepthSelect)) {
		t.Fatalf("第 %d 名应落在默认窗口内", DefaultTopN)
	}
	// 再往后一名 → 出窗口 → 放行。
	obs.Tools = []string{"a", "b", "c", "d", "e", "bad"}
	if !allPassed(judge(c, obs, DepthSelect)) {
		t.Fatalf("第 %d 名应落在默认窗口外", DefaultTopN+1)
	}
}

// TestJudgePlanDepthDoesNotWindow 确认窗口只对 select 深度生效：
// plan/full 的集合是模型真的挑出来的，每一项都算数，不能被截断放行。
func TestJudgePlanDepthDoesNotWindow(t *testing.T) {
	c := Case{ID: "x", Goal: "g", NotWantTools: []string{"bad"}, TopN: 1}
	obs := observation{Tools: []string{"a", "b", "bad"}, Steps: 1, Acceptance: 1}
	if allPassed(judge(c, obs, DepthPlan)) {
		t.Fatal("plan 深度不该按前 N 名截断——第 3 名也是模型真的选出来的")
	}
}

// ---------- 用例集校验 ----------

func TestValidateRejectsVacuousCase(t *testing.T) {
	// 没有任何期望的用例**永远会通过**，却让通过率看起来不错——评测最容易被这样架空。
	err := Validate([]Case{{ID: "empty", Goal: "随便做点什么"}})
	if err == nil || !strings.Contains(err.Error(), "没有任何期望") {
		t.Fatalf("应当拒绝没有期望的用例，实际 err=%v", err)
	}
}

func TestValidateRejectsBadCases(t *testing.T) {
	cases := map[string][]Case{
		"缺 id":   {{Goal: "g", WantTools: []string{"a"}}},
		"缺 goal": {{ID: "a", WantTools: []string{"x"}}},
		"id 重复":  {{ID: "a", Goal: "g", WantTools: []string{"x"}}, {ID: "a", Goal: "g", WantTools: []string{"y"}}},
		"步数区间颠倒": {{ID: "a", Goal: "g", MinSteps: 5, MaxSteps: 2}},
	}
	for name, cs := range cases {
		if err := Validate(cs); err == nil {
			t.Errorf("%s：应当报错，却通过了", name)
		}
	}
}

// TestBuiltinCasesAreValid 守住内置用例集本身：它是门禁的输入，
// 输入坏了评测就静默失效（比如某条用例被改成空期望后永远绿）。
func TestBuiltinCasesAreValid(t *testing.T) {
	cases, err := BuiltinCases()
	if err != nil {
		t.Fatalf("内置用例集不合法: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("内置用例集为空")
	}
	for _, c := range cases {
		// 报告靠 note 让红的用例自己解释自己，缺了就得回去翻文件。
		if strings.TrimSpace(c.Note) == "" {
			t.Errorf("用例 %s 缺 note——红的用例必须能自己解释自己", c.ID)
		}
		// 标了"已知问题"的，note 里必须写清楚根因，否则就成了"反正它红着"。
		if c.KnownIssue && !strings.Contains(c.Note, "已知问题") {
			t.Errorf("用例 %s 标了 known_issue，note 里却没说明根因", c.ID)
		}
	}
}

// ---------- 基线对比 ----------

func rep(depth Depth, passed map[string]bool) Report {
	r := Report{GeneratedAt: time.Now(), Depth: depth}
	for id, ok := range passed {
		r.Cases = append(r.Cases, CaseResult{ID: id, Passed: ok})
	}
	return r
}

func TestCompareDetectsRegression(t *testing.T) {
	base := rep(DepthSelect, map[string]bool{"a": true, "b": true, "c": false})
	now := rep(DepthSelect, map[string]bool{"a": true, "b": false, "c": true})

	d, err := Compare(now, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Broke) != 1 || d.Broke[0] != "b" {
		t.Fatalf("应当报出 b 回归，实际 %v", d.Broke)
	}
	if len(d.Fixed) != 1 || d.Fixed[0] != "c" {
		t.Fatalf("应当报出 c 修好，实际 %v", d.Fixed)
	}
	if !d.Regressed() {
		t.Fatal("有回归时 Regressed 必须为真——它决定退出码")
	}
}

// TestCompareRejectsDepthMismatch 守住"跨深度不比"这条：
// select 与 plan 的可判项不同，硬比会把"换了深度"误报成一片回归，那比不比更糟。
func TestCompareRejectsDepthMismatch(t *testing.T) {
	_, err := Compare(rep(DepthPlan, map[string]bool{"a": true}), rep(DepthSelect, map[string]bool{"a": true}))
	if err == nil || !strings.Contains(err.Error(), "深度不一致") {
		t.Fatalf("跨深度对比应当直接报错，实际 err=%v", err)
	}
}

// TestCompareRejectsTierMismatch 模拟档位会改变提示词（按档位条件化的规则），
// 所以"未模拟档位"的基线不能拿来比"模拟了 coding 档"的结果——
// 那会把规则措辞差异报成一片莫名其妙的回归。
func TestCompareRejectsTierMismatch(t *testing.T) {
	base := rep(DepthSelect, map[string]bool{"a": true})
	now := rep(DepthSelect, map[string]bool{"a": true})
	now.Tier = "coding"
	if _, err := Compare(now, base); err == nil || !strings.Contains(err.Error(), "模拟档位不一致") {
		t.Fatalf("档位不同时应当直接报错，实际 err=%v", err)
	}
	// 两边都是空（未模拟）时必须正常比——不能让默认路径也被这条拦住。
	if _, err := Compare(rep(DepthSelect, map[string]bool{"a": true}), base); err != nil {
		t.Fatalf("未模拟档位时不该报错，实际 err=%v", err)
	}
}

// TestRunnerTierReachesReport Runner 的模拟档位必须如实进报告，
// 否则基线里记不下它，上面那条跨档位拦截就无从判起。
func TestRunnerTierReachesReport(t *testing.T) {
	r := &Runner{Depth: DepthSelect, Tier: "reasoning"}
	rep := r.Run(context.Background(), nil)
	if rep.Tier != "reasoning" {
		t.Fatalf("报告应记下模拟档位，实际 %q", rep.Tier)
	}
	if (&Runner{Depth: DepthSelect}).Run(context.Background(), nil).Tier != "" {
		t.Fatal("未模拟时档位应为空")
	}
}

// TestCompareReportsRemovedCases 守住"删用例会让通过率虚高"这条：
// 基线里有、本次没跑的必须显式报出来。
func TestCompareReportsRemovedCases(t *testing.T) {
	base := rep(DepthSelect, map[string]bool{"a": true, "b": false})
	now := rep(DepthSelect, map[string]bool{"a": true})

	d, err := Compare(now, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Removed) != 1 || d.Removed[0] != "b" {
		t.Fatalf("应当报出 b 被移出用例集，实际 %v", d.Removed)
	}
}

// ---------- 规则集：这次评测是哪版规则跑出来的 ----------

// TestRunnerReportsRuleSet 报告必须带上规则集指纹，否则"这份 16/16 是哪一版规则
// 跑出来的"无从回答——基线存了通过率，却没存规则版本。
func TestRunnerReportsRuleSet(t *testing.T) {
	r := &Runner{Depth: DepthSelect}
	rep := r.Run(context.Background(), nil)
	if rep.RuleSet == "" {
		t.Fatal("报告里没有规则集指纹")
	}
	if rep.RuleSet != agent.RuleSetVersion() {
		t.Fatalf("报告里的指纹 %q 与当前规则表 %q 不一致——记的不是渲染用的那一份",
			rep.RuleSet, agent.RuleSetVersion())
	}
}

// TestCompareReportsRuleSetChangeButDoesNotFail 规则集变了要报出来，但**不能**卡门禁。
//
// 卡门禁是个陷阱：改规则是正常动作，一旦它让门禁变红，人的第一反应是摘掉 --strict，
// 门禁就整体失效了。所以它只出现在对比说明里——有回归时，第一条该怀疑的是规则改动。
func TestCompareReportsRuleSetChangeButDoesNotFail(t *testing.T) {
	base := rep(DepthSelect, map[string]bool{"a": true})
	base.RuleSet = "aaaaaaaaaa"
	now := rep(DepthSelect, map[string]bool{"a": true})
	now.RuleSet = "bbbbbbbbbb"

	d, err := Compare(now, base)
	if err != nil {
		t.Fatal(err)
	}
	if d.RuleSetFrom != "aaaaaaaaaa" || d.RuleSetTo != "bbbbbbbbbb" {
		t.Fatalf("规则集变化没记全：%q → %q", d.RuleSetFrom, d.RuleSetTo)
	}
	var line string
	for _, l := range d.Describe() {
		if strings.Contains(l, "规则集") {
			line = l
		}
	}
	if line == "" {
		t.Fatal("对比说明里没有规则集变化——回归时最该先看的线索丢了")
	}
	if !strings.Contains(line, "先怀疑规则改动") {
		t.Errorf("说明该指出归因方向，实际 %q", line)
	}
	if d.Regressed() {
		t.Fatal("规则集变了不该判成回归（会逼人摘掉 --strict）")
	}
}

// TestCompareRuleSetUnchangedStaysQuiet 同一版规则不必刷存在感——
// 每次都报"规则集未变"会淹没真正的变化。
func TestCompareRuleSetUnchangedStaysQuiet(t *testing.T) {
	base := rep(DepthSelect, map[string]bool{"a": true})
	base.RuleSet = "same123456"
	now := rep(DepthSelect, map[string]bool{"a": true})
	now.RuleSet = "same123456"
	d, err := Compare(now, base)
	if err != nil {
		t.Fatal(err)
	}
	if d.RuleSetFrom != "" || d.RuleSetTo != "" {
		t.Errorf("规则集没变却记了差异：%q → %q", d.RuleSetFrom, d.RuleSetTo)
	}
	for _, l := range d.Describe() {
		if strings.Contains(l, "规则集") {
			t.Errorf("规则集没变却报了：%q", l)
		}
	}
}

// TestCompareSkipsKnownIssues 守住门禁可信度：已知问题本来预期就是红的，
// 拿它判回归会让门禁常红，最后没人看。
func TestCompareSkipsKnownIssues(t *testing.T) {
	base := rep(DepthSelect, map[string]bool{"a": true, "known": true})
	now := rep(DepthSelect, map[string]bool{"a": true, "known": false})
	for i := range now.Cases {
		if now.Cases[i].ID == "known" {
			now.Cases[i].Known = true
		}
	}

	d, err := Compare(now, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Broke) != 0 {
		t.Fatalf("已知问题不该算回归，实际 %v", d.Broke)
	}
	if d.Regressed() {
		t.Fatal("只有已知问题变红时，不该判定为回归")
	}
}

// ---------- 归集规则 ----------

func TestTallyKnownIssueAccounting(t *testing.T) {
	var r Report
	tally(&r, Case{ID: "p", KnownIssue: false}, &CaseResult{ID: "p", Passed: true})
	tally(&r, Case{ID: "f", KnownIssue: false}, &CaseResult{ID: "f", Passed: false})
	tally(&r, Case{ID: "k", KnownIssue: true}, &CaseResult{ID: "k", Passed: false})
	tally(&r, Case{ID: "kr", KnownIssue: true}, &CaseResult{ID: "kr", Passed: true})

	if r.Passed != 2 || r.Failed != 1 || r.Known != 1 {
		t.Fatalf("归集错误：passed=%d failed=%d known=%d", r.Passed, r.Failed, r.Known)
	}
	if len(r.Resolved) != 1 || r.Resolved[0] != "kr" {
		t.Fatalf("标着已知问题却通过了的应当报进 Resolved，实际 %v", r.Resolved)
	}
}

func TestSummaryLineMentionsKnown(t *testing.T) {
	r := Report{Depth: DepthSelect, Total: 4, Passed: 2, Failed: 1, Known: 1}
	if !strings.Contains(r.SummaryLine(), "已知问题") {
		t.Fatalf("摘要该提到已知问题，实际 %q", r.SummaryLine())
	}
}

// ---------- 基线读写 ----------

func TestBaselineRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "base.json")
	in := rep(DepthSelect, map[string]bool{"a": true})
	in.PromptChars = 123
	if err := SaveBaseline(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.Depth != in.Depth || out.PromptChars != in.PromptChars || len(out.Cases) != 1 {
		t.Fatalf("基线往返丢信息：%+v", out)
	}
	if _, err := LoadBaseline(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("读不存在的基线应当报错")
	}
}

// TestValidDepth 确认深度白名单——CLI 靠它挡掉手滑写错的深度。
func TestValidDepth(t *testing.T) {
	for _, d := range []Depth{DepthSelect, DepthPlan, DepthFull} {
		if !ValidDepth(d) {
			t.Errorf("%q 应当是合法深度", d)
		}
	}
	if ValidDepth("bogus") || ValidDepth("") {
		t.Error("非法深度不该通过校验")
	}
}
