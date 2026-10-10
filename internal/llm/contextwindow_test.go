package llm

import "testing"

// 表里认出来的模型要给出**表里的数**，而且要说清来源是表——
// 界面拿这个来源去标注"这个百分比是按什么算的"，标错了就是在替一个兜底值背书。
func TestContextWindowFor_Table(t *testing.T) {
	cases := map[string]int{
		"claude-sonnet-4":      200000,
		"claude-3-5-sonnet":    200000,
		"gpt-4o":               128000,
		"gpt-4o-mini":          128000,
		"gpt-4-turbo":          128000,
		"glm-4-plus":           128000,
		"glm-5.3-flash":        128000,
		"qwen3-coder":          131072,
		"Qwen2.5-72B-Instruct": 131072, // 大小写不敏感
	}
	for model, want := range cases {
		got, src, _ := ContextWindowFor(model)
		if got != want || src != SourceTable {
			t.Errorf("ContextWindowFor(%q) = %d/%s，应为 %d/%s", model, got, src, want, SourceTable)
		}
	}
}

// 带着厂商前缀的名字（从模型列表里拉回来常常长这样）也要认得出来。
func TestContextWindowFor_StripsVendorPrefix(t *testing.T) {
	for _, model := range []string{"zhipu/glm-4-plus", "anthropic.claude-sonnet-4", "openai:gpt-4o"} {
		if got, src, _ := ContextWindowFor(model); src != SourceTable {
			t.Errorf("ContextWindowFor(%q) 没认出来（得到 %d/%s）", model, got, src)
		}
	}
}

// 认不出来就用兜底值，而且**来源必须说成兜底**：
// 拿兜底值算出来的百分比要能被读的人看出"这个数可能不准"。
func TestContextWindowFor_FallsBack(t *testing.T) {
	for _, model := range []string{"", "   ", "some-local-llama-70b"} {
		got, src, note := ContextWindowFor(model)
		if got != DefaultContextWindow || src != SourceDefault {
			t.Errorf("ContextWindowFor(%q) = %d/%s，应为 %d/%s", model, got, src, DefaultContextWindow, SourceDefault)
		}
		if note == "" {
			t.Errorf("ContextWindowFor(%q) 的说明不能是空的：界面要拿它解释分母从哪来", model)
		}
	}
}
