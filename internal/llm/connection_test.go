// connection_test.go 模型连接自测：base_url 拼接容错、空 Key 鉴权头、重试分类。
// 这三类是配置模型时最容易踩的坑，全部用本地 httptest 假网关验证，不触网。
package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// echoHandler 记录每次请求的路径/头，固定回一个各协议都能解析的最小成功体。
type hitLog struct {
	paths   []string
	headers []http.Header
}

func (h *hitLog) handler(status int, respBody string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.paths = append(h.paths, r.URL.Path)
		h.headers = append(h.headers, r.Header.Clone())
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}
}

const glmOK = `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`
const anthOK = `{"content":[{"type":"text","text":"ok"}]}`
const respOK = `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`

func chatReq() ChatRequest {
	return ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}}
}

// base_url 误填成完整端点（用户从文档里整段复制）时不能双拼出 404。
func TestBaseURL_EndpointSuffixDedup(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		newCli func(base string) Client
		req    func(Client) error
		want   string
	}{
		{"glm", glmOK, func(b string) Client { return NewGLM(b, "k", "m", 0, 0, 5*time.Second) },
			func(c Client) error { _, e := c.Chat(context.Background(), chatReq()); return e }, "/chat/completions"},
		{"glm-尾斜杠", glmOK, func(b string) Client { return NewGLM(b, "k", "m", 0, 0, 5*time.Second) },
			func(c Client) error { _, e := c.Chat(context.Background(), chatReq()); return e }, "/chat/completions"},
		{"responses", respOK, func(b string) Client { return NewResponses(b, "k", "m", 0, 0, 5*time.Second) },
			func(c Client) error { _, e := c.Chat(context.Background(), chatReq()); return e }, "/responses"},
		{"anthropic-v1结尾", anthOK, func(b string) Client { return NewAnthropic(b, "k", "m", 0, 0, 5*time.Second) },
			func(c Client) error { _, e := c.Chat(context.Background(), chatReq()); return e }, "/v1/messages"},
		{"anthropic-完整端点", anthOK, func(b string) Client { return NewAnthropic(b, "k", "m", 0, 0, 5*time.Second) },
			func(c Client) error { _, e := c.Chat(context.Background(), chatReq()); return e }, "/v1/messages"},
	}
	// 每个用例的输入 base：故意带尾斜杠 / 已含端点路径
	bases := map[string]string{
		"glm":            "%s/chat/completions",
		"glm-尾斜杠":        "%s/chat/completions/",
		"responses":      "%s/responses",
		"anthropic-v1结尾": "%s/v1",
		"anthropic-完整端点": "%s/v1/messages",
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var h hitLog
			srv := httptest.NewServer(h.handler(http.StatusOK, tc.body))
			defer srv.Close()
			base := srv.URL
			if f, ok := bases[tc.name]; ok {
				base = fmt.Sprintf(f, srv.URL)
			}
			c := tc.newCli(base)
			if err := tc.req(c); err != nil {
				t.Fatalf("Chat 失败: %v", err)
			}
			if len(h.paths) != 1 || h.paths[0] != tc.want {
				t.Errorf("请求路径 = %v，期望恰好 1 次 %s", h.paths, tc.want)
			}
		})
	}
}

// 空 Key 时三家必须一致地省略鉴权头（Anthropic 原来会发空头 x-api-key，网关直接 400/403）。
func TestEmptyKey_OmitsAuthHeaders(t *testing.T) {
	t.Run("glm", func(t *testing.T) {
		var h hitLog
		srv := httptest.NewServer(h.handler(http.StatusOK, glmOK))
		defer srv.Close()
		c := NewGLM(srv.URL, "", "m", 0, 0, 5*time.Second)
		if _, err := c.Chat(context.Background(), chatReq()); err != nil {
			t.Fatal(err)
		}
		if got := h.headers[0].Get("Authorization"); got != "" {
			t.Errorf("空 key 不应发 Authorization，得到 %q", got)
		}
	})
	t.Run("responses", func(t *testing.T) {
		var h hitLog
		srv := httptest.NewServer(h.handler(http.StatusOK, respOK))
		defer srv.Close()
		c := NewResponses(srv.URL, "", "m", 0, 0, 5*time.Second)
		if _, err := c.Chat(context.Background(), chatReq()); err != nil {
			t.Fatal(err)
		}
		if got := h.headers[0].Get("Authorization"); got != "" {
			t.Errorf("空 key 不应发 Authorization，得到 %q", got)
		}
	})
	t.Run("anthropic", func(t *testing.T) {
		var h hitLog
		srv := httptest.NewServer(h.handler(http.StatusOK, anthOK))
		defer srv.Close()
		c := NewAnthropic(srv.URL, "", "m", 0, 0, 5*time.Second)
		if _, err := c.Chat(context.Background(), chatReq()); err != nil {
			t.Fatal(err)
		}
		if _, ok := h.headers[0]["X-Api-Key"]; ok {
			t.Errorf("空 key 不应发 x-api-key 头（空头会被网关判为格式错误）")
		}
		if h.headers[0].Get("anthropic-version") == "" {
			t.Error("anthropic-version 与 key 无关，必须始终发送")
		}
	})
}

// 鉴权失败(401)不重试；限流(429)只重试一次。走 Anthropic 验证共享重试层。
func TestRetryPolicy_StatusClass(t *testing.T) {
	t.Run("401不重试", func(t *testing.T) {
		var h hitLog
		srv := httptest.NewServer(h.handler(http.StatusUnauthorized, ""))
		defer srv.Close()
		c := NewAnthropic(srv.URL, "k", "m", 0, 0, 5*time.Second)
		if _, err := c.Chat(context.Background(), chatReq()); err == nil {
			t.Fatal("应返回错误")
		}
		if len(h.paths) != 1 {
			t.Errorf("401 应只请求 1 次，实际 %d 次", len(h.paths))
		}
	})
	t.Run("429重试一次", func(t *testing.T) {
		var h hitLog
		var n int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.paths = append(h.paths, r.URL.Path)
			n++
			if n == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(anthOK))
		}))
		defer srv.Close()
		c := NewAnthropic(srv.URL, "k", "m", 0, 0, 5*time.Second)
		if _, err := c.Chat(context.Background(), chatReq()); err != nil {
			t.Fatalf("429 后第二次成功应吞掉错误: %v", err)
		}
		if len(h.paths) != 2 {
			t.Errorf("429 应重试恰好一次，实际 %d 次", len(h.paths))
		}
	})
}

// 非 200 但响应体是合法 JSON 错误时，错误信息要能透传出去（排障靠它）。
func TestErrorSnippet_PropagatesStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"model not found"}}`))
	}))
	defer srv.Close()
	c := NewGLM(srv.URL, "k", "m", 0, 0, 5*time.Second)
	_, err := c.Chat(context.Background(), chatReq())
	if err == nil {
		t.Fatal("404 应报错")
	}
	var se *statusError
	if !errors.As(err, &se) {
		t.Fatalf("应包装为 statusError，实际 %T: %v", err, err)
	}
	if se.code != http.StatusNotFound {
		t.Errorf("code = %d", se.code)
	}
}
