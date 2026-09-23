// Package geo 提供生成式引擎优化（Generative Engine Optimization）能力：
// 评估创作产出在大模型 / AI 问答引擎中的可发现性、可引用性与推荐价值，
// 并给出可操作的优化建议。
package geo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gleam/internal/llm"
)

// Suggestion 一次 GEO 分析的结构化结果。
type Suggestion struct {
	Score       int      `json:"score"`       // 综合得分 0-100
	Summary     string   `json:"summary"`     // 总体评价
	Strengths   []string `json:"strengths"`   // 已做好的地方
	Weaknesses  []string `json:"weaknesses"`  // 待优化的地方
	Actionables []Action `json:"actionables"` // 可执行的改写建议
}

// Action 一条可操作的优化建议。
type Action struct {
	Category    string `json:"category"`    // 结构 | 语义 | 引用性 | 关键词
	Description string `json:"description"` // 具体怎么改
	Priority    string `json:"priority"`    // high | medium | low
}

// Analyzer GEO 分析器。零值不可用，请用 NewAnalyzer 构造。
type Analyzer struct {
	LLM    llm.Client
	TaskID string // 任务 ID：把这次分析的用量归集到消耗看板
}

// NewAnalyzer 创建分析器。client 为 nil 时 Analyze 将返回错误。
func NewAnalyzer(client llm.Client) *Analyzer {
	return &Analyzer{LLM: client}
}

// Analyze 对内容做 GEO 评估。content 为创作产出正文。
func (a *Analyzer) Analyze(ctx context.Context, content string) (*Suggestion, error) {
	if a == nil || a.LLM == nil {
		return nil, fmt.Errorf("geo: 未配置模型客户端")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("geo: 待分析内容为空")
	}

	req := llm.ChatRequest{
		System:      systemPrompt(),
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: userPrompt(content)}},
		Temperature: 0.3,
		TaskID:      a.TaskID,
	}
	raw, err := a.LLM.Chat(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("geo: 模型调用失败: %w", err)
	}
	s, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// systemPrompt 构建 GEO 分析的系统提示词。必须带 llm.MarkerGEO，
// 供 MockClient 与调用统计识别用途。
func systemPrompt() string {
	return "你是 Gleam 的生成式引擎优化（GEO）分析器。" + llm.MarkerGEO + `

你的任务：评估一段内容在大模型 / AI 问答引擎中被检索、引用、推荐的可能性，并给出改进建议。

评估维度：
1. 结构：标题层级、段落切分、要点是否可独立摘出
2. 语义：核心概念是否自解释，是否过度依赖未言明的上下文
3. 引用性：是否有具体数据、事实、案例、来源，可被验证与转述
4. 关键词：核心术语与长尾表达的自然覆盖度

只输出一个 JSON 对象，不要输出解释文字或代码块标记。字段：
{"score":0-100 整数,"summary":"一句话总体评价","strengths":["..."],"weaknesses":["..."],
 "actionables":[{"category":"结构|语义|引用性|关键词","description":"具体怎么改","priority":"high|medium|low"}]}

要求：strengths / weaknesses 指向具体段落或句子，不要空泛；actionables 每条都要能直接照着改，最多 5 条。`
}

func userPrompt(content string) string {
	return "## 待分析内容\n\n" + content
}

// Parse 从模型输出中提取 GEO 建议，容忍代码块包裹与前后缀说明文字。
func Parse(raw string) (*Suggestion, error) {
	jsonStr := extractJSON(raw)
	if jsonStr == "" {
		return nil, fmt.Errorf("geo: 响应中未找到 JSON: %s", llmShorten(raw, 200))
	}
	var s Suggestion
	if err := json.Unmarshal([]byte(jsonStr), &s); err != nil {
		return nil, fmt.Errorf("geo: 解析响应失败: %w (原文 %s)", err, llmShorten(raw, 200))
	}
	s.normalize()
	return &s, nil
}

// extractJSON 抽取首个 JSON 对象：优先 ```json 代码块，其次首 { 到末 }。
func extractJSON(raw string) string {
	if i := strings.Index(raw, "```"); i >= 0 {
		rest := raw[i+3:]
		if j := strings.Index(rest, "\n"); j >= 0 {
			rest = rest[j+1:]
		}
		if k := strings.Index(rest, "```"); k >= 0 {
			if inner := strings.TrimSpace(rest[:k]); strings.HasPrefix(inner, "{") {
				return inner
			}
		}
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		return strings.TrimSpace(raw[start : end+1])
	}
	return ""
}

// normalize 收敛越界与非法枚举值，保证前端渲染不会拿到脏数据。
func (s *Suggestion) normalize() {
	if s.Score < 0 {
		s.Score = 0
	}
	if s.Score > 100 {
		s.Score = 100
	}
	for i := range s.Actionables {
		switch s.Actionables[i].Priority {
		case "high", "medium", "low":
		default:
			s.Actionables[i].Priority = "medium"
		}
		if s.Actionables[i].Category == "" {
			s.Actionables[i].Category = "语义"
		}
	}
}

// Format 把结构化建议渲染为纯文本，供 OnSuggestion 通道与 CLI 展示。
func Format(s *Suggestion) string {
	if s == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "GEO 优化建议（当前 %d/100）\n%s\n", s.Score, s.Summary)
	if len(s.Strengths) > 0 {
		b.WriteString("\n已做好：\n")
		for _, v := range s.Strengths {
			fmt.Fprintf(&b, "  · %s\n", v)
		}
	}
	if len(s.Weaknesses) > 0 {
		b.WriteString("\n待优化：\n")
		for _, v := range s.Weaknesses {
			fmt.Fprintf(&b, "  · %s\n", v)
		}
	}
	if len(s.Actionables) > 0 {
		b.WriteString("\n可以这样改：\n")
		for i, act := range s.Actionables {
			fmt.Fprintf(&b, "  %d. [%s/%s] %s\n", i+1, act.Category, priorityLabel(act.Priority), act.Description)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func priorityLabel(p string) string {
	switch p {
	case "high":
		return "高"
	case "low":
		return "低"
	default:
		return "中"
	}
}

// Principles 返回创作时应遵循的 GEO 准则，注入到创作类角色的系统提示词中，
// 让内容在生成阶段就对生成式引擎友好，而不是事后补救。
func Principles() string {
	return `
【生成式引擎优化准则（GEO）】
产出内容时请遵循，使其更易被大模型检索、引用与推荐：
1. 结论前置：开头用 1-2 句给出核心结论，便于被摘录引用。
2. 结构可切分：多用小标题与要点列表，让每段都能独立成立。
3. 概念自解释：首次出现的术语当场解释，不依赖未言明的上下文。
4. 事实可验证：关键论断配具体数据、时间、案例或来源。
5. 术语自然覆盖：核心概念与其常见同义表达都出现，不堆砌关键词。`
}

func llmShorten(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
