package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"gleam/internal/llm"
	"gleam/pkg/types"
)

// TestBudget_ZeroMeansUnlimited 三项预算都是 0 时不限制，任务照常跑完。
func TestBudget_ZeroMeansUnlimited(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"ok"}}]}`),
		reflectScript(92, "done", "完成"),
	})
	f.a.Cfg.Agent.MaxLLMCallsPerTask = 0
	f.a.Cfg.Agent.MaxTokensPerTask = 0
	f.a.Cfg.Agent.MaxTaskDurationSecs = 0
	f.a.initUsage("t-unlimited")
	f.a.addUsage("t-unlimited", llm.Usage{PromptTokens: 100, CompletionTokens: 100})
	if got := f.a.overBudget("t-unlimited"); got != "" {
		t.Fatalf("预算全为 0 时不应限流，实际：%s", got)
	}
}

// TestBudget_StopsOnCallLimit 模型调用次数超预算时，任务应停下并征求用户确认。
func TestBudget_StopsOnCallLimit(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"ok"}}]}`),
		reflectScript(92, "done", "完成"),
	})
	f.a.Cfg.Agent.MaxLLMCallsPerTask = 1
	f.notify.approveAll = false // 用户拒绝继续

	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "统计一下目录", Mode: "auto"})
	if res.Status != types.GoalFailed {
		t.Fatalf("超预算且用户拒绝时应失败停止，实际 %s（%s）", res.Status, res.Error)
	}
	if !strings.Contains(res.Error, "超出任务预算") {
		t.Fatalf("错误信息应说明原因，实际：%s", res.Error)
	}
	found := false
	for _, req := range f.notify.approvals {
		if strings.Contains(req.Reason, "预算") {
			found = true
		}
	}
	if !found {
		t.Fatalf("应通过审批通道询问用户是否继续，实际审批记录：%+v", f.notify.approvals)
	}
}

// TestBudget_GrantExtendsLimit 用户批准后追加一倍额度，任务可以继续。
func TestBudget_GrantExtendsLimit(t *testing.T) {
	f := newFixture(t, nil)
	f.a.Cfg.Agent.MaxLLMCallsPerTask = 2
	f.a.initUsage("t-grant")
	f.a.addUsage("t-grant", llm.Usage{PromptTokens: 10, CompletionTokens: 10})
	f.a.addUsage("t-grant", llm.Usage{PromptTokens: 10, CompletionTokens: 10})
	if got := f.a.overBudget("t-grant"); got == "" {
		t.Fatal("达到调用上限时应报告超限")
	}
	f.a.grantBudget("t-grant")
	if got := f.a.overBudget("t-grant"); got != "" {
		t.Fatalf("批准后应获得追加额度，仍超限：%s", got)
	}
	f.a.addUsage("t-grant", llm.Usage{PromptTokens: 10, CompletionTokens: 10})
	f.a.addUsage("t-grant", llm.Usage{PromptTokens: 10, CompletionTokens: 10})
	if got := f.a.overBudget("t-grant"); got == "" {
		t.Fatal("追加额度用尽后应再次报告超限")
	}
}

// TestBudget_TokenLimit token 预算应按累计消耗判定。
func TestBudget_TokenLimit(t *testing.T) {
	f := newFixture(t, nil)
	f.a.Cfg.Agent.MaxTokensPerTask = 100
	f.a.initUsage("t-token")
	f.a.addUsage("t-token", llm.Usage{PromptTokens: 60, CompletionTokens: 30})
	if got := f.a.overBudget("t-token"); got != "" {
		t.Fatalf("90 < 100 不应超限，实际：%s", got)
	}
	f.a.addUsage("t-token", llm.Usage{PromptTokens: 20, CompletionTokens: 0})
	if got := f.a.overBudget("t-token"); got == "" {
		t.Fatal("累计 110 超过 100 应报告超限")
	}
}

// TestBudget_DurationLimit 运行时长超过预算也应熔断。
func TestBudget_DurationLimit(t *testing.T) {
	f := newFixture(t, nil)
	f.a.Cfg.Agent.MaxTaskDurationSecs = 1
	f.a.initUsage("t-dur")
	f.a.mu.Lock()
	f.a.tasks["t-dur"] = &taskHandle{id: "t-dur", started: time.Now().Add(-3 * time.Second)}
	f.a.mu.Unlock()
	if got := f.a.overBudget("t-dur"); got == "" {
		t.Fatal("运行时长超预算应报告超限")
	}
}

// TestBudget_ClearedWithTask 任务淘汰时预算记录应一并释放，避免内存泄漏。
func TestBudget_ClearedWithTask(t *testing.T) {
	f := newFixture(t, nil)
	f.a.Cfg.Agent.MaxLLMCallsPerTask = 1
	f.a.initUsage("t-drop")
	f.a.grantBudget("t-drop")
	f.a.dropUsage("t-drop")
	f.a.usageMu.Lock()
	_, left := f.a.budget["t-drop"]
	f.a.usageMu.Unlock()
	if left {
		t.Fatal("任务淘汰后预算记录应被清除")
	}
}
