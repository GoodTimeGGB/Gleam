package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"gleam/internal/config"
	"gleam/internal/harness/skill"
	"gleam/pkg/types"
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
	// installed 必须是真布尔。写成 `_, installed := Skills.Get(name)` 时它是 error，
	// 序列化出来是 {}——前端一律当成「已安装」，市场里每一条都挂着重装按钮。
	if _, ok := presets[0].(map[string]any)["installed"].(bool); !ok {
		t.Fatalf("installed 不是布尔: %#v", presets[0].(map[string]any)["installed"])
	}
	if presets[0].(map[string]any)["installed"] != false {
		t.Fatal("未安装时 installed 应为 false")
	}
	inst := f.call("POST", "/api/market/skills/install", map[string]any{"name": "quick-note"})
	if inst["installed"] != true || inst["version"] != float64(1) {
		t.Errorf("安装结果 = %v", inst)
	}
	if got := f.call("GET", "/api/market/skills?q=速记", nil)["presets"].([]any)[0].(map[string]any)["installed"]; got != true {
		t.Errorf("安装后 installed = %v", got)
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
	// 重装要确认：未带 force → 409；带 force → 版本升级
	if code, _ := f.status("POST", "/api/market/skills/install", `{"name":"quick-note"}`); code != 409 {
		t.Errorf("技能重装未确认应 409, got %d", code)
	}
	again := f.call("POST", "/api/market/skills/install", map[string]any{"name": "quick-note", "force": true})
	if again["version"] != float64(2) {
		t.Errorf("重装应升版本: %v", again)
	}
}

