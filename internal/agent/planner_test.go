package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"gleam/internal/config"
	"gleam/internal/llm"
)

func testCtx() context.Context { return context.Background() }

// TestValidateFallbackSharesConfigDefault MaxSteps<=0 时的兜底上限必须与 config 同源。
//
// 原先 planner 与 config 各写死一个 12，改一处忘一处就成了两份边界——
// 而且是"改大了 config 却没改 planner"这种最难发现的形态：
// 只在 MaxSteps 被显式置 0 时才暴露，而默认路径上看不出来。
// 这个测试钉住的是"两者是同一个数"，不是"这个数等于多少"。
func TestValidateFallbackSharesConfigDefault(t *testing.T) {
	p := &Planner{Reg: menuRegistry(2)} // MaxSteps 未设 → 走兜底

	planJSON := func(n int) string {
		ids := make([]string, 0, n)
		for i := 0; i < n; i++ {
			ids = append(ids, fmt.Sprintf(`{"id":"s%d","tool":"reply","args":{"text":"x"}}`, i+1))
		}
		return `{"steps":[` + strings.Join(ids, ",") + `]}`
	}

	if _, err := p.validate([]byte(planJSON(config.DefaultMaxSteps)), "g"); err != nil {
		t.Fatalf("恰好 %d 步应当通过，实际 %v", config.DefaultMaxSteps, err)
	}
	_, err := p.validate([]byte(planJSON(config.DefaultMaxSteps+1)), "g")
	if err == nil {
		t.Fatalf("超过 %d 步应当被拒", config.DefaultMaxSteps)
	}
	if !strings.Contains(err.Error(), fmt.Sprint(config.DefaultMaxSteps)) {
		t.Errorf("报错里应写出上限 %d（说明用的是共享常量），实际 %v", config.DefaultMaxSteps, err)
	}
}

func TestExtractJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"a":1}`, `{"a":1}`},
		{"```json\n{\"a\": 1}\n```", `{"a": 1}`},
		{"前置说明 {\"a\":{\"b\":\"}\"}} 后缀", `{"a":{"b":"}"}}`},
		{"{\"text\":\"引号\\\"与括号{后继续\"}", `{"text":"引号\"与括号{后继续"}`},
	}
	for i, c := range cases {
		got, err := extractJSON(c.in)
		if err != nil {
			t.Errorf("case %d: %v", i, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("case %d: got %q want %q", i, got, c.want)
		}
	}
	if _, err := extractJSON("没有 JSON"); err == nil {
		t.Error("应报错")
	}
	if _, err := extractJSON("{\"未闭合\": 1"); err == nil {
		t.Error("未闭合应报错")
	}
}

func TestPlanner_ValidPlan(t *testing.T) {
	p := &Planner{
		LLM: mockReturning(`{"steps":[
			{"id":"s1","description":"列目录","tool":"echo","args":{"in":"x"}},
			{"id":"s2","description":"回复","tool":"reply","args":{"text":"完成"},"depends_on":["s1"]}
		],"estimated_time":"short"}`),
		Reg: newTestRegistry(),
	}
	plan, err := p.Plan(testCtx(), "目标", "/ws", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 2 || plan.Steps[1].DependsOn[0] != "s1" {
		t.Errorf("plan = %+v", plan)
	}
	if plan.Steps[0].ID != "s1" || plan.Steps[0].Tool != "echo" {
		t.Errorf("step0 = %+v", plan.Steps[0])
	}
}

func TestPlanner_AutoAssignIDs(t *testing.T) {
	p := &Planner{
		LLM: mockReturning(`{"steps":[
			{"tool":"echo","args":{"in":"a"}},
			{"tool":"echo","args":{"in":"b"}}
		]}`),
		Reg: newTestRegistry(),
	}
	plan, err := p.Plan(testCtx(), "g", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Steps[0].ID != "s1" || plan.Steps[1].ID != "s2" {
		t.Errorf("自动 ID = %s,%s", plan.Steps[0].ID, plan.Steps[1].ID)
	}
}

func TestPlanner_ValidationFailures(t *testing.T) {
	cases := []struct {
		name   string
		output string
		expect string
	}{
		{"非JSON", "抱歉我无法输出 JSON", "JSON"},
		{"未知工具", `{"steps":[{"id":"s1","tool":"nope"}]}`, "不存在"},
		{"缺必填参数", `{"steps":[{"id":"s1","tool":"echo","args":{}}]}`, "缺少"},
		{"空步骤", `{"steps":[]}`, "没有步骤"},
		{"循环依赖", `{"steps":[{"id":"s1","tool":"echo","args":{"in":"a"},"depends_on":["s2"]},{"id":"s2","tool":"echo","args":{"in":"b"},"depends_on":["s1"]}]}`, "循环"},
		{"自依赖", `{"steps":[{"id":"s1","tool":"echo","args":{"in":"a"},"depends_on":["s1"]}]}`, "自身"},
		{"依赖不存在", `{"steps":[{"id":"s1","tool":"echo","args":{"in":"a"},"depends_on":["s9"]}]}`, "不存在"},
		{"类型错误", `{"steps":[{"id":"s1","tool":"echo","args":{"in":123}}]}`, "字符串"},
	}
	for _, c := range cases {
		p := &Planner{LLM: mockReturning(c.output), Reg: newTestRegistry()}
		_, err := p.Plan(testCtx(), "g", "", nil, nil, "", "")
		if err == nil {
			t.Errorf("%s: 应报错", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.expect) {
			t.Errorf("%s: 错误 %q 不含 %q", c.name, err.Error(), c.expect)
		}
	}
}

func TestPlanner_FencedOutput(t *testing.T) {
	out := "好的，下面是计划：\n```json\n{\"steps\":[{\"id\":\"s1\",\"tool\":\"reply\",\"args\":{\"text\":\"hi\"}}]}\n```\n以上。"
	p := &Planner{LLM: mockReturning(out), Reg: newTestRegistry()}
	plan, err := p.Plan(testCtx(), "g", "", nil, nil, "", "")
	if err != nil {
		t.Fatalf("应容忍代码块: %v", err)
	}
	if plan.Steps[0].Tool != "reply" {
		t.Error("步骤错误")
	}
}

// mockOneShot 返回固定输出的最小 LLM 客户端。
type mockOneShot struct{ text string }

func (m *mockOneShot) Chat(_ context.Context, _ llm.ChatRequest) (string, error) { return m.text, nil }
func (m *mockOneShot) ChatStream(_ context.Context, _ llm.ChatRequest, onDelta func(string)) (string, error) {
	if onDelta != nil {
		onDelta(m.text)
	}
	return m.text, nil
}
func (m *mockOneShot) Name() string { return "mock-one-shot" }

// mockReturning 构造固定输出的 Mock。
func mockReturning(text string) *mockOneShot { return &mockOneShot{text: text} }
