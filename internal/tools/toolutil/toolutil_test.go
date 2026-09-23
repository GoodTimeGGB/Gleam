package toolutil

import (
	"strings"
	"testing"
)

func TestStr(t *testing.T) {
	args := map[string]any{
		"s": "text", "n": 3.5, "i": int64(7), "b": true, "nil": nil,
	}
	if Str(args, "s") != "text" {
		t.Error("字符串")
	}
	if Str(args, "n") != "3.5" {
		t.Errorf("数字 = %q", Str(args, "n"))
	}
	if Str(args, "i") != "7" {
		t.Errorf("整数 = %q", Str(args, "i"))
	}
	if Str(args, "b") != "true" {
		t.Errorf("布尔 = %q", Str(args, "b"))
	}
	if Str(args, "missing") != "" {
		t.Error("缺失应为空")
	}
}

func TestRequireStr(t *testing.T) {
	if _, err := RequireStr(map[string]any{"k": "v"}, "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := RequireStr(map[string]any{"k": "  "}, "k"); err == nil {
		t.Error("空白应报错")
	}
	if _, err := RequireStr(map[string]any{}, "k"); err == nil {
		t.Error("缺失应报错")
	}
}

func TestBoolIntMap(t *testing.T) {
	if !Bool(map[string]any{"b": true}, "b") {
		t.Error("bool true")
	}
	if Bool(map[string]any{"b": "false"}, "b") {
		t.Error("字符串 false")
	}
	if Bool(map[string]any{}, "b") {
		t.Error("缺失默认 false")
	}
	if Int(map[string]any{"n": float64(5)}, "n", 1) != 5 {
		t.Error("float64 整数")
	}
	if Int(map[string]any{"n": "8"}, "n", 1) != 8 {
		t.Error("字符串整数")
	}
	if Int(map[string]any{}, "n", 9) != 9 {
		t.Error("默认值")
	}
	m := Map(map[string]any{"m": map[string]any{"a": 1}}, "m")
	if m == nil || m["a"] != 1 {
		t.Error("Map 取值")
	}
	if Map(map[string]any{}, "m") != nil {
		t.Error("缺失 Map 应为 nil")
	}
}

func TestSchema(t *testing.T) {
	s := Schema("描述", []string{"path"}, map[string]any{
		"path": SchemaProp("路径", "string"),
	})
	if s["type"] != "object" || s["description"] != "描述" {
		t.Errorf("schema = %v", s)
	}
	props := s["properties"].(map[string]any)
	if !strings.Contains(props["path"].(map[string]any)["type"].(string), "string") {
		t.Error("属性类型")
	}
}
