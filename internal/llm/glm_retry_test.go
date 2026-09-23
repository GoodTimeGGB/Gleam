package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// GLM 客户端瞬时故障自动重试（丝滑：网关抖动不直接打断任务）。

func TestGLM_RetriesOnTransientStatus(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("bad gateway"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "恢复后返回"}}},
		})
	}))
	defer srv.Close()

	c := NewGLM(srv.URL, "key", "test-model", 0.2, 128, 5*time.Second)
	out, err := c.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if out != "恢复后返回" {
		t.Errorf("out = %q", out)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("调用次数 = %d，应为 2", got)
	}
}

func TestGLM_NoRetryOnClientError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("bad key"))
	}))
	defer srv.Close()

	c := NewGLM(srv.URL, "bad", "test-model", 0.2, 128, 5*time.Second)
	_, err := c.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err == nil {
		t.Fatal("401 应返回错误")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("401 不应重试，调用次数 = %d", got)
	}
}
