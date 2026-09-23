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

// ---------- 前缀长度工具 ----------

func TestStablePrefixLen_CountsRunes(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "abc", 0},
		{"abc", "abc", 3},
		{"abc", "abd", 2},
		{"中文前缀相同，后面不同", "中文前缀相同，后面也不一样", 9},
		{"", "", 0},
	}
	for _, c := range cases {
		if got := StablePrefixLen(c.a, c.b); got != c.want {
			t.Errorf("StablePrefixLen(%q, %q) = %d，期望 %d", c.a, c.b, got, c.want)
		}
	}
}

// TestStablePrefixLen_DoesNotSplitMultibyte 公共前缀必须整字切分：
// 按字节切会把一个汉字劈成半个，比出来的长度没有意义。
func TestStablePrefixLen_DoesNotSplitMultibyte(t *testing.T) {
	a := "稳定段"
	b := "稳定端" // 第三个字不同
	stable, restA, restB := SplitCacheBoundary(a, b)
	if stable != "稳定" {
		t.Errorf("公共前缀应为「稳定」，实际 %q", stable)
	}
	if restA != "段" || restB != "端" {
		t.Errorf("剩余部分应整字切分，实际 %q / %q", restA, restB)
	}
}

// ---------- OpenAI 兼容协议（GLM / DeepSeek / Qwen…） ----------

func TestGLM_ParsesOpenAIStyleCachedTokens(t *testing.T) {
	body := `{"choices":[{"message":{"role":"assistant","content":"好的"}}],
		"usage":{"prompt_tokens":1000,"completion_tokens":50,
		"prompt_tokens_details":{"cached_tokens":800}}}`
	u := usageFromChatBody(t, body)
	if u.PromptTokens != 1000 || u.CompletionTokens != 50 {
		t.Fatalf("基础用量解析错误: %+v", u)
	}
	if u.CachedTokens != 800 {
		t.Errorf("应解析出 OpenAI 风格的 cached_tokens=800，实际 %d", u.CachedTokens)
	}
	if got := u.CacheHitRate(); got < 0.79 || got > 0.81 {
		t.Errorf("命中率应约 0.8，实际 %v", got)
	}
}

func TestGLM_ParsesDeepSeekStyleCachedTokens(t *testing.T) {
	body := `{"choices":[{"message":{"role":"assistant","content":"好的"}}],
		"usage":{"prompt_tokens":1000,"completion_tokens":50,"prompt_cache_hit_tokens":600}}`
	u := usageFromChatBody(t, body)
	if u.CachedTokens != 600 {
		t.Errorf("应解析出 DeepSeek 风格的 prompt_cache_hit_tokens=600，实际 %d", u.CachedTokens)
	}
}

// TestGLM_DeepSeekFieldWinsWhenBothPresent 两种叫法同时出现时以顶层字段为准，
// 避免把两个口径的数字加在一起。
func TestGLM_DeepSeekFieldWinsWhenBothPresent(t *testing.T) {
	body := `{"choices":[{"message":{"role":"assistant","content":"x"}}],
		"usage":{"prompt_tokens":1000,"completion_tokens":10,
		"prompt_cache_hit_tokens":600,"prompt_tokens_details":{"cached_tokens":700}}}`
	if u := usageFromChatBody(t, body); u.CachedTokens != 600 {
		t.Errorf("同时出现时应取顶层字段 600，实际 %d", u.CachedTokens)
	}
}

func TestGLM_NoCacheFieldIsZero(t *testing.T) {
	body := `{"choices":[{"message":{"role":"assistant","content":"x"}}],
		"usage":{"prompt_tokens":1000,"completion_tokens":10}}`
	u := usageFromChatBody(t, body)
	if u.CachedTokens != 0 {
		t.Errorf("厂商没返回缓存字段时应为 0（表示未知，不是未命中），实际 %d", u.CachedTokens)
	}
	if u.CacheHitRate() != 0 {
		t.Errorf("无缓存数据时命中率应为 0，实际 %v", u.CacheHitRate())
	}
}

// TestGLM_StreamRequestsUsage 流式请求要主动索要 usage——
// 规划走的就是流式，不主动要就永远只能估算，缓存命中量也就无从得知。
func TestGLM_StreamRequestsUsage(t *testing.T) {
	c := NewGLM("http://x", "", "m", 0.3, 100, 0)
	p := c.payload(ChatRequest{System: "s"}, true)
	if p.StreamOptions == nil || !p.StreamOptions.IncludeUsage {
		t.Fatal("流式请求应带 stream_options.include_usage")
	}
	if q := c.payload(ChatRequest{System: "s"}, false); q.StreamOptions != nil {
		t.Error("非流式请求不该带 stream_options")
	}
}

// TestGLM_StreamUsageReachesHook 流式收尾块里的 usage（含缓存命中）要真正上报，
// 不能像以前那样一律丢掉、退化成估算。
func TestGLM_StreamUsageReachesHook(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"你"}}]}`,
		`data: {"choices":[{"delta":{"content":"好"}}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":900,"completion_tokens":2,"prompt_cache_hit_tokens":700}}`,
		`data: [DONE]`,
		"",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}))
	defer srv.Close()

	got := captureUsage(t, func() {
		c := NewGLM(srv.URL, "", "m", 0.3, 100, 5*time.Second)
		text, err := c.ChatStream(context.Background(), ChatRequest{TaskID: "t1"}, nil)
		if err != nil {
			t.Fatalf("流式调用失败: %v", err)
		}
		if text != "你好" {
			t.Fatalf("应拼接出完整文本，实际 %q", text)
		}
	})
	if got.PromptTokens != 900 || got.CachedTokens != 700 {
		t.Errorf("流式 usage 应带缓存命中量，实际 %+v", got)
	}
	if got.Estimated {
		t.Error("拿到了真 usage 就不该标成估算")
	}
}

