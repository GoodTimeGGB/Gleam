// Package llm 封装大模型客户端：统一接口 + GLM（OpenAI 兼容协议）实现 + 测试 Mock。
package llm

import (
	"context"
	"strings"
	"sync"
	"unicode"
)

// Role 常量。
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message 对话消息。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest 一次对话请求。
type ChatRequest struct {
	System      string    // 系统提示词
	Messages    []Message // 历史（不含系统提示）
	Temperature float64
	MaxTokens   int
	TaskID      string // 可选：调用归属的任务，用于把用量统计到具体任务
}

// Usage 一次模型调用的 token 用量。
// 厂商未返回 usage 字段时（多数流式接口如此）用 EstimateTokens 估算，Estimated 为 true。
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	// CachedTokens 输入 token 中命中服务端提示词缓存的部分。
	// 前缀缓存（KV cache 复用）命中后输入按折扣计价，是「同样的活少花钱」最直接的一项。
	// 厂商不返回该字段时为 0，表示未命中或无从得知——不能据此断定"没缓存"。
	CachedTokens int
	Estimated    bool
}

// Total 本次调用的总 token。
func (u Usage) Total() int { return u.PromptTokens + u.CompletionTokens }

// CacheHitRate 缓存命中率（0-1）：命中 token 占输入 token 的比例。输入为 0 时返回 0。
func (u Usage) CacheHitRate() float64 {
	if u.PromptTokens <= 0 || u.CachedTokens <= 0 {
		return 0
	}
	if u.CachedTokens > u.PromptTokens {
		return 1
	}
	return float64(u.CachedTokens) / float64(u.PromptTokens)
}

// EstimateTokens 粗略估算文本的 token 数：CJK 约 1 token/字，其余约 4 字符/token。
// 只用于展示量级，不用于计费。
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	cjk, other := 0, 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			cjk++
		} else if !unicode.IsSpace(r) {
			other++
		}
	}
	return cjk + (other+3)/4
}

// usageHook 用量上报回调：引擎侧按任务累加，供「消耗看板」展示。
type usageHook func(taskID, kind string, req ChatRequest, text string, u Usage)

var (
	usageMu    sync.RWMutex
	usageSinks []usageHook
)

// AddUsageHook 注册用量回调（可多次注册，按注册顺序广播）。
// 引擎按 taskID 认领自己的调用，未归属的用量由各订阅者自行忽略。
func AddUsageHook(h func(taskID, kind string, req ChatRequest, text string, u Usage)) {
	if h == nil {
		return
	}
	usageMu.Lock()
	usageSinks = append(usageSinks, h)
	usageMu.Unlock()
}

// ReportUsage 上报一次调用用量：客户端实现者在拿到响应后调用；
// 自定义客户端（含测试替身）也应调用它，否则该调用不计入消耗看板。
// u 为零值时回退到估算，保证看板不会因为厂商不返回 usage 就缺数据。
func ReportUsage(req ChatRequest, text string, u Usage) {
	if u.PromptTokens == 0 && u.CompletionTokens == 0 {
		var b strings.Builder
		b.WriteString(req.System)
		for _, m := range req.Messages {
			b.WriteString(m.Content)
		}
		u = Usage{
			PromptTokens:     EstimateTokens(b.String()),
			CompletionTokens: EstimateTokens(text),
			Estimated:        true,
		}
	}
	usageMu.RLock()
	sinks := usageSinks
	usageMu.RUnlock()
	kind := KindOf(req.System)
	for _, h := range sinks {
		h(req.TaskID, kind, req, text, u)
	}
}

// Client 是 LLM 客户端的统一抽象。glm.Client 与 MockClient 均实现它。
type Client interface {
	// Chat 非流式调用，返回助手回复全文。
	Chat(ctx context.Context, req ChatRequest) (string, error)
	// ChatStream 流式调用（SSE），onDelta 在收到增量时被回调；返回全文。
	ChatStream(ctx context.Context, req ChatRequest, onDelta func(delta string)) (string, error)
	// Name 返回模型标识（用于日志与调试）。
	Name() string
}

// 供 MockClient 与测试识别请求用途的标记。规划器/反思器/压缩器/技能优化器/GEO分析器的
// 系统提示词包含这些标记。
const (
	MarkerPlan     = "[GLEAM-TASK:PLAN]"
	MarkerReflect  = "[GLEAM-TASK:REFLECT]"
	MarkerCompress = "[GLEAM-TASK:COMPRESS]"   // 上下文自动压缩摘要
	MarkerSkillFix = "[GLEAM-TASK:SKILL_FIX]"  // 技能失败后自动优化参数
	MarkerChat     = "[GLEAM-TASK:CHAT]"       // 对话模式直连问答
	MarkerGEO      = "[GLEAM-TASK:GEO]"        // 生成式引擎优化分析
	MarkerToolPick = "[GLEAM-TASK:TOOL_PICK]"  // 能力菜单下的工具快筛
	MarkerReview   = "[GLEAM-TASK:REVIEW]"     // 执行前的动作快筛（审核模型）
	MarkerChatChk  = "[GLEAM-TASK:CHAT_CHECK]" // 对话模式回答后的独立自检
)

// KindOf 依据系统提示词判断请求用途：plan | reflect | compress | skill_fix | geo | chat | 对话。
func KindOf(system string) string {
	switch {
	case contains(system, MarkerPlan):
		return "plan"
	case contains(system, MarkerReflect):
		return "reflect"
	case contains(system, MarkerCompress):
		return "compress"
	case contains(system, MarkerSkillFix):
		return "skill_fix"
	case contains(system, MarkerGEO):
		return "geo"
	case contains(system, MarkerToolPick):
		return "tool_pick"
	case contains(system, MarkerReview):
		return "review"
	case contains(system, MarkerChatChk):
		return "chat_check"
	case contains(system, MarkerChat):
		return "chat_mode"
	default:
		return "chat"
	}
}

// IsMockClient 判断客户端是否为 Mock（自动优化等仅真实模型启用的能力据此跳过，
// 保证离线自测的确定性）。
func IsMockClient(c Client) bool {
	_, ok := c.(*MockClient)
	return ok
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