// TestWebUI_SkillToggleEndToEnd 技能停用：状态落盘、运行被拒、不进规划清单。
func TestWebUI_SkillToggleEndToEnd(t *testing.T) {
	f := newFixture(t, nil)
	f.call("POST", "/api/market/skills/install", map[string]any{"name": "quick-note"})

	off := f.call("POST", "/api/skills/quick-note/enabled", map[string]any{"enabled": false})
	if off["enabled"] != false {
		t.Fatalf("停用返回 = %v", off)
	}
	list := f.call("GET", "/api/skills", nil)["skills"].([]any)
	if len(list) != 1 {
		t.Fatalf("停用的技能应仍留在库里（卸载才是删）: %v", list)
	}
	if list[0].(map[string]any)["disabled"] != true {
		t.Errorf("列表未体现停用状态: %v", list[0])
	}
	// 停用即不可运行，且要说清是被停的（不是"技能不存在"）
	if code, msg := f.status("POST", "/api/skills/quick-note/run", `{}`); code != 400 || !strings.Contains(msg, "已停用") {
		t.Errorf("停用后运行应被拒并说明原因: %d %q", code, msg)
	}
	// 进规划上下文的那份清单不含它
	if n := len(f.agent.Skills.ListSummaries()); n != 0 {
		t.Errorf("技能清单仍含停用项: %d 条", n)
	}
	// 重启（重新 Open）后仍是停用
	reopened, err := skill.Open(filepath.Join(f.dataDir, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	if sk, err := reopened.Get("quick-note"); err != nil || !sk.Disabled {
		t.Errorf("停用未落盘: %+v %v", sk, err)
	}
	// 启用回来：清单恢复、运行不再被停用手挡
	on := f.call("POST", "/api/skills/quick-note/enabled", map[string]any{"enabled": true})
	if on["enabled"] != true {
		t.Fatalf("启用返回 = %v", on)
	}
	if n := len(f.agent.Skills.ListSummaries()); n != 1 {
		t.Errorf("启用后应回到清单: %d 条", n)
	}
	if code, msg := f.status("POST", "/api/skills/no-such/enabled", `{"enabled":false}`); code != 404 {
		t.Errorf("未安装技能启停应 404, got %d (%s)", code, msg)
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
	// 重名安装被拒绝：409 而不是 400，前端才知道要弹「重装确认」而不是糊一句报错
	resp, err := http.Post(f.ts.URL+"/api/mcp", "application/json", strings.NewReader(`{"name":"my-mcp","command":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Errorf("重名应 409, got %d", resp.StatusCode)
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

// fakeMCPTool 假装「服务器已连上并注册了工具」的占位实现。
//
// 为什么要有它：测试环境起不了真的 stdio MCP 进程，而"停用后工具从注册表消失"这条线
// 恰恰是最容易只改配置、忘了摘工具的那类 bug——只看 enabled 字段是测不出来的。
type fakeMCPTool struct{ name string }

func (t fakeMCPTool) Name() string                 { return t.name }
func (t fakeMCPTool) Description() string          { return "测试用假工具" }
func (t fakeMCPTool) Schema() map[string]any       { return map[string]any{"type": "object"} }
func (t fakeMCPTool) Permission() types.Permission { return types.PermissionReadOnly }
func (t fakeMCPTool) Execute(context.Context, map[string]any) (any, error) {
	return "ok", nil
}

// status 直接发请求拿状态码与错误文案（f.call 对 >=400 会 Fatal）。
func (f *fixture) status(method, path string, body string) (int, string) {
	f.t.Helper()
	req, err := http.NewRequest(method, f.ts.URL+path, strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out.Error
}

// TestWebUI_MCPLifecycleReinstallAndToggle 覆盖「重装不追加、停用即摘工具并落盘」两条主线。
//
// 修之前这两处都是断的：重复安装无条件 append（列表里出现两条同名，装不上也删不干净）；
// enabled 字段只有启动时读一次，界面上「停用」无从谈起，只能卸载——而卸载会丢掉命令与
// 参数，调试一个连不上的服务器时，那正是用户最不想丢的东西。
func TestWebUI_MCPLifecycleReinstallAndToggle(t *testing.T) {
	f := newFixture(t, nil)
	const toolName = "mcp.my-mcp.echo"

	// 1) 首装（命令不存在 → 配置留存、连接告警）
	first := f.call("POST", "/api/mcp", map[string]any{
		"name": "my-mcp", "command": "gleam-nonexistent-cmd", "args": []string{"a"}, "trust": "readonly",
	})
	if first["installed"] != true || first["replaced"] != false {
		t.Fatalf("首装结果 = %v", first)
	}
	f.agent.Reg.MustRegister(fakeMCPTool{name: toolName})

	// 2) 重复安装未确认 → 409，且不往列表里塞第二条
	code, msg := f.status("POST", "/api/mcp", `{"name":"my-mcp","command":"other-cmd"}`)
	if code != 409 {
		t.Fatalf("重名未确认应 409, got %d (%s)", code, msg)
	}
	if !strings.Contains(msg, "已经安装") {
		t.Errorf("409 文案应说明已安装: %q", msg)
	}
	if c := f.call("GET", "/api/mcp", nil)["count"]; c != float64(1) {
		t.Errorf("被拒的安装不该留下条目，count = %v", c)
	}
	if _, ok := f.agent.Reg.Get(toolName); !ok {
		t.Error("被拒的安装不该把已注册的工具摘掉")
	}

	// 3) 带 force 重装 → 覆盖原条目（不追加）、旧工具摘掉、参数换成新的
	re := f.call("POST", "/api/mcp", map[string]any{
		"name": "my-mcp", "command": "gleam-nonexistent-cmd", "args": []string{"--v2"}, "trust": "readonly", "force": true,
	})
	if re["replaced"] != true {
		t.Errorf("force 安装应标记 replaced: %v", re)
	}
	if c := f.call("GET", "/api/mcp", nil)["count"]; c != float64(1) {
		t.Errorf("重装应覆盖而非追加，count = %v", c)
	}
	row := f.call("GET", "/api/mcp", nil)["mcp"].([]any)[0].(map[string]any)
	if got := fmt.Sprint(row["args"]); got != "[--v2]" {
		t.Errorf("参数应被替换为 [--v2]，实际 %v", got)
	}
	if _, ok := f.agent.Reg.Get(toolName); ok {
		t.Error("重装前应摘掉旧服务器的工具")
	}

	// 4) 停用：enabled 落盘、工具立刻从注册表消失
	f.agent.Reg.MustRegister(fakeMCPTool{name: toolName})
	off := f.call("POST", "/api/mcp/my-mcp/enabled", map[string]any{"enabled": false})
	if off["enabled"] != false {
		t.Fatalf("停用返回 = %v", off)
	}
	if _, ok := f.agent.Reg.Get(toolName); ok {
		t.Error("停用后工具仍在注册表：它还会被规划器看见")
	}
	fresh := config.Default()
	if err := config.LoadOverlay(fresh, f.dataDir+"/settings.yaml"); err != nil {
		t.Fatal(err)
	}
	if len(fresh.MCP) != 1 || fresh.MCP[0].Enabled {
		t.Errorf("停用未落盘: %+v", fresh.MCP)
	}
	// 停用的服务器不许「重连」——否则一个用户判定别跑的东西会重新挂上工具表
	if code, msg := f.status("POST", "/api/mcp/my-mcp/reconnect", ""); code != 404 || !strings.Contains(msg, "已停用") {
		t.Errorf("停用后重连应被拒: %d %q", code, msg)
	}

	// 5) 重新启用：落盘 + 尝试连接（这台连不上，所以是 connected=false + warning）
	on := f.call("POST", "/api/mcp/my-mcp/enabled", map[string]any{"enabled": true})
	if on["enabled"] != true || on["connected"] != false || on["warning"] == "" {
		t.Errorf("启用返回 = %v", on)
	}
	fresh2 := config.Default()
	if err := config.LoadOverlay(fresh2, f.dataDir+"/settings.yaml"); err != nil {
		t.Fatal(err)
	}
	if !fresh2.MCP[0].Enabled {
		t.Error("启用未落盘")
	}

	// 6) 未安装的名字 → 404，且不动配置
	if code, _ := f.status("POST", "/api/mcp/no-such/enabled", `{"enabled":false}`); code != 404 {
		t.Errorf("未安装应 404, got %d", code)
	}
	if c := f.call("GET", "/api/mcp", nil)["count"]; c != float64(1) {
		t.Errorf("失败操作不该改列表，count = %v", c)
	}
}

// TestWebUI_MarketMCPReinstallNeedsForce 市场预设走同一条守卫。
//
// 只测 409 那一支：它在连接之前就返回，因此不会去 spawn npx（真实下载要几分钟，
// 也可能根本没有网）。force 那支与自定义安装共用 mcpInstall，上面已经覆盖。
func TestWebUI_MarketMCPReinstallNeedsForce(t *testing.T) {
	f := newFixture(t, nil)
	// 预置一个同名条目，模拟「市场里这台已经装着」
	f.agent.Cfg.MCP = append(f.agent.Cfg.MCP, config.MCPServerConfig{
		Name: "filesystem", Command: "npx", Trust: "user_approved", Enabled: true,
	})
	code, msg := f.status("POST", "/api/market/mcp/install", `{"id":"filesystem","params":{"path":"D:/x"}}`)
	if code != 409 {
		t.Errorf("市场预设重名应 409, got %d (%s)", code, msg)
	}
	if !strings.Contains(msg, "已经安装") {
		t.Errorf("409 文案应引导重装确认: %q", msg)
	}
	// 非法名称走 400，别和 409 混成一类
	f.agent.Cfg.MCP = nil
	if code, _ := f.status("POST", "/api/market/mcp/install", `{"id":"no-such","params":{}}`); code != 400 {
		t.Errorf("未知预设应 400, got %d", code)
	}
}
