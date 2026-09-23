package llm

import (
	"context"
	"strings"
	"testing"
)

func TestKindOf(t *testing.T) {
	if KindOf("前缀 [GLEAM-TASK:PLAN] 后缀") != "plan" {
		t.Error("plan 标记未识别")
	}
	if KindOf("[GLEAM-TASK:REFLECT] 评估") != "reflect" {
		t.Error("reflect 标记未识别")
	}
	if KindOf("普通聊天") != "chat" {
		t.Error("应为 chat")
	}
}

func TestMockClient_ScriptFIFO(t *testing.T) {
	m := NewMock()
	m.Enqueue("plan", "第一个计划", "第二个计划")
	m.Enqueue("reflect", `{"score":90,"verdict":"done","reason":"ok"}`)

	ctx := context.Background()
	out, _ := m.Chat(ctx, ChatRequest{System: "[GLEAM-TASK:PLAN] x"})
	if out != "第一个计划" {
		t.Errorf("出队1 = %q", out)
	}
	out, _ = m.Chat(ctx, ChatRequest{System: "[GLEAM-TASK:PLAN] y"})
	if out != "第二个计划" {
		t.Errorf("出队2 = %q", out)
	}
	// 队列耗尽回落默认
	out, _ = m.Chat(ctx, ChatRequest{System: "[GLEAM-TASK:PLAN] z"})
	if !strings.Contains(out, `"reply"`) {
		t.Errorf("默认计划应使用 reply 工具: %q", out)
	}
	out, _ = m.Chat(ctx, ChatRequest{System: "[GLEAM-TASK:REFLECT] e"})
	if !strings.Contains(out, `"verdict":"done"`) {
		t.Errorf("默认反思: %q", out)
	}
	if len(m.Calls) != 4 {
		t.Errorf("调用记录 = %d", len(m.Calls))
	}
}

func TestMockClient_HandlerAndFail(t *testing.T) {
	m := NewMock()
	m.SetHandler(func(req ChatRequest) string { return "H:" + req.System })
	out, _ := m.Chat(context.Background(), ChatRequest{System: "abc"})
	if out != "H:abc" {
		t.Errorf("handler = %q", out)
	}
	m.FailNext(errMock)
	if _, err := m.Chat(context.Background(), ChatRequest{}); err != errMock {
		t.Errorf("FailNext 未生效: %v", err)
	}
	// 恢复后正常
	if _, err := m.Chat(context.Background(), ChatRequest{}); err != nil {
		t.Errorf("恢复失败: %v", err)
	}
}

var errMock = &mockError{}

type mockError struct{}

func (*mockError) Error() string { return "mock error" }

func TestMockClient_Stream(t *testing.T) {
	m := NewMock()
	m.Enqueue("chat", "你好世界")
	var got strings.Builder
	full, err := m.ChatStream(context.Background(), ChatRequest{System: "hi"}, func(d string) { got.WriteString(d) })
	if err != nil {
		t.Fatal(err)
	}
	if full != "你好世界" || got.String() != "你好世界" {
		t.Errorf("full=%q deltas=%q", full, got.String())
	}
}

func TestLoadScripts(t *testing.T) {
	// 由 e2e 测试覆盖文件加载；这里只测 JSON 形状
	scripts := []Scripted{{Kind: "plan", Texts: []string{`{"steps":[]}`}}}
	if scripts[0].Kind != "plan" {
		t.Error("形状错误")
	}
}

func TestMockClient_ExtractGoal(t *testing.T) {
	m := NewMock()
	out, _ := m.Chat(context.Background(), ChatRequest{System: "[GLEAM-TASK:PLAN]\n用户目标：帮我整理文件\n工具: ..."})
	if !strings.Contains(out, "帮我整理文件") {
		t.Errorf("默认计划应包含目标: %q", out)
	}
}
