// models.go 在线拉取厂商可用模型列表（尽力而为）：
// 配置模型时免记模型 ID，从列表点选；个别 Coding/Agent 套餐网关不实现该端点，
// 拉不到时前端仍可手输，所以列表是加速器而不是唯一入口。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ModelInfo 模型列表条目。
type ModelInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	OwnedBy     string `json:"owned_by,omitempty"`
}

// modelEndpoint 按协议拼列表端点（与对话端点同一套 base 归一化，防双拼）。
func modelEndpoint(protocol, baseURL string) string {
	switch protocol {
	case ProtocolAnthropic:
		base := normalizeBase(baseURL, "/v1/messages", "/messages", "/v1/models", "/models")
		if strings.HasSuffix(base, "/v1") {
			return base + "/models"
		}
		return base + "/v1/models"
	default: // openai_chat / openai_responses 共用 OpenAI 风格 /models
		base := normalizeBase(baseURL, "/chat/completions", "/responses", "/models")
		return base + "/models"
	}
}

func modelHeaders(protocol, apiKey string) map[string]string {
	h := map[string]string{}
	switch protocol {
	case ProtocolAnthropic:
		h["anthropic-version"] = "2023-06-01"
		if apiKey != "" {
			h["x-api-key"] = apiKey
		}
	default:
		if apiKey != "" {
			h["Authorization"] = "Bearer " + apiKey
		}
	}
	return h
}

// getList GET 一次并读响应体；非 200 包成 statusError 供上层分类。
func getList(ctx context.Context, hc *http.Client, url string, headers map[string]string) ([]byte, error) {
	noteEgress(url, 0)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
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

// parseModelsResponse 兼容三种下发形态：OpenAI 的 data[]、Anthropic 的 data[]（带 display_name）、
// 智谱 v4 的 models[]；同 ID 去重，保持厂商顺序。
func parseModelsResponse(raw []byte) ([]ModelInfo, error) {
	var entry struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
		OwnedBy     string `json:"owned_by"`
	}
	var payload struct {
		Data   []json.RawMessage `json:"data"`
		Models []json.RawMessage `json:"models"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("llm: 解析模型列表失败: %w", err)
	}
	if payload.Error != nil {
		return nil, fmt.Errorf("llm: API 错误: %s", payload.Error.Message)
	}
	items := payload.Data
	if len(items) == 0 {
		items = payload.Models
	}
	out := make([]ModelInfo, 0, len(items))
	seen := map[string]bool{}
	for _, it := range items {
		if err := json.Unmarshal(it, &entry); err != nil {
			continue // 个别条目形态异常不拖垮整表
		}
		id := entry.ID
		if id == "" {
			id = entry.Name
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, ModelInfo{ID: id, DisplayName: entry.DisplayName, OwnedBy: entry.OwnedBy})
	}
	return out, nil
}

// ListModels 按协议拉取可用模型列表。超时由 ctx 与 timeout 共同约束（<=0 时 15s）。
func ListModels(ctx context.Context, protocol, baseURL, apiKey string, timeout time.Duration) ([]ModelInfo, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	hc := &http.Client{Timeout: timeout, Transport: Transport()}
	raw, err := getList(ctx, hc, modelEndpoint(protocol, baseURL), modelHeaders(protocol, apiKey))
	if err != nil {
		return nil, err
	}
	return parseModelsResponse(raw)
}
