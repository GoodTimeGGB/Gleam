// providers.go 内置各大模型厂商官方接入预设与协议工厂。
// 套餐类型（token 按量 / coding 编程包月 / agent 智能体套餐）映射到各厂对应的官方入口；
// 预设仅是默认值，用户可在设置页修改任意字段（以厂商最新文档为准）。
package llm

import (
	"net/url"
	"strings"
	"time"
)

// 协议常量（自定义接入支持的三种线协议）。
const (
	ProtocolOpenAIChat      = "openai_chat"      // OpenAI Chat Completions 兼容（含 GLM/DeepSeek/Kimi/Qwen 等）
	ProtocolOpenAIResponses = "openai_responses" // OpenAI Responses API
	ProtocolAnthropic       = "anthropic"        // Anthropic Messages API
)

// 套餐类型。
const (
	PlanToken  = "token"  // 按量计费（通用入口）
	PlanCoding = "coding" // 编程套餐（包月，独立高优先级入口）
	PlanAgent  = "agent"  // 智能体套餐
)

// PlanPreset 厂商某类套餐的官方入口。
type PlanPreset struct {
	Kind     string `json:"kind"`     // token | coding | agent
	Label    string `json:"label"`    // 显示名（如 "编程套餐 Coding Plan"）
	BaseURL  string `json:"base_url"` // 官方 API 入口
	Protocol string `json:"protocol"` // openai_chat | openai_responses | anthropic
	Model    string `json:"model"`    // 该套餐的默认模型（可改）
}

// ProviderPreset 厂商预设。
type ProviderPreset struct {
	ID    string       `json:"id"`
	Name  string       `json:"name"`
	Plans []PlanPreset `json:"plans"`
}

