package webui

import (
	"net/http"
	"strings"
	"testing"

	"gleam/internal/config"
)

// ---------- 厂商预设 / 市场 / MCP 管理路由 ----------

func TestWebUI_Providers(t *testing.T) {
	f := newFixture(t, nil)
	out := f.call("GET", "/api/providers", nil)
	list := out["providers"].([]any)
	if len(list) < 5 {
		t.Errorf("厂商预设过少: %d", len(list))
	}
	first := list[0].(map[string]any)
	if first["id"] == "" || first["plans"] == nil {
		t.Errorf("预设字段缺失: %v", first)
	}
}

func TestWebUI_MarketSkillInstallAndList(t *testing.T) {
	f := newFixture(t, nil)
	out := f.call("GET", "/api/market/skills?q=速记", nil)
	presets := out["presets"].([]any)
	if len(presets) == 0 {
		t.Fatal("速记应命中 quick-note")
	}
	inst := f.call("POST", "/api/market/skills/install", map[string]any{"name": "quick-note"})
	if inst["installed"] != true || inst["version"] != float64(1) {
		t.Errorf("安装结果 = %v", inst)
	}
	// 已安装技能列表可见
	skills := f.call("GET", "/api/skills", nil)
	names := skills["skills"].([]any)
	ok := false
	for _, s := range names {
		if s.(map[string]any)["name"] == "quick-note" {
			ok = true
		}
	}
	if !ok {
		t.Error("安装后技能列表应包含 quick-note")
	}
	// 重装 = 版本升级
	again := f.call("POST", "/api/market/skills/install", map[string]any{"name": "quick-note"})
	if again["version"] != float64(2) {
		t.Errorf("重装应升版本: %v", again)
	}
}

func TestWebUI_MCPInstallCustomRemove(t *testing.T) {
	f := newFixture(t, nil)
	// 自定义安装：命令不存在 → 已保存配置但连接失败（warning），不报 500
	res := f.call("POST", "/api/mcp", map[string]any{
		"name": "my-mcp", "command": "gleam-nonexistent-cmd", "args": []string{"a", "b"}, "trust": "readonly",
	})
	if res["installed"] != true || res["connected"] != false {
		t.Fatalf("安装结果 = %v", res)
	}
	if res["warning"] == "" {
		t.Error("连接失败应有告警信息")
	}
	// 已安装列表
	list := f.call("GET", "/api/mcp", nil)
	if list["count"] != float64(1) {
		t.Errorf("列表 = %v", list)
	}
	// 持久化到覆盖层，重启可恢复
	fresh := config.Default()
	if err := config.LoadOverlay(fresh, f.dataDir+"/settings.yaml"); err != nil {
		t.Fatal(err)
	}
	if len(fresh.MCP) != 1 || fresh.MCP[0].Name != "my-mcp" {
		t.Errorf("覆盖层 = %+v", fresh.MCP)
	}
	// 重名安装被拒绝
	resp, err := http.Post(f.ts.URL+"/api/mcp", "application/json", strings.NewReader(`{"name":"my-mcp","command":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("重名应 400, got %d", resp.StatusCode)
	}
	// 卸载
	rm := f.call("DELETE", "/api/mcp/my-mcp", nil)
	if rm["removed_tools"] != float64(0) {
		t.Errorf("卸载结果 = %v", rm)
	}
	if cnt := f.call("GET", "/api/mcp", nil)["count"]; cnt != float64(0) {
		t.Errorf("卸载后应为空")
	}
}

func TestWebUI_MarketMCPInstallMissingParam(t *testing.T) {
	f := newFixture(t, nil)
	// filesystem 需要必填参数 path → 400
	resp, err := http.Post(f.ts.URL+"/api/market/mcp/install", "application/json", strings.NewReader(`{"id":"filesystem"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("缺必填参数应 400, got %d", resp.StatusCode)
	}
}
