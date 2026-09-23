package eval

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// builtinCases 内置用例集。放在 JSON 里而不是 Go 字面量里，
// 是为了让用户能直接复制出来改：gleam eval --cases my.json。
//
//go:embed cases.json
var builtinCases []byte

type caseFile struct {
	Cases []Case `json:"cases"`
}

// BuiltinCases 返回内置用例集。
func BuiltinCases() ([]Case, error) {
	return parseCases(builtinCases)
}

// LoadCases 从文件读取用例集。
func LoadCases(path string) ([]Case, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseCases(b)
}

func parseCases(b []byte) ([]Case, error) {
	var f caseFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("解析用例集失败: %w", err)
	}
	if len(f.Cases) == 0 {
		return nil, fmt.Errorf("用例集为空")
	}
	if err := Validate(f.Cases); err != nil {
		return nil, err
	}
	return f.Cases, nil
}

// FilterLayer 按分层筛选用例（CLI --layer 用）。空层 = 全部。
func FilterLayer(cases []Case, l Layer) []Case {
	if l == "" {
		return cases
	}
	out := make([]Case, 0, len(cases))
	for _, c := range cases {
		if LayerOf(c) == l {
			out = append(out, c)
		}
	}
	return out
}

// Validate 校验用例集自身是否合法。
//
// 其中「没有任何期望」这一条最要紧：那样的用例**永远会通过**，
// 却会让通过率看起来不错——评测最容易被这样悄悄架空。
func Validate(cases []Case) error {
	seen := map[string]bool{}
	for i, c := range cases {
		if strings.TrimSpace(c.ID) == "" {
			return fmt.Errorf("第 %d 条用例缺 id", i+1)
		}
		if seen[c.ID] {
			return fmt.Errorf("用例 id 重复：%s", c.ID)
		}
		seen[c.ID] = true
		if strings.TrimSpace(c.Goal) == "" {
			return fmt.Errorf("用例 %s 缺 goal", c.ID)
		}
		if !ValidLayer(Layer(c.Layer)) {
			return fmt.Errorf("用例 %s 的 layer 取值非法: %q（可选 smoke|regression|edge|adversarial|holdout）", c.ID, c.Layer)
		}
		if len(c.WantTools) == 0 && len(c.NotWantTools) == 0 &&
			c.MinSteps == 0 && c.MaxSteps == 0 && !c.WantAcceptance && len(c.WantStatus) == 0 {
			return fmt.Errorf("用例 %s 没有任何期望，永远会通过——要么补期望，要么删掉它", c.ID)
		}
		if c.MinSteps > 0 && c.MaxSteps > 0 && c.MinSteps > c.MaxSteps {
			return fmt.Errorf("用例 %s 步数区间颠倒：%d..%d", c.ID, c.MinSteps, c.MaxSteps)
		}
	}
	return nil
}
