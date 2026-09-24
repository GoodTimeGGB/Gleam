// anthropic.go Anthropic Messages API 协议客户端（自研，纯标准库）。
// 线协议：POST {base}/v1/messages（base 以 /v1 结尾时追加 /messages），
// 鉴权 x-api-key + anthropic-version；system 为顶层字段；
// 非流式解析 content[].text；流式消费 content_block_delta 事件。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// AnthropicClient Anthropic Messages API 客户端。
type AnthropicClient struct {
	BaseURL     string
	APIKey      string
	Model       string
	Temperature float64
	MaxTokens   int
	HTTP        *http.Client
}

// NewAnthropic 创建 Anthropic 客户端。
func NewAnthropic(baseURL, apiKey, model string, temperature float64, maxTokens int, timeout time.Duration) *AnthropicClient {
	return &AnthropicClient{
		BaseURL:     normalizeBase(baseURL, "/v1/messages", "/messages"),
		APIKey:      apiKey,
		Model:       model,
		Temperature: temperature,
		MaxTokens:   maxTokens,
		HTTP:        &http.Client{Timeout: timeout},
	}
}

func (c *AnthropicClient) Name() string { return c.Model }

// endpoint 拼接 messages 路径：base 已含 /v1 时直接追加 /messages。
func (c *AnthropicClient) endpoint() string {
	if strings.HasSuffix(c.BaseURL, "/v1") {
		return c.BaseURL + "/messages"
	}
	return c.BaseURL + "/v1/messages"
}

func (c *AnthropicClient) headers() map[string]string {
	h := map[string]string{
		"anthropic-version": "2023-06-01",
	}
	// 与 GLM/Responses 对齐：空 key 省略鉴权头，空头会被网关判成格式错误
	if c.APIKey != "" {
		h["x-api-key"] = c.APIKey
	}
	return h
}

type anthropicPayload struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      any                `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Temperature float64            `json:"temperature,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
}

// anthropicSystemBlock Anthropic 的 system 支持字符串或内容块数组。
// 用数组形式才能挂 cache_control——这是 Anthropic 与 OpenAI 系最大的差别：
// 前缀缓存必须**显式**标注断点，不标就完全不缓存。
type anthropicSystemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

// cacheControl 缓存断点标记。
type cacheControl struct {
	Type string `json:"type"` // ephemeral
}

// anthropicCacheMinTokens Anthropic 可缓存前缀的下限（约 1024 token）。
// 低于它标注断点没有意义，反而多占一个断点名额。
const anthropicCacheMinTokens = 1024

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (c *AnthropicClient) payload(req ChatRequest, stream bool) anthropicPayload {
	temp := req.Temperature
	if temp == 0 {
		temp = c.Temperature
	}
	maxTok := req.MaxTokens
	if maxTok == 0 {
		maxTok = c.MaxTokens
	}
	if maxTok <= 0 {
		maxTok = 4096 // Anthropic 协议 max_tokens 必填
	}
	msgs := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, anthropicMessage{Role: m.Role, Content: m.Content})
	}
	// 系统提示词够长时标一个缓存断点：后续调用只要前缀一致就能命中，输入按折扣计价。
	var system any
	if req.System != "" {
		if EstimateTokens(req.System) >= anthropicCacheMinTokens {
			system = []anthropicSystemBlock{{
				Type: "text", Text: req.System,
				CacheControl: &cacheControl{Type: "ephemeral"},
			}}
		} else {
			system = req.System
		}
	}
	return anthropicPayload{
		Model: c.Model, MaxTokens: maxTok, System: system, Messages: msgs,
		Temperature: temp, Stream: stream,
	}
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		// CacheReadInputTokens 命中缓存断点的输入 token（按折扣计价）
		CacheReadInputTokens int `json:"cache_read_input_tokens"`
		// CacheCreationInputTokens 本次写入缓存的输入 token（略贵于普通输入，但后续可复用）
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat 非流式调用。
func (c *AnthropicClient) Chat(ctx context.Context, req ChatRequest) (string, error) {
	body, err := json.Marshal(c.payload(req, false))
	if err != nil {
		return "", err
	}
	raw, err := postJSON(ctx, c.HTTP, c.endpoint(), c.headers(), body)
	if err != nil {
		return "", err
	}
	var out anthropicResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("llm: 解析响应失败: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("llm: API 错误: %s", out.Error.Message)
	}
	var b strings.Builder
	for _, part := range out.Content {
		if part.Type == "text" {
			b.WriteString(part.Text)
		}
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("llm: 响应中无内容")
	}
	var u Usage
	if out.Usage != nil {
		u = Usage{
			PromptTokens:     out.Usage.InputTokens + out.Usage.CacheReadInputTokens + out.Usage.CacheCreationInputTokens,
			CompletionTokens: out.Usage.OutputTokens,
			CachedTokens:     out.Usage.CacheReadInputTokens,
		}
	}
	ReportUsage(req, b.String(), u)
	return b.String(), nil
}

