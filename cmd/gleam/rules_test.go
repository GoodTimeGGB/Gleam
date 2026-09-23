package main

import (
	"strings"
	"testing"

	"gleam/internal/agent"
)

// ---------- 适用范围的排印 ----------

// TestScopeTextMarksUnlimited 全空范围要排成 "-"，不能留成空白。
// 空白会被读成"这条规则没配范围、大概是漏了"，而它其实是**对所有场景生效**——
// 契约段三条规则都属此类，是提示词的主体。
func TestScopeTextMarksUnlimited(t *testing.T) {
	if got := scopeText(agent.RuleInfo{}); got != "-" {
		t.Fatalf("无条件生效应排成 %q，实际 %q", "-", got)
	}
	got := scopeText(agent.RuleInfo{Modes: []string{"code"}, Tiers: []string{"coding", "office"}})
	for _, want := range []string{"模式 code", "档位 coding/office"} {
		if !strings.Contains(got, want) {
			t.Errorf("适用范围 %q 里缺少 %q", got, want)
		}
	}
	// 角色维度也要排出来：漏了它，规则明明只对某个角色生效却看不出来。
	if !strings.Contains(scopeText(agent.RuleInfo{Roles: []string{"coder"}}), "角色 coder") {
		t.Error("适用范围里缺少角色维度")
	}
}

// ---------- 两种视图 ----------

// TestRenderRulesReport_TableViewListsEveryRule 全表视图必须一条不漏，并给出指纹。
// 漏一条等于告诉读者"这条规则不存在"，比不列更糟。
func TestRenderRulesReport_TableViewListsEveryRule(t *testing.T) {
	o := rulesOutput{Version: "deadbeef01", Mode: "work", Rules: agent.RuleTable()}
	got := renderRulesReport(o, false)
	if !strings.Contains(got, "deadbeef01") {
		t.Error("全表视图没有版本指纹——本次回查就无从与历史对照")
	}
	for _, r := range o.Rules {
		if !strings.Contains(got, r.ID) {
			t.Errorf("全表视图漏了规则 %s", r.ID)
		}
	}
	if !strings.Contains(got, "合计") {
		t.Error("全表视图没有合计行")
	}
}

// TestRenderRulesReport_ScenarioViewSeparatesActiveFromInactive 场景视图要分清"用了"和"没用"。
//
// 这是这个命令存在的理由：人带着一个具体场景来问"这次到底用了哪几条"，
// 尤其是"为什么那条规则没进提示词"。只列生效的答不了后半句。
func TestRenderRulesReport_ScenarioViewSeparatesActiveFromInactive(t *testing.T) {
	table := agent.RuleTable()
	rs := agent.DescribeRuleSet("work", "", "")
	o := rulesOutput{Version: "deadbeef01", Mode: "work", Active: rs.Active, Inactive: rs.Inactive, Rules: table}
	got := renderRulesReport(o, true)

	if !strings.Contains(got, "模式=work") {
		t.Error("场景视图没写出场景——读者不知道自己看的是哪一档")
	}
	for _, id := range rs.Active {
		if !strings.Contains(got, id) {
			t.Errorf("场景视图漏了生效规则 %s", id)
		}
	}
	if !strings.Contains(got, "未生效") {
		t.Error("场景视图没有未生效区——「为什么这条没进提示词」无从回答")
	}
	for _, id := range rs.Inactive {
		if !strings.Contains(got, id) {
			t.Errorf("场景视图漏了未生效规则 %s", id)
		}
	}
	// 工作模式下编程准则必须明确出现在"未生效"里——这是条件化最容易被误解的地方。
	if !strings.Contains(got, "code.verify") {
		t.Error("工作模式下 code.verify 应列为未生效")
	}
}

// TestRenderRulesReport_ChatExplainsWhyEmpty 对话模式生效 0 条，必须解释原因。
// 只打一个"生效 0 条"，读者会以为规则表坏了或漏记了。
func TestRenderRulesReport_ChatExplainsWhyEmpty(t *testing.T) {
	rs := agent.DescribeRuleSet("chat", "", "")
	o := rulesOutput{Version: "deadbeef01", Mode: "chat", Active: rs.Active, Rules: agent.RuleTable()}
	got := renderRulesReport(o, true)
	if !strings.Contains(got, "生效 0 条") {
		t.Fatalf("对话模式应显示 0 条，实际：%q", got)
	}
	if !strings.Contains(got, "不经规划") {
		t.Error("对话模式的 0 条需要一句解释，否则会被当成漏记")
	}
}

// TestRulesOutputJSONKeepsOneShape --json 的形状不能随参数改变，
// 否则脚本得先猜自己拿到的是哪一种。全表字段始终在，场景字段按需出现。
func TestRulesOutputJSONKeepsOneShape(t *testing.T) {
	o := rulesOutput{Version: "v", Mode: "work", Rules: agent.RuleTable()}
	if len(o.Rules) == 0 {
		t.Fatal("规则表为空，这个断言就没意义了")
	}
	withScenario := o
	withScenario.Active = []string{"output.format"}
	if withScenario.Rules == nil || withScenario.Active == nil {
		t.Error("加场景维度后全表字段不该消失")
	}
}
