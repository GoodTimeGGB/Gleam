package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

// Scripted 是一条按请求类型排队的固定响应脚本。
type Scripted struct {
	Kind  string   `json:"kind"`  // plan | reflect | chat
	Texts []string `json:"texts"` // 依次出队；耗尽后回落到默认/处理器
}

// MockClient 是确定性 LLM 假实现：用于单元测试、端到端自测与离线演示。
type MockClient struct {
	mu         sync.Mutex
	scripts    map[string][]string // kind -> 待消费响应队列
	handler    func(req ChatRequest) string
	Calls      []ChatRequest // 全部调用记录（供断言）
	Model      string
	failNext   error // 若非 nil，下一次调用返回该错误
	failRemain int   // failNext 的剩余生效次数
}

// NewMock 创建 Mock 客户端。
func NewMock() *MockClient {
	return &MockClient{scripts: map[string][]string{}, Model: "mock"}
}

// Enqueue 为指定类型的请求按序注入响应。
func (m *MockClient) Enqueue(kind string, texts ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scripts[kind] = append(m.scripts[kind], texts...)
}

// SetHandler 设置兜底响应函数（队列为空时调用）。
func (m *MockClient) SetHandler(h func(req ChatRequest) string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handler = h
}

// FailNext 令下一次调用返回错误（模拟 LLM 故障）。
func (m *MockClient) FailNext(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNext = err
	m.failRemain = 1
}

// FailNextN 令接下来 n 次调用返回错误（模拟鉴权失败连续重试场景）。
func (m *MockClient) FailNextN(n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNext = err
	m.failRemain = n
}

func (m *MockClient) Name() string { return m.Model }

func (m *MockClient) next(req ChatRequest) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, req)
	if m.failRemain > 0 && m.failNext != nil {
		m.failRemain--
		return "", m.failNext
	}
	kind := KindOf(req.System)
	if q := m.scripts[kind]; len(q) > 0 {
		text := q[0]
		m.scripts[kind] = q[1:]
		return text, nil
	}
	if m.handler != nil {
		return m.handler(req), nil
	}
	return m.defaultReply(kind, req), nil
}

// Chat implements Client.
func (m *MockClient) Chat(ctx context.Context, req ChatRequest) (string, error) {
	text, err := m.next(req)
	if err != nil {
		return "", err
	}
	// Mock 同样上报用量（估算值），保证离线演示与测试的看板数据不缺
	ReportUsage(req, text, Usage{})
	return text, nil
}

// ChatStream implements Client。
func (m *MockClient) ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (string, error) {
	text, err := m.next(req)
	if err != nil {
		return "", err
	}
	ReportUsage(req, text, Usage{})
	// 模拟流式：按 rune 分批回调
	runes := []rune(text)
	for i := 0; i < len(runes); i += 8 {
		end := i + 8
		if end > len(runes) {
			end = len(runes)
		}
		if onDelta != nil {
			onDelta(string(runes[i:end]))
		}
	}
	return text, nil
}

// defaultReply 无脚本时的确定性行为，保证"空配置也能跑通"。
func (m *MockClient) defaultReply(kind string, req ChatRequest) string {
	switch kind {
	case "plan":
		goal := extractGoal(req.System + "\n" + messagesText(req.Messages))
		if goal == "" {
			goal = "未指定目标"
		}
		plan := map[string]any{
			"steps": []map[string]any{{
				"id":          "s1",
				"description": "直接回复用户",
				"tool":        "reply",
				"args":        map[string]any{"text": "（Mock 模式）收到目标：" + goal},
			}},
			"estimated_time": "short",
		}
		b, _ := json.Marshal(plan)
		return string(b)
	case "reflect":
		return `{"score":85,"verdict":"done","reason":"Mock 默认评估：执行无异常"}`
	case "compress":
		// Mock 摘要：确定性抽取式截断，保证离线压缩有可用结果
		text := messagesText(req.Messages)
		runes := []rune(text)
		if len(runes) > 160 {
			runes = runes[:160]
		}
		return "（Mock 摘要）" + strings.TrimSpace(string(runes))
	case "geo":
		// Mock GEO：离线测试返回固定的中等得分建议
		return `{"score":75,"summary":"Mock GEO 分析：结构清晰，建议补充数据支撑","strengths":["段落组织合理","核心概念明确"],"weaknesses":["缺少具体案例","关键词密度偏低"],"actionables":[{"category":"引用性","description":"补充 1-2 个具体数据或案例","priority":"high"},{"category":"关键词","description":"在小标题中自然融入核心术语","priority":"medium"}]}`
	case "chat_mode":
		// Mock 对话：回显用户消息（截断），保证对话模式离线可演示
		text := messagesText(req.Messages)
		runes := []rune(strings.TrimSpace(text))
		if len(runes) > 120 {
			runes = runes[:120]
		}
		return "（Mock 对话）" + string(runes)
	default:
		return "（Mock 响应）"
	}
}

// extractGoal 从提示词中提取 "用户目标：" 行的内容。
func extractGoal(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"用户目标：", "## 用户目标", "用户目标:"} {
			if strings.HasPrefix(line, prefix) {
				v := strings.TrimSpace(strings.TrimPrefix(line, prefix))
				v = strings.Trim(v, ":：")
				if v != "" {
					return v
				}
			}
		}
	}
	return ""
}

func messagesText(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

// LoadScripts 从 JSON 文件加载脚本：[{"kind":"plan","texts":["..."]}]。
func LoadScripts(path string) ([]Scripted, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("mock: 读取脚本失败: %w", err)
	}
	var scripts []Scripted
	if err := json.Unmarshal(data, &scripts); err != nil {
		return nil, fmt.Errorf("mock: 解析脚本失败: %w", err)
	}
	return scripts, nil
}

// Apply 将脚本集装载进 MockClient。
func (m *MockClient) Apply(scripts []Scripted) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range scripts {
		m.scripts[s.Kind] = append(m.scripts[s.Kind], s.Texts...)
	}
}
