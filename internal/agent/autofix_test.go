package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"gleam/internal/harness/skill"
	"gleam/internal/llm"
	"gleam/pkg/types"
)

// 设计文档 §4.2.2：技能每次执行记录成功/失败，失败后自动优化参数并保存新版本。

func TestRunSkill_AutoOptimizeOnFailure(t *testing.T) {
	f := newFixture(t, nil)
	// 非 Mock 客户端（mockOneShot）才会触发自动优化
	f.a.LLM = mockReturning(`{"steps":[
		{"id":"s1","description":"写文件","tool":"file.write","args":{"path":"optimized.txt","content":"v2 内容"}}
	]}`)
	if _, err := f.store.Save(skill.Skill{
		Name:        "broken-skill",
		Description: "使用不存在工具的技能",
		Steps:       []types.Step{{ID: "s1", Tool: "no-such-tool", Args: map[string]any{}}},
	}); err != nil {
		t.Fatal(err)
	}

	out, err := f.a.RunSkill(context.Background(), "broken-skill", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["status"] != string(types.GoalFailed) {
		t.Errorf("首次运行应失败: %v", out)
	}
	got, err := f.store.Get("broken-skill")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 {
		t.Fatalf("自动优化后应保存为 v2, got v%d", got.Version)
	}
	if got.Steps[0].Tool != "file.write" {
		t.Errorf("优化后步骤错误: %+v", got.Steps[0])
	}
	found := false
	for _, ev := range f.notify.progress {
		if strings.Contains(ev.Message, "已自动优化参数并保存为 v2") {
			found = true
		}
	}
	if !found {
		t.Error("应推送自动优化通知")
	}
}

func TestRunSkill_AutoOptimizeSkippedForMock(t *testing.T) {
	f := newFixture(t, nil) // fixture 默认使用 *llm.MockClient
	if _, err := f.store.Save(skill.Skill{
		Name:  "mock-skill",
		Steps: []types.Step{{ID: "s1", Tool: "no-such-tool", Args: map[string]any{}}},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := f.a.RunSkill(context.Background(), "mock-skill", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["status"] != string(types.GoalFailed) {
		t.Errorf("应失败: %v", out)
	}
	got, _ := f.store.Get("mock-skill")
	if got.Version != 1 {
		t.Errorf("Mock 模型下不应自动优化，version = %d", got.Version)
	}
	if got.Runs != 1 || got.Successes != 0 {
		t.Errorf("统计应记录失败: %d/%d", got.Successes, got.Runs)
	}
}

func TestRunSkill_AutoOptimizeInvalidLLMOutputIgnored(t *testing.T) {
	f := newFixture(t, nil)
	f.a.LLM = mockReturning("这不是 JSON")
	if _, err := f.store.Save(skill.Skill{
		Name:  "bad-output-skill",
		Steps: []types.Step{{ID: "s1", Tool: "no-such-tool", Args: map[string]any{}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.RunSkill(context.Background(), "bad-output-skill", nil); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.Get("bad-output-skill")
	if got.Version != 1 {
		t.Errorf("LLM 输出非法时不应保存新版本, version = %d", got.Version)
	}
}

// 设计文档 §9：上下文自动压缩——超出短期窗口的对话摘要存储并注入规划。

func TestRunGoal_ContextAutoCompressInjectedIntoPlanner(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"好的"}}]}`),
		reflectScript(90, "done", "ok"),
	})
	// 短期容量 20：写入 25 轮制造溢出（溢出内容含可检索关键词）
	for i := 0; i < 25; i++ {
		f.a.Mem.AddTurn("user", fmt.Sprintf("历史第 %d 轮：记住关键词琥珀%d", i, i))
	}
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "继续之前的任务"})
	if res.Status != types.GoalSuccess {
		t.Fatalf("status = %s err = %s", res.Status, res.Error)
	}
	compressed, injected := false, false
	for _, call := range f.llm.Calls {
		// 压缩调用：溢出内容作为 user 消息交给压缩器
		if strings.Contains(call.System, llm.MarkerCompress) && strings.Contains(call.Messages[0].Content, "琥珀") {
			compressed = true
		}
		// 规划调用：摘要注入系统提示词
		if strings.Contains(call.System, "早期上下文摘要") && strings.Contains(call.System, "琥珀") {
			injected = true
		}
	}
	if !compressed {
		t.Error("应发生上下文压缩 LLM 调用")
	}
	if !injected {
		t.Error("规划提示词应注入压缩摘要")
	}
	if f.a.Mem.Summary() == "" {
		t.Error("滚动摘要不应为空")
	}
}

func TestRunGoal_ContextCompressDisabled(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"好"}}]}`),
		reflectScript(90, "done", "ok"),
	})
	f.a.Cfg.Agent.ContextCompress = false
	for i := 0; i < 25; i++ {
		f.a.Mem.AddTurn("user", fmt.Sprintf("历史第 %d 轮", i))
	}
	f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "g"})
	if f.a.Mem.Summary() != "" {
		t.Error("关闭压缩时不应产生摘要")
	}
	for _, call := range f.llm.Calls {
		if strings.Contains(call.System, llm.MarkerCompress) {
			t.Error("关闭压缩时不应调用压缩器")
		}
	}
}
