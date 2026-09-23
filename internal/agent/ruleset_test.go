package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"gleam/internal/harness/growth"
	"gleam/internal/llm"
	"gleam/pkg/types"
)

// 本文件守住 ④「记录产出用了哪版提示词/规则集」。
//
// 一个前置共识：**记录的全部价值在于它跟真实渲染一致。**
// 一份会撒谎的规则集记录比没有更糟——它让人在错误的方向上归因
// （"规则没变，那就是代码坏了"）。所以下面最关键的一条测试是
// TestActiveRuleIDs_ConsistentWithRenderedPrompt：逐条比对"记下的 ID"与
// "真的写进提示词的正文"。

// ---------- 版本指纹 ----------

// TestRuleSetVersion_IsStableAndShort 指纹要能念出来、能贴进工单。
// 太长没人用，太短会撞车；10 位十六进制是折中（与 shortHash 的约定一致）。
func TestRuleSetVersion_IsStableAndShort(t *testing.T) {
	a, b := RuleSetVersion(), RuleSetVersion()
	if a != b {
		t.Fatalf("同一份规则表两次算出不同指纹：%q vs %q", a, b)
	}
	if len(a) != 10 {
		t.Fatalf("指纹应为 10 位，实际 %q（%d 位）", a, len(a))
	}
	if strings.Trim(a, "0123456789abcdef") != "" {
		t.Fatalf("指纹应为小写十六进制，实际 %q", a)
	}
}

// TestRuleSetVersion_SensitiveToEveryField 指纹必须对规则的**每一处**内容都敏感。
//
// 漏掉任何一处，都会造出一类最难查的假象：规则改了，指纹没变，
// 于是两次产出的差异被当成"模型随机性"或"代码回归"排掉。
// 这里逐个字段做变异，确保没有字段被漏进指纹。
func TestRuleSetVersion_SensitiveToEveryField(t *testing.T) {
	base := RuleSetVersion()
	mutations := []struct {
		name string
		mod  func(rs []Rule) []Rule
	}{
		{"改条目正文", func(rs []Rule) []Rule {
			i := ruleIndex(rs, "output.rules")
			items := append([]string(nil), rs[i].Items...)
			items[0] += "（多一个字）"
			rs[i].Items = items
			return rs
		}},
		{"改标题", func(rs []Rule) []Rule {
			i := ruleIndex(rs, "output.rules")
			rs[i].Title += "（改了标题）"
			return rs
		}},
		{"改适用范围", func(rs []Rule) []Rule {
			i := ruleIndex(rs, "schedule.hint")
			rs[i].Scope = Scope{Modes: []string{"code"}}
			return rs
		}},
		{"改槽位", func(rs []Rule) []Rule {
			i := ruleIndex(rs, "schedule.hint")
			rs[i].Slot = SlotContract
			return rs
		}},
		{"改规则 ID", func(rs []Rule) []Rule {
			i := ruleIndex(rs, "code.diff")
			rs[i].ID = "code.diff2"
			return rs
		}},
		{"新增一条规则", func(rs []Rule) []Rule {
			return append(rs, Rule{ID: "tmp.probe", Slot: SlotContract, Items: []string{"探针"}})
		}},
		{"删掉一条规则", func(rs []Rule) []Rule {
			out := append([]Rule(nil), rs...)
			return out[:len(out)-1]
		}},
		{"调换渲染顺序", func(rs []Rule) []Rule {
			out := append([]Rule(nil), rs...)
			out[0], out[1] = out[1], out[0]
			return out
		}},
	}
	for _, m := range mutations {
		orig := stableRules
		cp := append([]Rule(nil), orig...)
		stableRules = m.mod(cp)
		got := RuleSetVersion()
		stableRules = orig
		if got == base {
			t.Errorf("%s：指纹没变（%s），这条改动会悄悄逃过归因", m.name, got)
		}
	}
	// 变异过后必须完全复原——否则后面的测试会跑在一份被改过的表上。
	if RuleSetVersion() != base {
		t.Fatal("变异后规则表没有复原")
	}
}

func ruleIndex(rs []Rule, id string) int {
	for i, r := range rs {
		if r.ID == id {
			return i
		}
	}
	panic("测试写错了：规则表里没有 " + id)
}

// ---------- 生效规则 ID ----------

