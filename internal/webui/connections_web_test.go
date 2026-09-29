package webui

import (
	"strings"
	"testing"

	"gleam/internal/agent"
	"gleam/internal/llm"
)

// 台账的判据一律走**真实路由**。直接调 Agent.ConnectionView 也能过，
// 但那一层测的是纯函数；这一层要挡的失效是路由没注册、handler 忘了把
// 宿主的监听地址传下去、读数没接上门控——那些在纯函数测试里一律绿。

func ledgerRows(t *testing.T, f *fixture) []map[string]any {
	t.Helper()
	out := f.call("GET", "/api/connections", nil)
	raw, _ := out["rows"].([]any)
	if len(raw) == 0 {
		t.Fatalf("台账路由没返回任何行：%v", out)
	}
	rows := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("台账行不是对象：%v", item)
		}
		rows = append(rows, row)
	}
	return rows
}

func findRow(t *testing.T, rows []map[string]any, id string) map[string]any {
	t.Helper()
	for _, r := range rows {
		if s, _ := r["id"].(string); s == id {
			return r
		}
	}
	t.Fatalf("台账里没有 %q 这一行", id)
	return nil
}

func textOf(row map[string]any, key string) string {
	s, _ := row[key].(string)
	return s
}

// TestWebUI_ConnectionsRouteServesEveryEgressKind 每条出网落点都从路由上取得到。
//
// id 清单取 agent 那份 owner（不在测试里重抄一遍——重抄的就是将来会漂的那一份），
// 反向也判：路由里不许有清单之外的出网行。
// 开头那条 404 断言是**负例控制**：不先确认"未注册的路由会 404"，
// 后面所有"这行在不在"都可能只是读到了静态页的兜底响应。
func TestWebUI_ConnectionsRouteServesEveryEgressKind(t *testing.T) {
	f := newFixture(t, nil)
	if code, _ := f.status("GET", "/api/connections-not-registered", ""); code != 404 {
		t.Fatalf("未注册的路由应回 404，实得 %d——那下面的台账断言就无从判起", code)
	}

	rows := ledgerRows(t, f)
	listed := map[string]bool{}
	for _, kind := range agent.EgressKinds() {
		listed[kind] = true
		row := findRow(t, rows, kind)
		if textOf(row, "kind") != "out" {
			t.Errorf("%s 行 kind = %q，出网落点必须是 out", kind, textOf(row, "kind"))
		}
		// kindText 必须带：前端不自己写映射表是这一层的约定（显示口径只有一个 owner）
		if textOf(row, "kindText") == "" {
			t.Errorf("%s 行没带中文类别", kind)
		}
		if textOf(row, "leaves") == "" {
			t.Errorf("%s 行没说清什么东西会离开本机", kind)
		}
	}
	for _, row := range rows {
		if textOf(row, "kind") == "out" && !listed[textOf(row, "id")] {
			t.Errorf("路由给出了未登记的出网行 %q：这一行不会有留痕来源", textOf(row, "id"))
		}
	}
	if scope := textOfMap(f.call("GET", "/api/connections", nil), "egressScope"); strings.TrimSpace(scope) == "" {
		t.Error("台账没交代这些读数到底数了哪些留痕")
	}
}

func textOfMap(out map[string]any, key string) string {
	s, _ := out[key].(string)
	return s
}

