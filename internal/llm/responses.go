// responses.go OpenAI Responses API 协议客户端（自研，纯标准库）。
// 线协议：POST {base}/responses，system 走 instructions，消息走 input 数组；
// 非流式解析 output[].content[].output_text；流式消费 response.output_text.delta 事件。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ResponsesClient OpenAI Responses API 客户端。
type ResponsesClient struct {
	BaseURL     string
	APIKey      string
	Model       string
	Temperature float64
	MaxTokens   int
	HTTP        *http.Client
}

// NewResponses 创建 Responses API 客户端。
func NewResponses(baseURL, apiKey, model string, temperature float64, maxTokens int, timeout time.Duration) *ResponsesClient {
	return &ResponsesClient{
		BaseURL:     strings.TrimRight(baseURL, "/"),
		APIKey:      apiKey,
		Model:       model,
		Temperature: temperature,
		MaxTokens:   maxTokens,
		HTTP:        &http.Client{Timeout: timeout},
	}
}

func (c *ResponsesClient) Name() string { return c.Model }

func (c *ResponsesClient) headers() map[string]string {
	h := map[string]string{}
	if c.APIKey != "" {
		h["Authorization"] = "Bearer " + c.APIKey
	}
	return h
}

type responsesPayload struct {
	Model           string             `json:"model"`
	Instructions    string             `json:"instructions,omitempty"`
	Input           []responsesMessage `json:"input"`
	Temperature     float64            `json:"temperature,omitempty"`
	MaxOutputTokens int                `json:"max_output_tokens,omitempty"`
	Stream          bool               `json:"stream,omitempty"`
}

type responsesMessage struct {
	Role    string           `json:"role"`
	Content []map[string]any `json:"content"`
}

func (c *ResponsesClient) payload(req ChatRequest, stream bool) responsesPayload {
	temp := req.Temperature
	if temp == 0 {
		temp = c.Temperature
	}
	maxTok := req.MaxTokens
	if maxTok == 0 {
		maxTok = c.MaxTokens
	}
	in := make([]responsesMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		in = append(in, responsesMessage{Role: m.Role, Content: []map[string]any{
			{"type": "input_text", "text": m.Content},
		}})
	}
	return responsesPayload{
		Model: c.Model, Instructions: req.System, Input: in,
		Temperature: temp, MaxOutputTokens: maxTok, Stream: stream,
	}
}

type responsesOutput struct {
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	OutputText string `json:"output_text"`
	Usage      *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
		// InputTokensDetails 缓存命中量（OpenAI Responses 风格）
		InputTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// textFromOutput 聚合 output 数组中的 output_text 片段；空时回退顶层 output_text。
// 同时返回响应中的 token 用量（缺失时为零值，由 reportUsage 估算兜底）。
func textFromOutput(raw []byte) (string, Usage, error) {
	var out responsesOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", Usage{}, fmt.Errorf("llm: 解析响应失败: %w", err)
	}
	if out.Error != nil {
		return "", Usage{}, fmt.Errorf("llm: API 错误: %s", out.Error.Message)
	}
	var u Usage
	if out.Usage != nil {
		u = Usage{PromptTokens: out.Usage.InputTokens, CompletionTokens: out.Usage.OutputTokens}
		if out.Usage.InputTokensDetails != nil {
			u.CachedTokens = out.Usage.InputTokensDetails.CachedTokens
		}
	}
	var b strings.Builder
	for _, item := range out.Output {
		for _, part := range item.Content {
			if part.Type == "output_text" {
				b.WriteString(part.Text)
			}
		}
	}
	if s := strings.TrimSpace(b.String()); s != "" {
		return s, u, nil
	}
	if s := strings.TrimSpace(out.OutputText); s != "" {
		return s, u, nil
	}
	return "", u, fmt.Errorf("llm: 响应中无内容")
}

// Chat 非流式调用。
func (c *ResponsesClient) Chat(ctx context.Context, req ChatRequest) (string, error) {
	body, err := json.Marshal(c.payload(req, false))
	if err != nil {
		return "", err
	}
	raw, err := postJSON(ctx, c.HTTP, c.BaseURL+"/responses", c.headers(), body)
	if err != nil {
		return "", err
	}
	text, u, err := textFromOutput(raw)
	if err != nil {
		return "", err
	}
	ReportUsage(req, text, u)
	return text, nil
}

// ChatStream 流式调用（SSE：response.output_text.delta 事件）。
func (c *ResponsesClient) ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (string, error) {
	body, err := json.Marshal(c.payload(req, true))
	if err != nil {
		return "", err
	}
	rc, err := openStream(ctx, c.HTTP, c.BaseURL+"/responses", c.headers(), body)
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
		var ev struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
			// response.completed 事件带完整 response（含 usage），
			// 不接住它就只能退化成估算，缓存命中量也就无从得知。
			Response *struct {
				Usage *struct {
					InputTokens        int `json:"input_tokens"`
					OutputTokens       int `json:"output_tokens"`
					InputTokensDetails *struct {
						CachedTokens int `json:"cached_tokens"`
					} `json:"input_tokens_details"`
				} `json:"usage"`
			} `json:"response"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			return true
		}
		switch ev.Type {
		case "response.output_text.delta":
			if ev.Delta != "" {
				full.WriteString(ev.Delta)
				if onDelta != nil {
					onDelta(ev.Delta)
				}
			}
		case "response.completed", "response.failed", "response.incomplete":
			if ev.Response != nil && ev.Response.Usage != nil {
				ru := ev.Response.Usage
				u = Usage{PromptTokens: ru.InputTokens, CompletionTokens: ru.OutputTokens}
				if ru.InputTokensDetails != nil {
					u.CachedTokens = ru.InputTokensDetails.CachedTokens
				}
			}
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
	// 拿到 response.completed 的 usage 就用真的（含缓存命中量），否则估算兜底。
	ReportUsage(req, full.String(), u)
	return full.String(), nil
}

var _ Client = (*ResponsesClient)(nil)