// TestActiveRuleIDs_ConsistentWithRenderedPrompt 记录与渲染必须共用同一份选择逻辑。
//
// 这是 ④ 的地基：ID 列表与提示词正文一旦对不上，"这次用了哪几条规则"
// 就成了另一份会走偏的文档。逐场景、逐条规则双向比对：
//
//   - 记在生效列表里的 → 它的正文必须出现在对应槽位的渲染结果里；
//   - 没记进去的（含同一 ID 未被选中的变体）→ 它的正文必须**不出现**。
func TestActiveRuleIDs_ConsistentWithRenderedPrompt(t *testing.T) {
	scenarios := []struct {
		mode, tier, role string
	}{
		{"work", "", ""},
		{"work", TierOffice, "writer"},
		{"work", TierReasoning, "analyst"},
		{"code", "", "coder"},
		{"code", TierEconomy, "coder"},
		{"code", TierCoding, "coder"},
		{"code", TierReasoning, "ops"},
	}
	for _, sc := range scenarios {
		rendered := map[string]string{}
		for _, slot := range []string{SlotContract, SlotCode, SlotSchedule} {
			rendered[slot] = renderRules(slot, sc.mode, sc.tier, sc.role)
		}

		// 每个 ID 在场景中"被选中"的那一条（变体在前，第一条命中即选中）——
		// 期望的生效顺序也就是规则表里的出现顺序。
		chosen := map[string]bool{}
		var want []string
		for _, r := range stableRules {
			if chosen[r.ID] || !r.Scope.allows(sc.mode, sc.tier, sc.role) {
				continue
			}
			chosen[r.ID] = true
			want = append(want, r.ID)
		}

		got := ActiveRuleIDs(sc.mode, sc.tier, sc.role)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("场景 %+v：生效 ID 为 [%s]，期望 [%s]", sc, strings.Join(got, " "), strings.Join(want, " "))
		}

		seen := map[string]bool{}
		for _, r := range stableRules {
			if seen[r.ID] {
				continue // 同一 ID 的后续变体：本场景用的是前一条，这条不该出现
			}
			hit := r.Scope.allows(sc.mode, sc.tier, sc.role)
			if hit {
				seen[r.ID] = true
			}
			inPrompt := strings.Contains(rendered[r.Slot], r.Items[0])
			if inPrompt != hit {
				t.Errorf("场景 %+v：规则 %s 的记录与渲染不一致（记为生效=%v，正文在提示词里=%v）",
					sc, r.ID, hit, inPrompt)
			}
		}
	}
}

// TestActiveRuleIDs_CannotDistinguishVariants 明确记下这条边界：ID 列表**区分不了变体**。
//
// code.verify 有"强档软化版"与"兜底版"两个变体，共用同一个 ID（按设计：先匹配到的生效）。
// 所以"这次用了 code.verify"并不说明用的是哪一版措辞——
// 措辞层面的归因靠版本指纹（改一字就变），ID 列表只回答"用了哪几条"。
//
// 这个边界是变异测试逼出来的：把 ActiveRuleIDs 改成忽略档位，全部测试仍然绿——
// 因为两个变体的 ID 相同。测试绿不代表行为被守住了，得先问"它到底看不看得见"。
// 与其假装看不见，不如把它写成断言：将来若有人指望靠 ID 列表区分变体措辞，这里会先红。
func TestActiveRuleIDs_CannotDistinguishVariants(t *testing.T) {
	withTier := ActiveRuleIDs("code", TierCoding, "coder")
	without := ActiveRuleIDs("code", "", "coder")
	if strings.Join(withTier, ",") != strings.Join(without, ",") {
		t.Fatalf("两个档位下生效 ID 不同了（%v vs %v）——若变体改用不同 ID，记录与归因都要重新设计",
			withTier, without)
	}
	// 但提示词必须不同：条件化的效果真实存在，只是不体现在 ID 上，而体现在指纹上。
	if renderRules(SlotCode, "code", TierCoding, "coder") == renderRules(SlotCode, "code", "", "coder") {
		t.Fatal("两个档位下的编程准则完全相同——按档位条件化失效了")
	}
}

// TestActiveRuleIDs_ChatUsesNoRules 对话模式直连模型、不经规划，一条规则都没用上。
//
// 记成"用了 contract 那几条"是撒谎：翻日志的人会以为对话回复也受规则约束，
// 从而往完全错误的方向排查。宁可留空。
func TestActiveRuleIDs_ChatUsesNoRules(t *testing.T) {
	if got := ActiveRuleIDs(string(types.TaskChat), "", ""); len(got) != 0 {
		t.Errorf("对话模式不该记任何规则，实际 %v", got)
	}
	if got := ActiveRuleIDs("work", "", ""); len(got) == 0 {
		t.Error("工作模式至少要记下契约段的规则")
	}
}

// ---------- 回查（gleam rules 用的快照） ----------

// TestRuleTable_IsSnapshotOfStableRules 回查视图必须与真实规则表对得上，
// 且是副本——视图被用来排障，改它不该改坏内部表。
func TestRuleTable_IsSnapshotOfStableRules(t *testing.T) {
	tab := RuleTable()
	if len(tab) != len(stableRules) {
		t.Fatalf("回查到 %d 条，规则表里是 %d 条", len(tab), len(stableRules))
	}
	for i, r := range tab {
		if r.ID != stableRules[i].ID || r.Slot != stableRules[i].Slot {
			t.Errorf("第 %d 条对不上：视图 %s/%s，实际 %s/%s", i, r.Slot, r.ID, stableRules[i].Slot, stableRules[i].ID)
		}
		if r.Items != len(stableRules[i].Items) {
			t.Errorf("规则 %s 条目数记成 %d，实际 %d", r.ID, r.Items, len(stableRules[i].Items))
		}
		chars := 0
		for _, it := range stableRules[i].Items {
			chars += len(it)
		}
		if r.Chars != chars {
			t.Errorf("规则 %s 字符数记成 %d，实际 %d", r.ID, r.Chars, chars)
		}
	}
	tab[0].ID = "改坏了"
	if RuleTable()[0].ID == "改坏了" {
		t.Error("回查视图与内部表共用底层数组，改视图会改坏规则表")
	}
}

