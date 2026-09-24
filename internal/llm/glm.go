package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// GLMClient 通过 OpenAI 兼容协议调用 GLM 系列模型（也兼容 DeepSeek、Qwen 等服务）。
type GLMClient struct {
	BaseURL     string // 如 https://open.bigmodel.cn/api/paas/v4
	APIKey      string
	Model       string
	Temperature float64
	MaxTokens   int
	HTTP        *http.Client
	// noStreamUsage 置位后不再请求流式 usage：个别厂商不认 stream_options 字段会直接 400，
	// 首次被拒就永久退回"估算用量"，不能为了拿准数字而把主链路弄挂。
	noStreamUsage atomic.Bool
}

// NewGLM 创建 GLM 客户端。
func NewGLM(baseURL, apiKey, model string, temperature float64, maxTokens int, timeout time.Duration) *GLMClient {
	return &GLMClient{
		BaseURL:     normalizeBase(baseURL, "/chat/completions"),
		APIKey:      apiKey,
		Model:       model,
		Temperature: temperature,
		MaxTokens:   maxTokens,
		HTTP:        &http.Client{Timeout: timeout},
	}
}

func (c *GLMClient) Name() string { return c.Model }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequestPayload struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream"`
	// StreamOptions 请求流式响应附带 usage：规划走的是流式，不主动要就永远只能估算，
	// 缓存命中量也就无从得知（估算值里没有缓存这一项）。
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

// streamOptions OpenAI 兼容协议的流式扩展字段。
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatChoice struct {
	Message      *chatMessage `json:"message"`
	Delta        *chatMessage `json:"delta"`
	FinishReason string       `json:"finish_reason"`
}