// TestGLM_StreamOptionsRejectedFallsBack 个别厂商不认 stream_options 会直接 400：
// 必须自动退回再试一次，并且此后不再发该字段——不能为了拿准数字把主链路弄挂。
func TestGLM_StreamOptionsRejectedFallsBack(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		bodies = append(bodies, m)
		if _, has := m["stream_options"]; has {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unknown field stream_options"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	c := NewGLM(srv.URL, "", "m", 0.3, 100, 5*time.Second)
	for i := 0; i < 2; i++ {
		if _, err := c.ChatStream(context.Background(), ChatRequest{}, nil); err != nil {
			t.Fatalf("第 %d 次调用应成功（退回不带 stream_options），实际 %v", i+1, err)
		}
	}
	if len(bodies) != 3 {
		t.Fatalf("应共发出 3 次请求（1 次被拒 + 1 次退回 + 第 2 轮直接退回），实际 %d", len(bodies))
	}
	if _, has := bodies[2]["stream_options"]; has {
		t.Error("被拒过一次后就不该再发 stream_options")
	}
}

// ---------- Anthropic ----------

func TestAnthropic_ParsesCacheReadTokens(t *testing.T) {
	body := `{"content":[{"type":"text","text":"好的"}],
		"usage":{"input_tokens":100,"output_tokens":20,
		"cache_read_input_tokens":800,"cache_creation_input_tokens":200}}`
	u := usageFromAnthropicBody(t, body)
	// Anthropic 的 input_tokens 不含缓存部分，总输入要三项相加才与 OpenAI 口径一致
	if u.PromptTokens != 1100 {
		t.Errorf("总输入应为 100+800+200=1100，实际 %d", u.PromptTokens)
	}
	if u.CachedTokens != 800 {
		t.Errorf("命中量应取 cache_read_input_tokens=800，实际 %d", u.CachedTokens)
	}
}

// TestAnthropic_SystemGetsCacheBreakpoint 长系统提示词要挂 cache_control 断点：
// Anthropic 的前缀缓存是显式的，不标就完全不缓存。
func TestAnthropic_SystemGetsCacheBreakpoint(t *testing.T) {
	c := NewAnthropic("http://x", "", "m", 0.3, 100, 0)
	long := strings.Repeat("规则", 1200) // 远超 1024 token 下限
	p := c.payload(ChatRequest{System: long}, false)
	blocks, ok := p.System.([]anthropicSystemBlock)
	if !ok || len(blocks) != 1 {
		t.Fatalf("长系统提示词应转成内容块数组，实际 %#v", p.System)
	}
	if blocks[0].CacheControl == nil || blocks[0].CacheControl.Type != "ephemeral" {
		t.Error("应挂上 ephemeral 缓存断点")
	}
	if blocks[0].Text != long {
		t.Error("断点块里的文本必须与原文完全一致，否则缓存永远不命中")
	}
}

func TestAnthropic_ShortSystemStaysPlainString(t *testing.T) {
	c := NewAnthropic("http://x", "", "m", 0.3, 100, 0)
	p := c.payload(ChatRequest{System: "短提示词"}, false)
	if _, ok := p.System.(string); !ok {
		t.Errorf("短提示词不值得占缓存断点，应保持字符串形式，实际 %#v", p.System)
	}
}

// ---------- Responses 协议 ----------

func TestResponses_ParsesCachedTokens(t *testing.T) {
	body := `{"output_text":"好的","usage":{"input_tokens":1000,"output_tokens":20,
		"input_tokens_details":{"cached_tokens":900}}}`
	var out responsesOutput
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.Usage == nil || out.Usage.InputTokensDetails == nil {
		t.Fatal("应解析出 input_tokens_details")
	}
	if got := out.Usage.InputTokensDetails.CachedTokens; got != 900 {
		t.Errorf("缓存命中应为 900，实际 %d", got)
	}
}

// ---------- 测试辅助 ----------

// usageFromChatBody 用假的 chat completions 响应驱动 GLMClient，取回它上报的 Usage。
func usageFromChatBody(t *testing.T, body string) Usage {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	return captureUsage(t, func() {
		c := NewGLM(srv.URL, "", "m", 0.3, 100, 5*time.Second)
		if _, err := c.Chat(context.Background(), ChatRequest{}); err != nil {
			t.Fatalf("调用失败: %v", err)
		}
	})
}

func usageFromAnthropicBody(t *testing.T, body string) Usage {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	return captureUsage(t, func() {
		c := NewAnthropic(srv.URL, "", "m", 0.3, 100, 5*time.Second)
		if _, err := c.Chat(context.Background(), ChatRequest{}); err != nil {
			t.Fatalf("调用失败: %v", err)
		}
	})
}

// captureUsage 临时挂一个用量钩子，收集 fn 执行期间上报的 Usage。
// 钩子只能注册不能注销（全局广播表），所以这里依赖"包内测试顺序执行"这一事实：
// 读取发生在 fn 返回后、下一次调用之前，不会被别的用例串味。
func captureUsage(t *testing.T, fn func()) Usage {
	t.Helper()
	var got Usage
	AddUsageHook(func(_, _ string, _ ChatRequest, _ string, u Usage) { got = u })
	fn()
	return got
}