// TestDescribeRuleSet_PartitionsRuleIDs 生效 + 未生效 = 表里全部 ID，且不重不漏。
//
// "未生效"是回答"为什么这条没进提示词"的第一手材料：漏了会让人去找不存在的 bug，
// 重复了（同一 ID 的两个变体一个生效、一个却被算作未生效）会让人以为规则丢了。
func TestDescribeRuleSet_PartitionsRuleIDs(t *testing.T) {
	for _, sc := range []struct{ mode, tier, role string }{
		{"work", "", ""},
		{"code", TierCoding, "coder"},
	} {
		rs := DescribeRuleSet(sc.mode, sc.tier, sc.role)
		if rs.Version != RuleSetVersion() {
			t.Errorf("场景 %+v 指纹对不上", sc)
		}
		all := map[string]bool{}
		for _, r := range stableRules {
			all[r.ID] = true
		}
		covered := map[string]bool{}
		for _, id := range rs.Active {
			if !all[id] {
				t.Errorf("生效列表里有表里不存在的 ID %q", id)
			}
			if covered[id] {
				t.Errorf("ID %q 在生效列表里重复", id)
			}
			covered[id] = true
		}
		for _, id := range rs.Inactive {
			if !all[id] {
				t.Errorf("未生效列表里有表里不存在的 ID %q", id)
			}
			if covered[id] {
				t.Errorf("ID %q 同时出现在生效与未生效列表里", id)
			}
			covered[id] = true
		}
		if len(covered) != len(all) {
			t.Errorf("场景 %+v：两份列表合计 %d 个 ID，表里是 %d 个", sc, len(covered), len(all))
		}
		// 变体去重：code.verify 有两条，只要一条生效就不能把另一条算作"未生效"
		if sc.mode == "code" {
			for _, id := range rs.Inactive {
				if id == "code.verify" {
					t.Error("code 模式下 code.verify 已生效，不该再出现在未生效列表里")
				}
			}
		}
	}
}

// ---------- 落库：产出与成长日志 ----------

// TestRunGoal_RecordsRuleSet 一次规划类产出必须带上"用了哪版规则"。
func TestRunGoal_RecordsRuleSet(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"好"}}]}`),
		reflectScript(92, "done", "已回复"),
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "打个招呼", Role: "coder"})
	if res.Status != types.GoalSuccess {
		t.Fatalf("任务未成功: %+v", res)
	}
	if res.RuleSet != RuleSetVersion() {
		t.Errorf("结果里的规则集指纹是 %q，当前规则表是 %q", res.RuleSet, RuleSetVersion())
	}
	want := ActiveRuleIDs(string(types.TaskWork), f.a.EffectiveTier("coder"), "coder")
	if strings.Join(res.Rules, ",") != strings.Join(want, ",") {
		t.Errorf("结果里记的生效规则是 %v，期望 %v", res.Rules, want)
	}
	if len(res.Rules) == 0 {
		t.Error("工作模式的产出不该记成「没用规则」")
	}
}

// TestRunGoal_RuleSetReachesGrowthLog 规则集必须跟着任务一起进成长日志。
//
// 产出结果里的字段只留在本次响应里，成长日志才是能回看的历史。
// 少了它，"换了规则之后平均分变了多少"就永远算不出来——
// 而那正是判断"这次减法到底值不值"唯一可信的长期数据。
func TestRunGoal_RuleSetReachesGrowthLog(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"好"}}]}`),
		reflectScript(92, "done", "已回复"),
	})
	g, err := growth.Open(filepath.Join(t.TempDir(), "growth"))
	if err != nil {
		t.Fatal(err)
	}
	f.a.Growth = g
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "打个招呼"})
	if res.Status != types.GoalSuccess {
		t.Fatalf("任务未成功: %+v", res)
	}
	entries := g.Recent(1)
	if len(entries) != 1 {
		t.Fatalf("成长日志里有 %d 条，期望 1 条", len(entries))
	}
	if entries[0].RuleSet != RuleSetVersion() {
		t.Errorf("成长日志里的规则集是 %q，当前规则表是 %q", entries[0].RuleSet, RuleSetVersion())
	}
}

// TestRunGoal_ChatRecordsNoRuleSet 对话模式没走规划提示词，就不该留下规则集记录。
func TestRunGoal_ChatRecordsNoRuleSet(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		{Kind: "chat_mode", Texts: []string{"你好呀！我是 Gleam。"}},
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{
		Goal: "你好", TaskMode: types.TaskChat,
	})
	if res.Status != types.GoalSuccess {
		t.Fatalf("对话未成功: %+v", res)
	}
	if res.RuleSet != "" || len(res.Rules) != 0 {
		t.Errorf("对话模式不该记规则集，实际 RuleSet=%q Rules=%v", res.RuleSet, res.Rules)
	}
}
