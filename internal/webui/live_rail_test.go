package webui

import (
	"testing"
)

// TestInfo_CarriesMemoryCount 现场栏「记忆条目」这一格的数据线。
//
// 为什么要为一个小数字单独立测：`Agent.MemoryCount()` 写对了不等于 `/api/info`
// 把它带出来了，更不等于前端拿到的就是它。本仓库栽过的全是这一类——
// 判据对、线没接。走真实路由（而不是直接调门面）才能证明这条线是通的。
func TestInfo_CarriesMemoryCount(t *testing.T) {
	f := newFixture(t, nil)

	before := infoMemoryCount(t, f)

	if out := f.call("POST", "/api/memory", map[string]any{
		"content": "用户偏好：现场栏的数字必须来自单一 owner",
		"tags":    []string{"preference"},
	}); out == nil {
		t.Fatal("存记忆失败")
	}
	after := infoMemoryCount(t, f)

	if after != before+1 {
		t.Errorf("存一条记忆后 /api/info 的 memory 应 +1：%d → %d（改门面不改这里就是这条线断的样子）", before, after)
	}
}

// infoMemoryCount 读一次 /api/info 并断言 memory 是个数字。
//
// 断言类型而不是只取值：字段漏发时 JSON 里根本没有这个键，前端会显示 0，
// 而 0 恰好是"看起来合理"的值——静默错成"你还没有记忆"。
func infoMemoryCount(t *testing.T, f *fixture) int64 {
	t.Helper()
	info := f.call("GET", "/api/info", nil)
	raw, ok := info["memory"]
	if !ok {
		t.Fatal("/api/info 没有 memory 字段")
	}
	n, ok := raw.(float64)
	if !ok {
		t.Fatalf("memory 应为数字，实际 %T", raw)
	}
	return int64(n)
}