// TestWebUI_ConnectionsLLMRowFollowsSettings 改了接入地址，路由给出的主机要跟着改。
//
// 断的是**相等**：等于 llm.KeyScope（密钥绑定那把尺），不是"非空"。
// 两处各算一遍主机名就会漂，于是界面上出现"密钥说 A、台账写 B"两份互相矛盾的安全承诺。
func TestWebUI_ConnectionsLLMRowFollowsSettings(t *testing.T) {
	f := newFixture(t, nil)
	f.agent.Cfg.LLM.Provider = "glm"
	f.agent.Cfg.LLM.BaseURL = "https://api.vendor-a.test/v1"
	row := findRow(t, ledgerRows(t, f), "llm")
	if want := llm.KeyScope(f.agent.Cfg.LLM.BaseURL); textOf(row, "target") != want {
		t.Errorf("目标 = %q，应等于密钥绑定的主机 %q", textOf(row, "target"), want)
	}

	// 换厂商（用户真的会这么做）：台账必须说新家的地址，而不是启动时那一次
	f.agent.Cfg.LLM.BaseURL = "https://api.vendor-b.test/v1"
	after := findRow(t, ledgerRows(t, f), "llm")
	if want := llm.KeyScope(f.agent.Cfg.LLM.BaseURL); textOf(after, "target") != want {
		t.Errorf("换厂商后目标仍是 %q，应为 %q", textOf(after, "target"), want)
	}
	if !strings.Contains(textOf(after, "status"), "开着") {
		t.Errorf("配了真实接入应说开着：%q", textOf(after, "status"))
	}
}

// TestWebUI_ConnectionsInboundRowNeedsRealBindAddr 入网行由宿主装配的真实监听地址决定。
//
// 这条挡的是装配期的漏：cmd/gleam 忘了把 ln.Addr() 灌进 Server.BindAddr 时，
// 台账永远不画入网行，用户读到的是"这台机器没有对外的耳朵"——纯函数测试全绿。
func TestWebUI_ConnectionsInboundRowNeedsRealBindAddr(t *testing.T) {
	f := newFixture(t, nil)
	for _, row := range ledgerRows(t, f) {
		if textOf(row, "kind") == "in" {
			t.Fatalf("没装配监听地址时不该画入网行：%v", row["id"])
		}
	}

	f.srv.BindAddr = "127.0.0.1:8795"
	row := findRow(t, ledgerRows(t, f), "inbound")
	if textOf(row, "target") != "127.0.0.1:8795" {
		t.Errorf("target = %q，应原样回显监听地址", textOf(row, "target"))
	}
	if untraced, _ := row["untraced"].(bool); !untraced {
		t.Error("这个端口不记录谁连过，必须标出来")
	}

	f.srv.BindAddr = "0.0.0.0:8795"
	open := findRow(t, ledgerRows(t, f), "inbound")
	if !strings.Contains(textOf(open, "alert"), "局域网") {
		t.Errorf("非回环必须改口报警：%q", textOf(open, "alert"))
	}
}

// TestWebUI_ConnectionsRowStatsComeFromAudit 门控里记一笔，台账那一行就得有读数。
//
// 断的是**台账与门控之间那根线**：读数不是文案模板里的占位数字。
// 这条与 agent 包里那条互补——那边测派生函数会读聚合，这边测路由没把聚合丢掉。
func TestWebUI_ConnectionsRowStatsComeFromAudit(t *testing.T) {
	f := newFixture(t, nil)
	f.agent.Gate.RecordEgress("web.fetch", "example.com", 512)
	stats := textOf(findRow(t, ledgerRows(t, f), "web.fetch"), "stats")
	if !strings.Contains(stats, "1 次") || !strings.Contains(stats, "example.com") {
		t.Errorf("抓取行的读数没接上门控留痕：%q", stats)
	}
	// 另一类没发过包：不许串台，也不许留空（空白格子的读法是"这行不归我管"）
	cloud := textOf(findRow(t, ledgerRows(t, f), "cloud"), "stats")
	if strings.Contains(cloud, "example.com") || strings.TrimSpace(cloud) == "" {
		t.Errorf("云端行读数被串台或空着：%q", cloud)
	}
}

// TestWebUI_ConnectionsIsReadOnly 读台账一个包都不发。
//
// 看着像废话，但它挡的是一种真实的写法："探一下通不通"去 ping 远端。
// 一个自称"出网都记着"的面板自己偷偷发包，是最难被发现的那种自打脸。
func TestWebUI_ConnectionsIsReadOnly(t *testing.T) {
	f := newFixture(t, nil)
	before := len(f.mock.Calls)
	ledgerRows(t, f)
	ledgerRows(t, f)
	if got := len(f.mock.Calls); got != before {
		t.Errorf("读台账触发了模型调用（%d → %d）：这一屏必须完全不发包", before, got)
	}
}
