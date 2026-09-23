package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------- 厂商预设与套餐解析 ----------

func TestResolvePreset_ZhipuCoding(t *testing.T) {
	base, model, protocol := ResolvePreset("zhipu", PlanCoding, "", "")
	if base != "https://open.bigmodel.cn/api/coding/paas/v4" {
		t.Errorf("coding 入口 = %s", base)
	}
	if protocol != ProtocolOpenAIChat || model == "" {
		t.Errorf("protocol=%s model=%s", protocol, model)
	}
	// 显式填写优先于预设
	base2, model2, _ := ResolvePreset("zhipu", PlanToken, "https://custom.example/v4", "my-model")
	if base2 != "https://custom.example/v4" || model2 != "my-model" {
		t.Errorf("显式值应优先: %s %s", base2, model2)
	}
	// 未知厂商回落原值
	base3, _, _ := ResolvePreset("nope", "token", "https://x/v1", "m")
	if base3 != "https://x/v1" {
		t.Errorf("未知厂商 = %s", base3)
	}
	// 各厂商都有 token 套餐与合法协议
	for _, p := range Providers {
		if FindProvider(p.ID) == nil {
			t.Errorf("FindProvider(%s) 失败", p.ID)
		}
		found := false
		for _, pl := range p.Plans {
			if !ValidProtocol(pl.Protocol) {
				t.Errorf("%s/%s 协议非法: %s", p.ID, pl.Kind, pl.Protocol)
			}
			if pl.Kind == PlanToken {
				found = true
			}
		}
		if !found {
			t.Errorf("%s 缺少 token 套餐", p.ID)
		}
	}
}

// ---------- OpenAI Responses 协议 ----------

func TestResponsesClient_Chat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["model"] != "test-model" || req["instructions"] == "" {
			t.Errorf("payload = %v", req)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{{
				"type": "message",
				"content": []map[string]any{
					{"type": "output_text", "text": "你好"},
					{"type": "output_text", "text": "，世界"},
				},
			}},
		})
	}))
	defer srv.Close()

	c := NewResponses(srv.URL, "key", "test-model", 0.2, 128, 5*time.Second)
	out, err := c.Chat(context.Background(), ChatRequest{System: "sys", Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if out != "你好，世界" {
		t.Errorf("out = %q", out)
	}
}

func TestResponsesClient_ChatStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"流\"}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"式输出\"}\n\n" +
			"data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer srv.Close()

	c := NewResponses(srv.URL, "key", "test-model", 0.2, 128, 5*time.Second)
	var sb strings.Builder
	out, err := c.ChatStream(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}}, func(d string) { sb.WriteString(d) })
	if err != nil {
		t.Fatal(err)
	}
	if out != "流式输出" || sb.String() != "流式输出" {
		t.Errorf("out=%q delta=%q", out, sb.String())
	}
}

// ---------- Anthropic 协议 ----------

func TestAnthropicClient_Chat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "key" || r.Header.Get("anthropic-version") == "" {
			t.Error("缺少鉴权头")
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["system"] != "sys" || req["max_tokens"] == float64(0) {
			t.Errorf("payload = %v", req)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "Claude 回复"}},
		})
	}))
	defer srv.Close()

	c := NewAnthropic(srv.URL, "key", "claude-test", 0.2, 128, 5*time.Second)
	out, err := c.Chat(context.Background(), ChatRequest{System: "sys", Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if out != "Claude 回复" {
		t.Errorf("out = %q", out)
	}
}

func TestAnthropicClient_ChatStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"安\"}}\n\n" +
			"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"心\"}}\n\n" +
			"data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer srv.Close()

	c := NewAnthropic(srv.URL, "key", "claude-test", 0.2, 128, 5*time.Second)
	out, err := c.ChatStream(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "安心" {
		t.Errorf("out = %q", out)
	}
}

func TestFactory_ProtocolSelection(t *testing.T) {
	if _, ok := New(ProtocolOpenAIChat, "https://x", "k", "m", 0, 0, 0).(*GLMClient); !ok {
		t.Error("openai_chat 应构造 GLMClient")
	}
	if _, ok := New(ProtocolOpenAIResponses, "https://x", "k", "m", 0, 0, 0).(*ResponsesClient); !ok {
		t.Error("openai_responses 应构造 ResponsesClient")
	}
	if _, ok := New(ProtocolAnthropic, "https://x", "k", "m", 0, 0, 0).(*AnthropicClient); !ok {
		t.Error("anthropic 应构造 AnthropicClient")
	}
	if _, ok := New("", "https://x", "k", "m", 0, 0, 0).(*GLMClient); !ok {
		t.Error("空协议默认 openai_chat")
	}
}
