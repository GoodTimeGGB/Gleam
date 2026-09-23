// Package toolutil 提供工具参数解析的公共助手。
package toolutil

import (
	"encoding/json"
	"fmt"
	"strings"

	"gleam/pkg/types"
)

// Str 取字符串参数（容忍数字/布尔转换）。
func Str(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return trimFloat(t)
	case int64:
		return fmt.Sprintf("%d", t)
	case int:
		return fmt.Sprintf("%d", t)
	case bool:
		return fmt.Sprintf("%v", t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func trimFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// RequireStr 取必填字符串参数。
func RequireStr(args map[string]any, key string) (string, error) {
	s := Str(args, key)
	if strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("缺少必填参数 %q", key)
	}
	return s, nil
}

// Bool 取布尔参数。
func Bool(args map[string]any, key string) bool {
	switch v := args[key].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1" || v == "yes"
	default:
		return false
	}
}

// Int 取整数参数。
func Int(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int64:
		return int(v)
	case int:
		return v
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err == nil {
			return n
		}
	}
	return def
}

// Map 取映射参数。
func Map(args map[string]any, key string) map[string]any {
	if v, ok := args[key].(map[string]any); ok {
		return v
	}
	return nil
}

// IntOf 把任意数值型取值转成 int。
// 工具返回值里的数字在 map 里可能是 int（本进程内构造），
// 也可能是 float64（经 JSON 往返后），所以不能直接断言类型。
func IntOf(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		var r int
		if _, err := fmt.Sscanf(strings.TrimSpace(n), "%d", &r); err == nil {
			return r
		}
	}
	return 0
}

// EmptyIfNone 是「成功但没结果」这一态的统一判定：
// 返回值是带 count 字段的 map 且 count 为 0 时，判为 empty。
//
// 抽出来是因为这条判据在 file.list / file.search / memory.search 等处重复，
// 措辞一旦各处不一致，「结果为空不是错误、但必须与有结果区分」这条约定
// 就会漏掉某几个工具，而那些工具恰好是模型最容易反复重试的。
func EmptyIfNone(out any) (types.StepOutcome, string) {
	m, ok := out.(map[string]any)
	if !ok {
		return types.OutcomeOK, ""
	}
	if IntOf(m["count"]) == 0 {
		return types.OutcomeEmpty, ""
	}
	return types.OutcomeOK, ""
}

// SchemaProp 构造 JSON Schema 属性的简写助手。
func SchemaProp(desc, typ string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

// Schema 组装 JSON Schema。
func Schema(desc string, required []string, props map[string]any) map[string]any {
	return map[string]any{
		"type":        "object",
		"description": desc,
		"properties":  props,
		"required":    required,
	}
}
