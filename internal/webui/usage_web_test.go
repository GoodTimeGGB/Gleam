package webui

import (
	"testing"
	"time"
)

// usageOf 从任务详情里取出消耗统计。
func usageOf(t *testing.T, task map[string]any) map[string]any {
	t.Helper()
	res, _ := task["result"].(map[string]any)
	if res == nil {
		t.Fatalf("任务缺少 result: %v", task)
	}
	u, _ := res["usage"].(map[string]any)
	if u == nil {
		t.Fatalf("任务缺少 usage: %v", res)
	}
	return u
}

// TestUsage_ChatTask 对话模式：模型调用次数与 token 应被记录。
func TestUsage_ChatTask(t *testing.T) {
	f, _ := newGEOFixture(t)
	res := f.call("POST", "/api/goals", map[string]any{
		"goal":      "帮我写一段产品介绍文案",
		"task_mode": "chat",
		"role":      "writer",
	})
	id, _ := res["task_id"].(string)
	task := f.waitTask(id, 8*time.Second)
	u := usageOf(t, task)

	if got := u["llm_calls"].(float64); got < 1 {
		t.Fatalf("应记录至少 1 次模型调用，实际 %v", got)
	}
	if got := u["prompt_tokens"].(float64); got <= 0 {
		t.Fatalf("应统计输入 token，实际 %v", got)
	}
	if got := u["completion_tokens"].(float64); got <= 0 {
		t.Fatalf("应统计输出 token，实际 %v", got)
	}
	if got := u["duration_ms"].(float64); got <= 0 {
		t.Fatalf("应记录耗时，实际 %v", got)
	}
	// 未返回 usage 的客户端走估算，应如实标注
	if got := u["estimated_calls"].(float64); got < 1 {
		t.Fatalf("估算调用次数应 >= 1，实际 %v", got)
	}
}

// TestUsage_WorkTaskCountsToolCalls 工作模式：工具执行次数应计入。
func TestUsage_WorkTaskCountsToolCalls(t *testing.T) {
	plan := `{"steps":[
		{"id":"s1","description":"写文件","tool":"file.write","args":{"path":"usage-test.txt","content":"消耗看板测试"}},
		{"id":"s2","description":"回复","tool":"reply","args":{"text":"已写入"},"depends_on":["s1"]}
	]}`
	f, _ := newGEOFixtureWithPlan(t, plan)
	res := f.call("POST", "/api/goals", map[string]any{
		"goal":      "把一段文字写入 usage-test.txt",
		"task_mode": "work",
	})
	id, _ := res["task_id"].(string)
	task := f.waitTask(id, 8*time.Second)
	if task["status"] != "success" {
		t.Fatalf("任务未成功: %v", task)
	}
	u := usageOf(t, task)
	// 工作模式：规划（1 次）+ 反思（至少 1 次）
	if got := u["llm_calls"].(float64); got < 2 {
		t.Fatalf("工作模式应记录规划与反思调用，实际 %v", got)
	}
	if got := u["tool_calls"].(float64); got < 2 {
		t.Fatalf("应记录 2 次工具调用（写文件 + 回复），实际 %v", got)
	}
}