// Providers 内置厂商目录（2026-09 经官方文档核实；入口或套餐有变动时可在设置页直接改）。
// 依据：火山方舟 docs.volcengine.com/docs/82379/1925114（Coding Plan）、智谱 docs.bigmodel.cn/cn/coding-plan、
// 阿里云 help.aliyun.com/zh/model-studio/coding-plan、MiniMax platform.minimax.io、Kimi platform.kimi.com。
var Providers = []ProviderPreset{
	{
		ID: "zhipu", Name: "智谱 GLM",
		Plans: []PlanPreset{
			{Kind: PlanToken, Label: "按量 Token（OpenAI 兼容）", BaseURL: "https://open.bigmodel.cn/api/paas/v4", Protocol: ProtocolOpenAIChat, Model: "glm-5.3-flash"},
			{Kind: PlanCoding, Label: "GLM Coding Plan 编程套餐（OpenAI 兼容）", BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4", Protocol: ProtocolOpenAIChat, Model: "glm-5.3"},
			{Kind: PlanAgent, Label: "Anthropic 兼容入口（Claude Code 同款）", BaseURL: "https://open.bigmodel.cn/api/anthropic", Protocol: ProtocolAnthropic, Model: "glm-5.3"},
		},
	},
	{
		ID: "deepseek", Name: "DeepSeek 深度求索",
		Plans: []PlanPreset{
			{Kind: PlanToken, Label: "按量 Token（OpenAI 兼容，暂无编程套餐）", BaseURL: "https://api.deepseek.com/v1", Protocol: ProtocolOpenAIChat, Model: "deepseek-chat"},
		},
	},
	{
		ID: "moonshot", Name: "月之暗面 Kimi",
		Plans: []PlanPreset{
			{Kind: PlanToken, Label: "按量 Token（OpenAI 兼容）", BaseURL: "https://api.moonshot.cn/v1", Protocol: ProtocolOpenAIChat, Model: "kimi-k2.6"},
			{Kind: PlanCoding, Label: "Kimi Coding（Anthropic 兼容；托管版经 Kimi Code 登录自动配置）", BaseURL: "https://api.moonshot.cn/anthropic", Protocol: ProtocolAnthropic, Model: "kimi-k2.6"},
		},
	},
	{
		ID: "qwen", Name: "阿里云百炼（通义 Qwen）",
		Plans: []PlanPreset{
			{Kind: PlanToken, Label: "按量 Token（OpenAI 兼容）", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", Protocol: ProtocolOpenAIChat, Model: "qwen3-coder-plus"},
			{Kind: PlanCoding, Label: "百炼 Coding Plan（Anthropic 兼容；Key 为 sk-sp- 开头，与按量不互通）", BaseURL: "https://coding.dashscope.aliyuncs.com/anthropic", Protocol: ProtocolAnthropic, Model: "qwen3-coder-plus"},
			{Kind: PlanAgent, Label: "百炼 Coding Plan（OpenAI 兼容；一个 Key 聚合千问/GLM/Kimi/MiniMax）", BaseURL: "https://coding.dashscope.aliyuncs.com/v1", Protocol: ProtocolOpenAIChat, Model: "qwen3-coder-plus"},
		},
	},
	{
		ID: "volc", Name: "火山方舟（豆包）",
		Plans: []PlanPreset{
			{Kind: PlanToken, Label: "按量 Token（OpenAI 兼容）", BaseURL: "https://ark.cn-beijing.volces.com/api/v3", Protocol: ProtocolOpenAIChat, Model: "doubao-seed-1-6"},
			{Kind: PlanCoding, Label: "方舟 Coding Plan（Lite/Pro，Anthropic 兼容，适配 Claude Code）", BaseURL: "https://ark.cn-beijing.volces.com/api/coding", Protocol: ProtocolAnthropic, Model: "doubao-seed-code"},
		},
	},
	{
		ID: "minimax", Name: "MiniMax",
		Plans: []PlanPreset{
			{Kind: PlanToken, Label: "按量 Token（OpenAI 兼容，国内站）", BaseURL: "https://api.minimaxi.com/v1", Protocol: ProtocolOpenAIChat, Model: "MiniMax-M2.5"},
			{Kind: PlanCoding, Label: "MiniMax Coding Plan（Anthropic 兼容，国内站；国际站为 api.minimax.io）", BaseURL: "https://api.minimaxi.com/anthropic", Protocol: ProtocolAnthropic, Model: "MiniMax-M2.5"},
		},
	},
	{
		ID: "openai", Name: "OpenAI",
		Plans: []PlanPreset{
			{Kind: PlanToken, Label: "按量 Token（Chat Completions）", BaseURL: "https://api.openai.com/v1", Protocol: ProtocolOpenAIChat, Model: "gpt-4.1"},
			{Kind: PlanAgent, Label: "Responses API", BaseURL: "https://api.openai.com/v1", Protocol: ProtocolOpenAIResponses, Model: "gpt-4.1"},
		},
	},
	{
		ID: "anthropic", Name: "Anthropic",
		Plans: []PlanPreset{
			{Kind: PlanToken, Label: "按量 Token（Messages API）", BaseURL: "https://api.anthropic.com/v1", Protocol: ProtocolAnthropic, Model: "claude-sonnet-4-5"},
		},
	},
	{
		ID: "openrouter", Name: "OpenRouter（聚合）",
		Plans: []PlanPreset{
			{Kind: PlanToken, Label: "按量 Token", BaseURL: "https://openrouter.ai/api/v1", Protocol: ProtocolOpenAIChat, Model: "openrouter/auto"},
		},
	},
	{
		ID: "tokendance", Name: "TokenDance 词元跳动（聚合）",
		Plans: []PlanPreset{
			{Kind: PlanToken, Label: "按量 Token（OpenAI 兼容）", BaseURL: "https://tokendance.space/gateway/v1", Protocol: ProtocolOpenAIChat, Model: "gpt-4.1-mini"},
			{Kind: PlanAgent, Label: "Anthropic 兼容入口", BaseURL: "https://tokendance.space/gateway/v1", Protocol: ProtocolAnthropic, Model: "claude-sonnet-4-5"},
		},
	},
}

// KeyScope 把 base_url 归一成"密钥该认的那台主机"（小写 host:port，不含路径）。
//
// 用主机而不是整条 URL：同一家厂商的 /v1、/chat/completions 是不同端点同一把 key，
// 按 URL 绑会把用户每次改路径都变成"密钥丢了"。也故意不认 provider_id：自建网关
// 一个主机转发多家、同一家多个域名的情况都有，真正决定"这把 key 发给谁"的是主机。
// 解析不出来（空串、相对路径）就退回 TrimSpace 后的原值——宁可把它当成一个独立主机，
// 也不要把两行不同的配置判成同一台。
func KeyScope(baseURL string) string {
	s := strings.TrimSpace(baseURL)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return strings.ToLower(s)
	}
	return strings.ToLower(u.Host)
}

// FindProvider 按 ID 查厂商预设。
func FindProvider(id string) *ProviderPreset {
	id = strings.TrimSpace(id)
	for i := range Providers {
		if Providers[i].ID == id {
			return &Providers[i]
		}
	}
	return nil
}

// ResolvePreset 依据厂商与套餐解析官方入口；用户显式填写的 base_url/model 优先。
// 返回 (baseURL, model, protocol)。
func ResolvePreset(providerID, plan, baseURL, model string) (string, string, string) {
	p := FindProvider(providerID)
	if p == nil {
		return baseURL, model, ""
	}
	kind := plan
	if kind == "" {
		kind = PlanToken
	}
	var chosen *PlanPreset
	for i := range p.Plans {
		if p.Plans[i].Kind == kind {
			chosen = &p.Plans[i]
			break
		}
	}
	if chosen == nil {
		chosen = &p.Plans[0]
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = chosen.BaseURL
	}
	if strings.TrimSpace(model) == "" {
		model = chosen.Model
	}
	return baseURL, model, chosen.Protocol
}

// ValidProtocol 协议合法性。
func ValidProtocol(p string) bool {
	switch p {
	case ProtocolOpenAIChat, ProtocolOpenAIResponses, ProtocolAnthropic:
		return true
	}
	return false
}

// ResolveTarget 把"用户填了什么"算成"实际往哪发"：预设解析 + 协议回退，一步到位。
//
// 之前这三步（ResolvePreset → 预设协议为空则沿用已配协议 → 再兜底 openai_chat）
// 在启动装配、设置保存、连接自测三处各抄一遍，而密钥作用域恰恰要问"到底是哪台主机"：
// 三份里任何一份漏了兜底，就会算出两个不同的 scope。收到这里来，三处只问一次。
func ResolveTarget(providerID, plan, baseURL, model, protocol string) (string, string, string) {
	base, model, presetProto := ResolvePreset(providerID, plan, baseURL, model)
	if presetProto != "" {
		protocol = presetProto
	}
	if !ValidProtocol(protocol) {
		protocol = ProtocolOpenAIChat
	}
	return base, model, protocol
}

// New 协议工厂：按线协议构造对应客户端（全部自研实现）。timeoutSecs<=0 时用 60s。
func New(protocol, baseURL, apiKey, model string, temperature float64, maxTokens, timeoutSecs int) Client {
	if timeoutSecs <= 0 {
		timeoutSecs = 60
	}
	timeout := time.Duration(timeoutSecs) * time.Second
	switch protocol {
	case ProtocolOpenAIResponses:
		return NewResponses(baseURL, apiKey, model, temperature, maxTokens, timeout)
	case ProtocolAnthropic:
		return NewAnthropic(baseURL, apiKey, model, temperature, maxTokens, timeout)
	default:
		return NewGLM(baseURL, apiKey, model, temperature, maxTokens, timeout)
	}
}
