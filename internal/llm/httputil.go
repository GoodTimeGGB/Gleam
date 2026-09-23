// httputil.go 共享 HTTP JSON 工具：统一瞬时故障重试（传输层错误 + 限流/网关状态码）。
// 与 GLMClient 的重试语义一致，供 Responses / Anthropic 等协议客户端复用。
package llm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

const maxBodyBytes = 16 << 20 // 16MB 上限，防异常响应撑爆内存

func postJSONOnce(ctx context.Context, hc *http.Client, url string, headers map[string]string, body []byte) ([]byte, error) {
	// 出网留痕：只记主机名与请求字节数（见 egress.go 的边界说明）。
	// 放在真正发包之前——记的是"发出去了什么"，不是"收到了什么"。
	noteEgress(url, len(body))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm: 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, &statusError{code: resp.StatusCode, msg: string(snippet)}
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
}

// postJSON POST JSON 并返回响应体；瞬时故障自动重试一次。
func postJSON(ctx context.Context, hc *http.Client, url string, headers map[string]string, body []byte) ([]byte, error) {
	out, err := postJSONOnce(ctx, hc, url, headers, body)
	if err != nil && retryable(ctx, err) {
		select {
		case <-ctx.Done():
		case <-time.After(400 * time.Millisecond):
			if out2, err2 := postJSONOnce(ctx, hc, url, headers, body); err2 == nil {
				return out2, nil
			}
		}
	}
	return out, err
}

func openStreamOnce(ctx context.Context, hc *http.Client, url string, headers map[string]string, body []byte) (io.ReadCloser, error) {
	// 出网留痕：流式与非流式走同一条记账口径，否则"流式调用查不到留痕"。
	noteEgress(url, len(body))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm: 请求失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, &statusError{code: resp.StatusCode, msg: string(snippet)}
	}
	return resp.Body, nil
}

// openStream POST JSON 并返回 SSE 流；瞬时故障自动重试一次。
func openStream(ctx context.Context, hc *http.Client, url string, headers map[string]string, body []byte) (io.ReadCloser, error) {
	rc, err := openStreamOnce(ctx, hc, url, headers, body)
	if err != nil && retryable(ctx, err) {
		select {
		case <-ctx.Done():
		case <-time.After(400 * time.Millisecond):
			if rc2, err2 := openStreamOnce(ctx, hc, url, headers, body); err2 == nil {
				return rc2, nil
			}
		}
	}
	return rc, err
}