// ChatStream 流式调用（SSE：content_block_delta 事件）。
func (c *AnthropicClient) ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (string, error) {
	body, err := json.Marshal(c.payload(req, true))
	if err != nil {
		return "", err
	}
	rc, err := openStream(ctx, c.HTTP, c.endpoint(), c.headers(), body)
	if err != nil {
		return "", err
	}
	defer rc.Close()

	var full strings.Builder
	var u Usage
	// 三级超时看门狗（TTFB / 块间）：只靠 http.Client 的总超时抓不住"吐着吐着停了"
	err = scanStream(ctx, rc, func(line string) bool {
		if !strings.HasPrefix(line, "data:") {
			return true
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			return true
		}
		// Anthropic 流式的 usage 分两处下发：输入侧在 message_start，
		// 输出侧在 message_delta，都得接住才凑得齐（否则只能退化成估算）。
		var ev struct {
			Type    string `json:"type"`
			Message *struct {
				Usage *struct {
					InputTokens              int `json:"input_tokens"`
					OutputTokens             int `json:"output_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage *struct {
				InputTokens              int `json:"input_tokens"`
				OutputTokens             int `json:"output_tokens"`
				CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			} `json:"usage"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			return true
		}
		switch ev.Type {
		case "message_start":
			if ev.Message != nil && ev.Message.Usage != nil {
				mu := ev.Message.Usage
				u.PromptTokens = mu.InputTokens + mu.CacheReadInputTokens + mu.CacheCreationInputTokens
				u.CachedTokens = mu.CacheReadInputTokens
				u.CompletionTokens = mu.OutputTokens
			}
		case "message_delta":
			if ev.Usage != nil {
				if ev.Usage.OutputTokens > 0 {
					u.CompletionTokens = ev.Usage.OutputTokens
				}
				if ev.Usage.InputTokens > 0 {
					u.PromptTokens = ev.Usage.InputTokens + ev.Usage.CacheReadInputTokens + ev.Usage.CacheCreationInputTokens
				}
				if ev.Usage.CacheReadInputTokens > 0 {
					u.CachedTokens = ev.Usage.CacheReadInputTokens
				}
			}
			if ev.Delta.Text != "" {
				full.WriteString(ev.Delta.Text)
				if onDelta != nil {
					onDelta(ev.Delta.Text)
				}
			}
		case "content_block_delta":
			if ev.Delta.Text != "" {
				full.WriteString(ev.Delta.Text)
				if onDelta != nil {
					onDelta(ev.Delta.Text)
				}
			}
		case "message_stop", "message_error", "error":
			return false // 终态事件：正常停止读取
		}
		return true
	})
	if err != nil {
		return full.String(), err
	}
	if full.Len() == 0 {
		return "", fmt.Errorf("llm: 流式响应为空")
	}
	// 拿到了 message_start/message_delta 的 usage 就用真的（含缓存命中量），否则估算兜底。
	ReportUsage(req, full.String(), u)
	return full.String(), nil
}

var _ Client = (*AnthropicClient)(nil)
