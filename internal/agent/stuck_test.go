package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gleam/internal/llm"
	"gleam/pkg/types"
)

// samePlan 三轮完全相同的计划：读文件 + 回复。
func samePlan() string {
	return `{"steps":[{"id":"s1","description":"读取","tool":"file.read","args":{"path":"a.txt"}},
		{"id":"s2","description":"回复","tool":"reply","args":{"text":"看完了"},"depends_on":["s1"]}]}`
}

// stuckFixture 装配一个"每次重规划都产出同一份计划"的任务。
func stuckFixture(t *testing.T, rounds int) *agentFixture {
	t.Helper()
	var scripts []llm.Scripted
	for i := 0; i < rounds; i++ {
		scripts = append(scripts, planScript(samePlan()))
		scripts = append(scripts, reflectScript(45, "replan", "内容不够，需要再看看"))
	}
	f := newFixture(t, scripts)
	// 工作区里放一个文件，让 file.read 真的能跑起来
	writeFile(t, f.ws, "a.txt", "hello gleam")
	return f
}

// TestStuck_DetectsSameActionRepeated 连续多轮执行完全相同的动作应被判定为原地打转。
func TestStuck_DetectsSameActionRepeated(t *testing.T) {
	f := stuckFixture(t, 3)
	f.a.Cfg.Agent.MaxReplans = 4 // 给足重规划次数，验证是"打转"而非"次数用尽"让它停下
	f.a.Cfg.Agent.StuckThreshold = 3

	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "读 a.txt 并总结", Mode: "auto"})
	if res.Status != types.GoalFailed {
		t.Fatalf("打转应导致失败停止，实际 %s（%s）", res.Status, res.Error)
	}
	if !strings.Contains(res.Error, "原地打转") {
		t.Fatalf("错误信息应说明打转，实际：%s", res.Error)
	}
	if res.Suggestion == "" {
		t.Error("应给出可操作的后续建议")
	}
	// 关键：第 3 轮就停了，没有继续跑满 5 次规划
	if got := countKind(f.llm.Calls, "plan"); got != 3 {
		t.Fatalf("应在第 3 轮停下，实际规划了 %d 次", got)
	}
}

// TestStuck_DisabledWhenZero 阈值设为 0 时不检测，行为与之前一致。
func TestStuck_DisabledWhenZero(t *testing.T) {
	f := stuckFixture(t, 2)
	f.a.Cfg.Agent.MaxReplans = 1 // 只有 2 轮，且阈值关闭 → 走原来的"次数用尽"路径
	f.a.Cfg.Agent.StuckThreshold = 0

	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "读 a.txt 并总结", Mode: "auto"})
	if strings.Contains(res.Error, "原地打转") {
		t.Fatalf("阈值关闭时不应报打转，实际：%s", res.Error)
	}
}

// TestStuck_DifferentPlansNotFlagged 每轮计划不同（参数在变）不应被误判。
func TestStuck_DifferentPlansNotFlagged(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"读取","tool":"file.read","args":{"path":"a.txt"}}]}`),
		reflectScript(45, "replan", "再看一个"),
		planScript(`{"steps":[{"id":"s1","description":"读取","tool":"file.read","args":{"path":"b.txt"}}]}`),
		reflectScript(45, "replan", "还差一点"),
		planScript(`{"steps":[{"id":"s1","description":"读取","tool":"file.read","args":{"path":"c.txt"}}]}`),
		reflectScript(45, "replan", "再来"),
	})
	writeFile(t, f.ws, "a.txt", "A")
	writeFile(t, f.ws, "b.txt", "B")
	writeFile(t, f.ws, "c.txt", "C")
	f.a.Cfg.Agent.MaxReplans = 4
	f.a.Cfg.Agent.StuckThreshold = 3

	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "依次读三个文件", Mode: "auto"})
	if strings.Contains(res.Error, "原地打转") {
		t.Fatalf("参数每轮都不同，不应判定打转：%s", res.Error)
	}
	// 三轮计划各不相同，至少应完整跑完这三次（不提前中断）
	if got := countKind(f.llm.Calls, "plan"); got < 3 {
		t.Fatalf("参数每轮不同应至少跑完 3 次规划，实际 %d", got)
	}
}

// TestStuck_DedupeNotCountedTwice 单轮内被去重的多条结果只算一次，避免误判。
func TestStuck_DedupeNotCountedTwice(t *testing.T) {
	f := newFixture(t, nil)
	f.a.Cfg.Agent.StuckThreshold = 3
	f.a.initUsage("t-stuck")
	// 同一指纹在同一轮里出现两次（模拟去重产生的两条结果）
	steps := []types.StepResult{
		{Tool: "file.read", Status: types.StepSucceeded, FinalArgs: map[string]any{"path": "a.txt"}},
		{Tool: "file.read", Status: types.StepSucceeded, FinalArgs: map[string]any{"path": "a.txt"}, Deduped: true},
	}
	if fp, rounds := f.a.noteSteps("t-stuck", steps); fp != "" {
		t.Fatalf("单轮重复不应计数为多轮，实际 %d 轮：%s", rounds, describeFingerprint(fp))
	}
	// 再来两轮 → 第 3 轮才该触发
	f.a.noteSteps("t-stuck", steps)
	fp, rounds := f.a.noteSteps("t-stuck", steps)
	if fp == "" || rounds != 3 {
		t.Fatalf("第 3 轮应触发打转，实际 fp=%q rounds=%d", fp, rounds)
	}
}

// TestStuck_SkippedStepsNotCounted 因依赖失败被跳过的步骤不算一次尝试。
func TestStuck_SkippedStepsNotCounted(t *testing.T) {
	f := newFixture(t, nil)
	f.a.Cfg.Agent.StuckThreshold = 2
	f.a.initUsage("t-skip")
	skipped := []types.StepResult{
		{Tool: "file.read", Status: types.StepSkipped, FinalArgs: map[string]any{"path": "a.txt"}},
	}
	for i := 0; i < 5; i++ {
		if fp, _ := f.a.noteSteps("t-skip", skipped); fp != "" {
			t.Fatalf("被跳过的步骤不应计入打转")
		}
	}
}

// countKind 统计某类标记的模型调用次数。
func countKind(calls []llm.ChatRequest, kind string) int {
	markers := map[string]string{
		"plan": llm.MarkerPlan, "reflect": llm.MarkerReflect, "chat": llm.MarkerChat,
	}
	n := 0
	for _, c := range calls {
		if strings.Contains(c.System, markers[kind]) {
			n++
		}
	}
	return n
}

// writeFile 在工作区写一个测试文件。
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
