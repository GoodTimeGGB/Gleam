package webui

import (
	"reflect"
	"strings"
	"testing"
)

// readStatic 读取内嵌的前端静态资源。
func readStatic(t *testing.T, name string) string {
	t.Helper()
	b, err := staticFS.ReadFile(name)
	if err != nil {
		t.Fatalf("读取内嵌静态资源 %s: %v", name, err)
	}
	return string(b)
}

// TestReadinessFrontend_Wired 前端接线必须完整。
// 路由通了但界面没有入口，用户就看不到这份报告——
// "后端好了前端漏接"是最容易悄悄上线的一类缺陷。
func TestReadinessFrontend_Wired(t *testing.T) {
	html := readStatic(t, "static/index.html")
	js := readStatic(t, "static/app.js")
	css := readStatic(t, "static/style.css")

	for _, want := range []string{
		`data-view="readiness"`,
		`id="view-readiness"`,
		`id="readiness-items"`,
		`id="ready-verdict"`,
		`id="ready-refresh"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html 缺少 %s", want)
		}
	}
	if !strings.Contains(js, "readiness: loadReadiness") {
		t.Error("app.js 未把 readiness 注册进视图加载表，点导航不会触发加载")
	}
	for _, want := range []string{"async function loadReadiness", "function renderReadiness", "/api/readiness"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js 缺少 %s", want)
		}
	}
	for _, want := range []string{".readiness-list", ".readiness-item--fail", ".readiness-evidence", ".readiness-fix"} {
		if !strings.Contains(css, want) {
			t.Errorf("style.css 缺少 %s", want)
		}
	}
}

// TestReadinessRoute_ReturnsNineItems 路由返回九个坑的完整体检报告。
func TestReadinessRoute_ReturnsNineItems(t *testing.T) {
	f, _ := newGEOFixture(t)
	got := f.call("GET", "/api/readiness", nil)

	if s, _ := got["summary"].(string); s == "" {
		t.Fatal("应返回一行摘要")
	}
	rep, ok := got["report"].(map[string]any)
	if !ok {
		t.Fatalf("应返回 report 对象，实际 %v", got)
	}
	items, ok := rep["items"].([]any)
	if !ok || len(items) != 9 {
		t.Fatalf("应有 9 项，实际 %v", rep["items"])
	}
	if rep["total"].(float64) != 9 {
		t.Errorf("total 应为 9，实际 %v", rep["total"])
	}
	for i, raw := range items {
		it := raw.(map[string]any)
		if int(it["index"].(float64)) != i+1 {
			t.Errorf("第 %d 项编号不对：%v", i+1, it["index"])
		}
		for _, field := range []string{"key", "title", "status", "summary"} {
			if s, _ := it[field].(string); s == "" {
				t.Errorf("第 %d 项缺少 %s", i+1, field)
			}
		}
		if _, ok := it["evidence"].([]any); !ok {
			t.Errorf("第 %d 项的 evidence 应是数组", i+1)
		}
	}
}

// TestReadinessRoute_ReflectsRealState 报告必须反映真实装配状态，而不是一句"一切正常"。
// 这个 fixture 有文件工具与工作区，但没有成长日志、也没配模型档位。
func TestReadinessRoute_ReflectsRealState(t *testing.T) {
	f, _ := newGEOFixture(t)
	rep := f.call("GET", "/api/readiness", nil)["report"].(map[string]any)

	status := map[string]string{}
	for _, raw := range rep["items"].([]any) {
		it := raw.(map[string]any)
		status[it["key"].(string)] = it["status"].(string)
	}

	// 有 file.write：交付能力过关
	if status["delivery"] != "pass" {
		t.Errorf("注册了写工具，delivery 应 pass，实际 %s", status["delivery"])
	}
	// 工作区已设置：业务上下文过关
	if status["business_gap"] != "pass" {
		t.Errorf("工作区已设置，business_gap 应 pass，实际 %s", status["business_gap"])
	}
	// 没配档位：机制在但没用起来
	if status["model_lock"] != "warn" {
		t.Errorf("未配模型档位应 warn，实际 %s", status["model_lock"])
	}
	// fixture 没有成长日志：迭代健康度无从判断
	if status["iteration_arch"] != "fail" {
		t.Errorf("无成长日志应 fail，实际 %s", status["iteration_arch"])
	}
	if rep["verdict"] != "not_ready" {
		t.Errorf("有 fail 项时应判定 not_ready，实际 %v", rep["verdict"])
	}
}

// TestReadinessRoute_IsReadOnly 体检不得改动任何设置：跑前跑后配置一致。
func TestReadinessRoute_IsReadOnly(t *testing.T) {
	f, _ := newGEOFixture(t)
	before := f.call("GET", "/api/settings", nil)

	first := f.call("GET", "/api/readiness", nil)["report"].(map[string]any)
	second := f.call("GET", "/api/readiness", nil)["report"].(map[string]any)

	after := f.call("GET", "/api/settings", nil)
	if !reflect.DeepEqual(before, after) {
		t.Error("体检修改了设置")
	}
	if first["verdict"] != second["verdict"] ||
		first["passed"].(float64) != second["passed"].(float64) {
		t.Errorf("两次体检结果不一致：%v / %v", first["verdict"], second["verdict"])
	}
}
