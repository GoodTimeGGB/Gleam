package skill

import (
	"context"
	"testing"
	"time"

	"gleam/internal/harness/registry"
	"gleam/pkg/types"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func sampleSkill() Skill {
	return Skill{
		Name:        "demo-skill",
		Description: "演示技能",
		Params:      []string{"dir"},
		Steps: []types.Step{
			{ID: "s1", Tool: "file.list", Description: "列出", Args: map[string]any{"path": "{{dir}}"}},
			{ID: "s2", Tool: "reply", Description: "回复", Args: map[string]any{"text": "处理完成 {ref:s1.count}"}},
		},
	}
}

func TestStore_SaveGetRoundTrip(t *testing.T) {
	st := newTestStore(t)
	v, err := st.Save(sampleSkill())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if v != 1 {
		t.Errorf("首次版本 = %d", v)
	}
	got, err := st.Get("demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "演示技能" || len(got.Params) != 1 {
		t.Errorf("字段丢失: %+v", got)
	}
	if len(got.Steps) != 2 || got.Steps[0].Tool != "file.list" {
		t.Fatalf("步骤错误: %+v", got.Steps)
	}
	if got.Steps[0].Args["path"] != "{{dir}}" {
		t.Errorf("args 丢失: %v", got.Steps[0].Args)
	}
	if got.Steps[1].DependsOn != nil {
		t.Errorf("depends_on 应为空: %v", got.Steps[1].DependsOn)
	}
}

func TestStore_Versioning(t *testing.T) {
	st := newTestStore(t)
	v1, _ := st.Save(sampleSkill())
	v2, err := st.Save(sampleSkill())
	if err != nil {
		t.Fatal(err)
	}
	if v1 != 1 || v2 != 2 {
		t.Errorf("版本号 = %d -> %d", v1, v2)
	}
	got, _ := st.Get("demo-skill")
	if got.Version != 2 {
		t.Errorf("Get().Version = %d", got.Version)
	}
}

func TestStore_RecordRun(t *testing.T) {
	st := newTestStore(t)
	st.Save(sampleSkill())
	if err := st.RecordRun("demo-skill", true); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordRun("demo-skill", false); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get("demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	if got.Runs != 2 || got.Successes != 1 {
		t.Errorf("统计 = %d/%d", got.Successes, got.Runs)
	}
	if got.LastUsed == nil || time.Since(*got.LastUsed) > time.Minute {
		t.Errorf("LastUsed = %v", got.LastUsed)
	}
	if got.Version != 1 {
		t.Errorf("RecordRun 不应递增版本: %d", got.Version)
	}
}

// TestStore_DisabledSurvivesReloadAndSave 「停用」要成立，得同时满足三件事：
// 落盘（重启后还停着）、被 Save 尊重（自动优化与市场重装都会重写这个文件）、
// 从进规划上下文的那份清单里消失。少任何一件，界面显示的都是一句假话。
func TestStore_DisabledSurvivesReloadAndSave(t *testing.T) {
	st := newTestStore(t)
	st.Save(sampleSkill())
	if err := st.RecordRun("demo-skill", true); err != nil {
		t.Fatal(err)
	}
	sk, err := st.SetDisabled("demo-skill", true)
	if err != nil {
		t.Fatal(err)
	}
	if !sk.Disabled {
		t.Fatal("SetDisabled 返回的状态没带上停用")
	}

	reopened, err := Open(st.dir) // 等价于重启进程：只认磁盘上的 YAML
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get("demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Disabled {
		t.Error("停用未落盘，重启后就自己跑起来了")
	}
	if got.Runs != 1 || got.Successes != 1 {
		t.Errorf("统计被改写: %+v", got)
	}
	if len(got.Steps) != 2 || got.Params[0] != "dir" {
		t.Errorf("步骤或参数丢失: %+v", got)
	}

	if _, err := reopened.Save(sampleSkill()); err != nil {
		t.Fatal(err)
	}
	after, _ := reopened.Get("demo-skill")
	if !after.Disabled {
		t.Error("Save 把停用状态抹掉了——重新保存不是「启用」的入口")
	}
	if after.Version != 2 {
		t.Errorf("Save 仍应递增版本: %d", after.Version)
	}

	if n := len(reopened.ListSummaries()); n != 0 {
		t.Errorf("ListSummaries（进规划上下文）不应含停用技能，得到 %d 条", n)
	}
	if _, err := reopened.SetDisabled("demo-skill", false); err != nil {
		t.Fatal(err)
	}
	if n := len(reopened.ListSummaries()); n != 1 {
		t.Errorf("启用后应回到清单，得到 %d 条", n)
	}
	// 停用一个不存在的技能要报错，而不是悄悄建一个空文件
	if _, err := reopened.SetDisabled("no-such", true); err == nil {
		t.Error("不存在的技能应报错")
	}
}

func TestStore_ListDelete(t *testing.T) {
	st := newTestStore(t)
	st.Save(sampleSkill())
	s2 := sampleSkill()
	s2.Name = "another"
	st.Save(s2)
	list := st.List()
	if len(list) != 2 {
		t.Fatalf("List = %d", len(list))
	}
	if list[0].Name != "another" || list[1].Name != "demo-skill" {
		t.Errorf("排序错误: %v", list)
	}
	if err := st.Delete("demo-skill"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get("demo-skill"); err == nil {
		t.Error("删除后应不存在")
	}
	if err := st.Delete("nope"); err == nil {
		t.Error("删除不存在应报错")
	}
}

func TestStore_Validate(t *testing.T) {
	st := newTestStore(t)
	bad := []Skill{
		{Name: "", Steps: []types.Step{{Tool: "reply"}}},
		{Name: "bad name!", Steps: []types.Step{{Tool: "reply"}}},
		{Name: "ok-name"},
		{Name: "dup", Steps: []types.Step{{ID: "s1", Tool: "reply"}, {ID: "s1", Tool: "reply"}}},
	}
	for i, s := range bad {
		if _, err := st.Save(s); err == nil {
			t.Errorf("bad[%d] 应报错", i)
		}
	}
}

func TestSubstituteParams(t *testing.T) {
	args := map[string]any{
		"path": "{{dir}}/out.txt",
		"nested": map[string]any{
			"list": []any{"{{dir}}", 1, true},
		},
		"whole": "{{dir}}",
	}
	got, err := SubstituteParams(args, map[string]string{"dir": "D:/ws"})
	if err != nil {
		t.Fatal(err)
	}
	if got["path"] != "D:/ws/out.txt" {
		t.Errorf("path = %v", got["path"])
	}
	nested := got["nested"].(map[string]any)
	if nested["list"].([]any)[0] != "D:/ws" {
		t.Errorf("嵌套替换失败: %v", nested)
	}
	if got["whole"] != "D:/ws" {
		t.Errorf("整值替换失败: %v", got["whole"])
	}
	// 原参数不被修改
	if args["path"] != "{{dir}}/out.txt" {
		t.Error("不应修改原参数")
	}
	// 缺失参数
	if _, err := SubstituteParams(map[string]any{"a": "{{missing}}"}, nil); err == nil {
		t.Error("缺失参数应报错")
	}
}

// ---------- 技能执行（经由 RunSkill 的工具级验证在 agent 包测试） ----------

type countingTool struct {
	registry.Registry
}

func TestSkillRunnerWiring(t *testing.T) {
	// 占位：RunSkill 的行为在 internal/agent/agent_test.go 中覆盖
	ctx := context.Background()
	_ = ctx
}
