package agent

import (
	"strings"
	"testing"
)

// ---------- 领域化的验收重点 ----------
// 通用指南教模型"怎么写可判定的标准"，角色验收重点补的是"这个领域该盯哪几件事"。
// 尺度因领域而异：数据要能对账，代码要能验证，文案要能直接交付。

// TestRoleAcceptanceFocus_AllRolesCovered 每个内置角色都要有验收重点。
// 新增角色时忘了补，这条会拦住——没有它，新角色的验收就退回通用示例，
// 而通用示例给不出"这个领域什么算做到"。
func TestRoleAcceptanceFocus_AllRolesCovered(t *testing.T) {
	for _, r := range BuiltinRoles {
		if strings.TrimSpace(r.AcceptanceFocus) == "" {
			t.Errorf("角色 %s（%s）缺少 AcceptanceFocus", r.ID, r.Name)
		}
	}
}

// TestRoleAcceptanceFocus_DistinctPerRole 各角色的验收重点不能是同一句套话，
// 否则等于没分领域。
func TestRoleAcceptanceFocus_DistinctPerRole(t *testing.T) {
	seen := map[string]string{}
	for _, r := range BuiltinRoles {
		if prev, dup := seen[r.AcceptanceFocus]; dup {
			t.Errorf("角色 %s 与 %s 的验收重点完全相同：%s", r.ID, prev, r.AcceptanceFocus)
		}
		seen[r.AcceptanceFocus] = r.ID
	}
}

// TestRoleAcceptanceFocus_ReachesPlannerPrompt 角色验收重点要真的进到规划器提示词里，
// 且排在通用验收指南之后——先讲通用原则，再讲本领域该盯什么。
func TestRoleAcceptanceFocus_ReachesPlannerPrompt(t *testing.T) {
	p := &Planner{Reg: menuRegistry(2), MaxSteps: 5, Role: "coder"}
	sys := p.buildSystemPrompt("重构一个函数", "/tmp", nil, nil, "", "")

	focus := FindRole("coder").AcceptanceFocus
	if focus == "" {
		t.Fatal("coder 角色应有验收重点")
	}
	if !strings.Contains(sys, focus) {
		t.Fatalf("规划器提示词应包含 coder 的验收重点：\n%s", focus)
	}
	if !strings.Contains(sys, "本角色（开发工程师）的验收重点") {
		t.Error("应标明这是哪个角色的验收重点")
	}
	// 顺序：通用验收指南在前，领域重点在后
	general := strings.Index(sys, "## 验收标准（acceptance）")
	roleFocus := strings.Index(sys, "本角色（开发工程师）的验收重点")
	if general < 0 || roleFocus < 0 || roleFocus < general {
		t.Errorf("领域重点应排在通用验收指南之后（general=%d, focus=%d）", general, roleFocus)
	}
	// 领域重点应紧邻验收段，不要掉到协作风格之后
	style := strings.Index(sys, "## 协作风格")
	if style >= 0 && roleFocus > style {
		t.Error("领域重点不该出现在协作风格之后，否则离验收指南太远")
	}
}

// TestRoleAcceptanceFocus_DefaultRoleStillGetsFocus 没选角色时也要有验收重点——
// 通用助手同样需要一把可判定的尺子，而不是退回"做得不错"。
func TestRoleAcceptanceFocus_DefaultRoleStillGetsFocus(t *testing.T) {
	p := &Planner{Reg: menuRegistry(2), MaxSteps: 5} // Role 为空
	sys := p.buildSystemPrompt("整理一下下载目录", "/tmp", nil, nil, "", "")
	focus := FindRole("").AcceptanceFocus
	if focus == "" {
		t.Fatal("通用角色应有验收重点")
	}
	if !strings.Contains(sys, focus) {
		t.Errorf("未指定角色时也应注入通用角色的验收重点：%s", focus)
	}
}

// TestRoleAcceptanceFocus_NotConfusedWithRolePrompt 验收重点与角色知识段是两回事，
// 角色知识段仍应出现在专家角色段里（不能因为改了验收段把它挤掉）。
func TestRoleAcceptanceFocus_NotConfusedWithRolePrompt(t *testing.T) {
	p := &Planner{Reg: menuRegistry(2), MaxSteps: 5, Role: "analyst"}
	sys := p.buildSystemPrompt("统计销售数据", "/tmp", nil, nil, "", "")
	role := FindRole("analyst")
	if !strings.Contains(sys, "## 专家角色："+role.Name) {
		t.Error("专家角色知识段应仍然存在")
	}
	if !strings.Contains(sys, role.SystemPrompt) {
		t.Error("角色知识段正文应仍然存在")
	}
}