// chatUsage OpenAI 兼容协议的 usage 字段。
// 缓存命中量有两种叫法：OpenAI/GLM 放在 prompt_tokens_details.cached_tokens，
// DeepSeek 放在顶层的 prompt_cache_hit_tokens，两个都解析。
type chatUsage struct {
	PromptTokens         int `json:"prompt_tokens"`
	CompletionTokens     int `json:"completion_tokens"`
	TotalTokens          int `json:"total_tokens"`
	PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
	PromptTokensDetails  *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// cached 返回缓存命中的输入 token 数（厂商未返回时为 0）。
func (u *chatUsage) cached() int {
	if u == nil {
		return 0
	}
	if u.PromptCacheHitTokens > 0 {
		return u.PromptCacheHitTokens
	}
	if u.PromptTokensDetails != nil {
		return u.PromptTokensDetails.CachedTokens
	}
	return 0
}

// toUsage 转成统一的 Usage。
func (u *chatUsage) toUsage() Usage {
	if u == nil {
		return Usage{}
	}
	return Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		CachedTokens:     u.cached(),
	}
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Usage   *chatUsage   `json:"usage"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *GLMClient) buildMessages(req ChatRequest) []chatMessage {
	msgs := make([]chatMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, chatMessage{Role: RoleSystem, Content: req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, chatMessage{Role: m.Role, Content: m.Content})
	}
	return msgs
}

func (c *GLMClient) payload(req ChatRequest, stream bool) chatRequestPayload {
	temp := req.Temperature
	if temp == 0 {
		temp = c.Temperature
	}
	maxTok := req.MaxTokens
	if maxTok == 0 {
		maxTok = c.MaxTokens
	}
	p := chatRequestPayload{
		Model:       c.Model,
		Messages:    c.buildMessages(req),
		Temperature: temp,
		MaxTokens:   maxTok,
		Stream:      stream,
	}
	if stream && !c.noStreamUsage.Load() {
		p.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	return p
}

// statusError 携带 HTTP 状态码的调用错误（用于瞬时故障重试判定）。
type statusError struct {
	code int
	msg  string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("llm: HTTP %d: %s", e.code, e.msg)
}

func (c *GLMClient) post(ctx context.Context, payload chatRequestPayload) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("llm: 序列化请求失败: %w", err)
	}
	resp, err := c.do(ctx, body)
	if err != nil && retryable(ctx, err) {
		// 瞬时故障自动重试一次（自研韧性）：网络抖动/限流/网关抖动不直接打断任务
		select {
		case <-ctx.Done():
		case <-time.After(400 * time.Millisecond):
			if resp2, err2 := c.do(ctx, body); err2 == nil {
				return resp2, nil
			}
		}
	}
	return resp, err
}

// retryable 判定是否值得重试：传输层错误，或限流/网关类状态码。
func retryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var se *statusError
	if errors.As(err, &se) {
		return se.code == http.StatusTooManyRequests ||
			se.code == http.StatusBadGateway ||
			se.code == http.StatusServiceUnavailable ||
			se.code == http.StatusGatewayTimeout
	}
	return true // 传输层错误（连接拒绝、超时、DNS 抖动等）
}

// do 发起一次请求（每次重建 body reader，支持安全重试）。
func (c *GLMClient) do(ctx context.Context, body []byte) (*http.Response, error) {
	// 出网留痕：GLM 客户端自己走 HTTP（不经 httputil 的共享助手），所以这里单独记一笔。
	noteEgress(c.BaseURL+"/chat/completions", len(body))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm: 请求失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, &statusError{code: resp.StatusCode, msg: strings.TrimSpace(string(snippet))}
	}
	return resp, nil
}

// Chat 非流式调用。
func (c *GLMClient) Chat(ctx context.Context, req ChatRequest) (string, error) {
	resp, err := c.post(ctx, c.payload(req, false))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", fmt.Errorf("llm: 解析响应失败: %w", err)
	}
	if cr.Error != nil {
		return "", fmt.Errorf("llm: API 错误: %s", cr.Error.Message)
	}
	var u Usage
	if cr.Usage != nil {
		u = cr.Usage.toUsage()
	}
	for _, ch := range cr.Choices {
		if ch.Message != nil {
			ReportUsage(req, ch.Message.Content, u)
			return ch.Message.Content, nil
		}
	}
	return "", fmt.Errorf("llm: 响应中无内容")
}

// ChatStream 流式调用（SSE）。
func (c *GLMClient) ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (string, error) {
	resp, err := c.post(ctx, c.payload(req, true))
	if err != nil {
		// 厂商不认 stream_options 时会直接 400：退回不带该字段再试一次，并记住别再发。
		if c.wantsStreamUsage() && rejectedStreamOptions(err) {
			c.noStreamUsage.Store(true)
			resp, err = c.post(ctx, c.payload(req, true))
		}
		if err != nil {
			return "", err
		}
	}
	defer resp.Body.Close()

	var full strings.Builder
	var streamUsage *chatUsage
	// 三级超时看门狗（TTFB / 块间）：只靠 http.Client 的总超时抓不住"吐着吐着停了"
	err = scanStream(ctx, resp.Body, func(line string) bool {
		if !strings.HasPrefix(line, "data:") {
			return true
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			return true
		}
		if data == "[DONE]" {
			return false
		}
		var cr chatResponse
		if err := json.Unmarshal([]byte(data), &cr); err != nil {
			return true // 跳过无法解析的行
		}
		// 收尾块通常 choices 为空、只带 usage（开了 stream_options 才有）
		if cr.Usage != nil {
			streamUsage = cr.Usage
		}
		for _, ch := range cr.Choices {
			if ch.Delta != nil && ch.Delta.Content != "" {
				full.WriteString(ch.Delta.Content)
				if onDelta != nil {
					onDelta(ch.Delta.Content)
				}
			}
		}
		return true
	})
	if err != nil {
		return full.String(), err
	}
	if full.Len() == 0 {
		return "", fmt.Errorf("llm: 流式响应为空")
	}
	// 拿到真 usage 就用真的（含缓存命中量）；没拿到交给 ReportUsage 估算。
	ReportUsage(req, full.String(), streamUsage.toUsage())
	return full.String(), nil
}

// wantsStreamUsage 当前是否正在请求流式 usage。
func (c *GLMClient) wantsStreamUsage() bool { return !c.noStreamUsage.Load() }

// rejectedStreamOptions 判断错误是否为"请求体字段不被接受"（400/422）。
// 鉴权失败、限流、超时都不算——那些重试也不会变好。
func rejectedStreamOptions(err error) bool {
	var se *statusError
	if !errors.As(err, &se) {
		return false
	}
	return se.code == http.StatusBadRequest || se.code == http.StatusUnprocessableEntity
}
