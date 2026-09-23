package types

import "testing"

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens("你好世界"); got != 4 {
		t.Errorf("CJK 估算 = %d，应为 4（约 1 字 1 token）", got)
	}
	if got := EstimateTokens("abcdefgh"); got != 2 {
		t.Errorf("拉丁估算 = %d，应为 2（约 4 字符 1 token）", got)
	}
	if EstimateTokens("") != 0 {
		t.Error("空文本应为 0")
	}
	if EstimateTokens("你好 world 混合") < 6 {
		t.Error("混合文本估算异常偏低")
	}
}

func TestValidateReferences(t *testing.T) {
	if err := ValidateReferences([]Reference{{Kind: "file", Label: "方案.md", Value: "@D:/方案.md"}}); err != nil {
		t.Fatalf("合法引用被拒绝: %v", err)
	}
	cases := [][]Reference{
		{{Kind: "unknown", Label: "x", Value: "x"}},
		{{Kind: "file", Label: "", Value: "x"}},
		make([]Reference, MaxReferences+1),
	}
	for i, refs := range cases {
		if err := ValidateReferences(refs); err == nil {
			t.Errorf("case %d 应拒绝", i)
		}
	}
}
