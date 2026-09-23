package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------- 数据出网留痕（P1-3） ----------

// TestEgressHookFiresOnChat 模型调用必须留痕——提示词是实打实发出去的，
// 这是"本地优先"最需要自证的一条路径。
func TestEgressHookFiresOnChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	var host string
	var nbytes int
	SetEgressHook(func(h string, n int) { host, nbytes = h, n })
	defer SetEgressHook(nil)

	c := NewGLM(srv.URL, "k", "m", 0.3, 100, 5*time.Second)
	if _, err := c.Chat(context.Background(), ChatRequest{
		System:   "s",
		Messages: []Message{{Role: RoleUser, Content: "机密内容-不该出现在留痕里"}},
	}); err != nil {
		t.Fatalf("Chat 应成功: %v", err)
	}

	if host == "" {
		t.Fatal("模型调用发生了却没有出网留痕")
	}
	if !strings.Contains(host, "127.0.0.1") {
		t.Errorf("留痕应记下目标主机，实际 %q", host)
	}
	if nbytes == 0 {
		t.Error("留痕应记下请求字节数")
	}
	// 回调签名只给了主机名与字节数，这条断言是防止以后有人"顺手"把 body 也传进来。
	if strings.Contains(host, "机密内容") {
		t.Errorf("留痕不该包含内容：%q", host)
	}
}

// TestEgressHookRecordsStreamingToo 流式与非流式必须同一条记账口径。
// 只给非流式留痕会留下一个安静的盲区：对话模式走的就是流式。
func TestEgressHookRecordsStreamingToo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	hits := 0
	SetEgressHook(func(string, int) { hits++ })
	defer SetEgressHook(nil)

	c := NewGLM(srv.URL, "k", "m", 0.3, 100, 5*time.Second)
	_, _ = c.ChatStream(context.Background(), ChatRequest{
		System: "s", Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}, nil)

	if hits == 0 {
		t.Error("流式调用也必须留痕，否则对话模式是一条无痕出网路径")
	}
}

// TestEgressHookOffByDefault 没装钩子时一切照旧——审计是旁路，
// 不能成为模型调用的前置条件。
func TestEgressHookOffByDefault(t *testing.T) {
	SetEgressHook(nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	c := NewGLM(srv.URL, "k", "m", 0.3, 100, 5*time.Second)
	if _, err := c.Chat(context.Background(), ChatRequest{
		System: "s", Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("没装钩子时也应正常调用: %v", err)
	}
}
