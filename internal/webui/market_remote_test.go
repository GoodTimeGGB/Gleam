package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"gleam/internal/market"
)

// registryFixture 一份「长得像官方注册表」的响应：三种条目各一条——
// 能装的 npm stdio、只有 remotes 的远端、只有 cargo 包的。
//
// 第三种是刻意加进来的：源码里 runtimeFor 只认 npm/pypi，测试若只喂能装的那条，
// "不可安装要有理由"这半边就永远没人验。
const registryFixture = `{
  "servers": [
    {"server": {
      "name": "io.example/npm-server", "title": "NPM Server", "description": "一个能装的 npm 服务器",
      "version": "1.2.3",
      "repository": {"url": "https://example.invalid/npm-server"},
      "packages": [{
        "registryType": "npm", "registryBaseUrl": "https://registry.npmjs.org",
        "identifier": "@example/npm-server", "version": "1.2.3", "runtimeHint": "npx",
        "transport": {"type": "stdio"},
        "runtimeArguments": [{"value": "-y", "type": "positional"}],
        "environmentVariables": [
          {"name": "API_TOKEN", "description": "服务令牌", "isRequired": true},
          {"name": "OPTIONAL_ONE", "description": "可选", "isRequired": false}
        ]
      }]
    }},
    {"server": {
      "name": "io.example/plain-npm", "title": "Plain NPM", "description": "不需要环境变量的 npm 服务器",
      "version": "2.0.0",
      "packages": [{
        "registryType": "npm", "identifier": "@example/plain", "version": "2.0.0", "runtimeHint": "npx",
        "transport": {"type": "stdio"},
        "runtimeArguments": [{"value": "-y", "type": "positional"}]
      }]
    }},
    {"server": {
      "name": "ac.example/remote-only", "title": "Remote Only", "description": "只有远端传输",
      "version": "0.1.0",
      "remotes": [{"type": "streamable-http", "url": "http://127.0.0.1:9/mcp"}]
    }},
    {"server": {
      "name": "legacy.example/sse-only", "title": "Legacy SSE", "description": "只有旧版 sse",
      "version": "0.1.0",
      "remotes": [{"type": "sse", "url": "https://old.invalid/sse"}]
    }},
    {"server": {
      "name": "io.example/cargo-server", "title": "Cargo Server", "description": "只有 cargo 包",
      "version": "0.0.1",
      "packages": [{"registryType": "cargo", "identifier": "cargo-server", "version": "0.0.1", "runtimeHint": ""}]
    }}
  ]
}`

// TestParseRegistryClassifiesEveryShape 三种条目各自的判定与理由。
//
// 判据的重点是**不可安装那两条必须说清为什么**：目录里列着它、用户点了装不上，
// 而界面只说"暂不支持"，和"根本不在目录里"在观感上分不出来。
func TestParseRegistryClassifiesEveryShape(t *testing.T) {
	presets, err := market.ParseRegistry([]byte(registryFixture), "official-mcp")
	if err != nil {
		t.Fatal(err)
	}
	if len(presets) != 5 {
		t.Fatalf("应解析出 5 条，得到 %d", len(presets))
	}
	byID := map[string]market.RemotePreset{}
	for _, p := range presets {
		byID[p.ID] = p
	}

	// 不需要环境变量的 npm 条目：可安装，且命令行拼得对
	plain := byID["io.example/plain-npm"]
	if !plain.Installable {
		t.Fatalf("不需要环境变量的 npm stdio 条目应可安装：%+v", plain)
	}
	if plain.Command != "npx" {
		t.Errorf("运行时应是 npx，得到 %q", plain.Command)
	}
	if len(plain.BaseArgs) == 0 || plain.BaseArgs[0] != "-y" {
		t.Errorf("runtimeArguments 应排在包引用之前：%v", plain.BaseArgs)
	}
	if !strings.Contains(strings.Join(plain.BaseArgs, " "), "@example/plain@2.0.0") {
		t.Errorf("参数里应带包引用与版本：%v", plain.BaseArgs)
	}
	if plain.PkgRef != "@example/plain@2.0.0" {
		t.Errorf("PkgRef 应记包引用：%q", plain.PkgRef)
	}

	// 需要环境变量的 npm 条目：**可安装**（安装弹窗会要求填），且这些参数必须是 Kind=env。
	// Kind 决定值去哪：env 的作为环境变量交给子进程；若错标成 arg，API_TOKEN 会出现在
	// 命令行里（进程列表、日志都看得见）——所以这一位是安全属性，不只是排版。
	needEnv := byID["io.example/npm-server"]
	if !needEnv.Installable {
		t.Fatalf("需要环境变量的条目应可安装（弹窗里填）：%+v", needEnv)
	}
	if len(needEnv.Params) != 2 {
		t.Fatalf("必填与可选环境变量都该收进来，得到 %+v", needEnv.Params)
	}
	byKey := map[string]market.Param{}
	for _, pm := range needEnv.Params {
		byKey[pm.Key] = pm
	}
	if pm := byKey["API_TOKEN"]; pm.Kind != "env" || !pm.Required {
		t.Errorf("API_TOKEN 应是必填的环境变量：%+v", pm)
	}
	if pm := byKey["OPTIONAL_ONE"]; pm.Kind != "env" || pm.Required {
		t.Errorf("可选环境变量应标成非必填：%+v", pm)
	}
	if needEnv.Homepage != "https://example.invalid/npm-server" {
		t.Errorf("homepage 应取自 repository.url：%q", needEnv.Homepage)
	}

	// streamable-http 型远端：**可安装**（本版实现了这种传输），标成 http 并带地址
	remote := byID["ac.example/remote-only"]
	if !remote.Installable || remote.Kind != "http" || remote.URL == "" {
		t.Errorf("streamable-http 型远端应可安装并标成 http：%+v", remote)
	}
	// 旧版 sse：本版没实现，必须如实说不支持
	legacy := byID["legacy.example/sse-only"]
	if legacy.Installable || !strings.Contains(legacy.Unsupported, "sse") {
		t.Errorf("只有 sse 的条目应不可安装并点名 sse：%+v", legacy)
	}

	cargo := byID["io.example/cargo-server"]
	if cargo.Installable || !strings.Contains(cargo.Unsupported, "cargo") {
		t.Errorf("只有 cargo 包的条目应不可安装并点名分发方式：%+v", cargo)
	}
}

// TestFilterRemoteMatchesBuiltinKeywordRules 远端过滤与内置目录同一套口径。
func TestFilterRemoteMatchesBuiltinKeywordRules(t *testing.T) {
	presets, err := market.ParseRegistry([]byte(registryFixture), "official-mcp")
	if err != nil {
		t.Fatal(err)
	}
	if got := market.FilterRemote(presets, ""); len(got) != 5 {
		t.Errorf("空关键词应返回全部，得到 %d", len(got))
	}
	if got := market.FilterRemote(presets, "远端传输"); len(got) != 1 || got[0].ID != "ac.example/remote-only" {
		t.Errorf("应命中描述里的词，得到 %+v", got)
	}
	if got := market.FilterRemote(presets, "不存在的词"); len(got) != 0 {
		t.Errorf("无命中应返回空，得到 %d", len(got))
	}
}

// TestMarketInstallArgsOnlyRewritesNPM 只有「npm 包 + 非默认源」才加 --registry。
//
// 加了别的就坏：给 uvx 传 --registry 会让安装直接失败，而给官方源加参数纯属多此一举。
func TestMarketInstallArgsOnlyRewritesNPM(t *testing.T) {
	base := []string{"-y", "@example/npm-server@1.2.3"}
	cn := market.Source{ID: "npm-cn", Kind: "npm", BaseURL: "https://registry.npmmirror.com"}
	official := market.Source{ID: "npm", Kind: "npm", BaseURL: "https://registry.npmjs.org"}

	got := marketInstallArgs(cn, true, base)
	if len(got) != 3 || got[0] != "--registry=https://registry.npmmirror.com" {
		t.Errorf("国内源 + npm 包应前置 --registry：%v", got)
	}
	if out := marketInstallArgs(official, true, base); len(out) != len(base) {
		t.Errorf("官方源不该加参数：%v", out)
	}
	if out := marketInstallArgs(cn, false, base); len(out) != len(base) {
		t.Errorf("非 npm 包不该加 --registry：%v", out)
	}
}

// TestMarketRemoteFetchCacheAndEgress 端到端：拉目录 → 命中留痕 → 断网回退磁盘缓存。
//
// 断网那一段是这条判据的重点：目录连不上时**不能**把市场变成空列表——
// 空列表会被读成"市场里没有这个服务器"，而事实是"这次没连上"。
func TestMarketRemoteFetchCacheAndEgress(t *testing.T) {
	f := newFixture(t, nil)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/servers" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(registryFixture))
			return
		}
		// npm 测速用的那条
		_, _ = w.Write([]byte(`{"name":"left-pad"}`))
	}))
	defer ts.Close()

	oldCatalog, oldNPM, oldCN := market.MarketCatalogURL, market.MarketNPMURL, market.MarketNPMCNURL
	market.MarketCatalogURL = ts.URL + "/v0/servers"
	market.MarketNPMURL = ts.URL + "/npm"
	market.MarketNPMCNURL = ts.URL + "/npm-cn"
	defer func() {
		market.MarketCatalogURL, market.MarketNPMURL, market.MarketNPMCNURL = oldCatalog, oldNPM, oldCN
	}()

	// 换掉全局缓存，免得测试之间互相污染
	oldState := marketCacheState
	marketCacheState = &marketState{}
	defer func() { marketCacheState = oldState }()

	out := f.call("GET", "/api/market/mcp?q=", nil)
	if out["remote_count"] != float64(5) {
		t.Fatalf("远端应拉到 4 条：%v（note=%v）", out["remote_count"], out["source_note"])
	}
	presets, _ := out["presets"].([]any)
	remoteSeen := 0
	for _, it := range presets {
		if m, ok := it.(map[string]any); ok && m["remote"] == true {
			remoteSeen++
		}
	}
	if remoteSeen != 5 {
		t.Errorf("合并结果里应有 4 条带 remote 标记的条目，得到 %d", remoteSeen)
	}

	// 出网必须留痕：台账那一行才有读数
	rep := f.agent.Gate.EgressStats()
	found := false
	for _, st := range rep.Stats {
		if st.Kind == "market.remote" && st.Count > 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("market.remote 没有留痕：%+v", rep.Stats)
	}

	// 断网：把地址指到一个必然连不上的端口，强制重拉 → 应回退磁盘缓存并说明
	market.MarketCatalogURL = "http://127.0.0.1:1/v0/servers"
	marketCacheState = &marketState{}
	fallback := f.call("GET", "/api/market/mcp?refresh=1", nil)
	note, _ := fallback["source_note"].(string)
	if !strings.Contains(note, "缓存") {
		t.Errorf("断网时应说明用的是缓存，得到 note=%q", note)
	}
	if fallback["remote_count"] != float64(5) {
		t.Errorf("断网也应给出缓存里的 4 条，得到 %v", fallback["remote_count"])
	}
}

// TestMarketSourcesEndpoint 源列表把「目录从哪来、为什么选它」讲出来。
func TestMarketSourcesEndpoint(t *testing.T) {
	f := newFixture(t, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"left-pad"}`))
	}))
	defer ts.Close()

	oldNPM, oldCN := market.MarketNPMURL, market.MarketNPMCNURL
	market.MarketNPMURL, market.MarketNPMCNURL = ts.URL+"/npm", ts.URL+"/npm-cn"
	defer func() { market.MarketNPMURL, market.MarketNPMCNURL = oldNPM, oldCN }()
	oldState := marketCacheState
	marketCacheState = &marketState{}
	defer func() { marketCacheState = oldState }()

	out := f.call("GET", "/api/market/sources", nil)
	srcs, _ := out["sources"].([]any)
	if len(srcs) < 4 {
		t.Fatalf("源表至少应有 官方注册表 / npm / npm 镜像 / 内置，得到 %d", len(srcs))
	}
	if out["catalog_source"] != "official-mcp" {
		t.Errorf("目录源应标成 official-mcp：%v", out["catalog_source"])
	}
	why, _ := out["npm_source_why"].(string)
	if strings.TrimSpace(why) == "" {
		t.Error("必须说清为什么选了这个 npm 源")
	}
	// 选源的理由必须是「实测延迟」而不是 IP 归属地——这两者的差别是隐私
	if strings.Contains(why, "IP") || strings.Contains(why, "归属") {
		t.Errorf("选源理由不该提 IP 归属地：%q", why)
	}
	if !strings.Contains(out["note"].(string), "延迟") {
		t.Errorf("说明里应点出选择方式是实测延迟：%v", out["note"])
	}
}

// TestParseRegistryRejectsGarbage 坏响应要是错误，不是空目录。
func TestParseRegistryRejectsGarbage(t *testing.T) {
	if _, err := market.ParseRegistry([]byte("not json"), "official-mcp"); err == nil {
		t.Error("非 JSON 应当报错")
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(registryFixture), &resp); err != nil {
		t.Fatalf("fixture 本身应是合法 JSON：%v", err)
	}
}

// TestRemoteInstallRefusesTheUninstallable 安装入口对远端条目的三种结果。
//
// 重点在中间那条：**不可安装必须当场拒绝并说清理由**，而不是装出一个起不来的服务器。
// 另外命令与参数由服务端按目录拼，不取自请求体——这条用"请求体里塞命令"来验。
func TestRemoteInstallRefusesTheUninstallable(t *testing.T) {
	f := newFixture(t, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/servers" {
			_, _ = w.Write([]byte(registryFixture))
			return
		}
		_, _ = w.Write([]byte(`{"name":"left-pad"}`))
	}))
	defer ts.Close()

	oldCatalog, oldNPM, oldCN := market.MarketCatalogURL, market.MarketNPMURL, market.MarketNPMCNURL
	market.MarketCatalogURL, market.MarketNPMURL, market.MarketNPMCNURL = ts.URL+"/v0/servers", ts.URL+"/npm", ts.URL+"/npm-cn"
	defer func() {
		market.MarketCatalogURL, market.MarketNPMURL, market.MarketNPMCNURL = oldCatalog, oldNPM, oldCN
	}()
	oldState := marketCacheState
	marketCacheState = &marketState{}
	defer func() { marketCacheState = oldState }()

	// ① 可安装的：命令取自目录（npx），且 registry 覆盖按选中的源注入
	inst := f.call("POST", "/api/market/mcp/install", map[string]any{"id": "io.example/plain-npm"})
	if inst["installed"] != true {
		t.Fatalf("可安装的远端条目应装上：%v", inst)
	}
	if _, ok := inst["name"]; !ok {
		t.Errorf("安装结果应带服务器名：%v", inst)
	}

	// ② 远端 http 型：现在可安装。地址故意指到一个死端口——契约是"配置落盘成功、
	// 连接失败只回 warning"，而不是整个安装失败（否则用户改完网络还得重装一次）。
	remoteInst := f.call("POST", "/api/market/mcp/install", map[string]any{"id": "ac.example/remote-only"})
	if remoteInst["installed"] != true {
		t.Fatalf("远端条目应装上（配置落盘）：%v", remoteInst)
	}
	if w, _ := remoteInst["warning"].(string); w == "" {
		t.Errorf("连不上时应给出 warning，而不是假装连上了：%v", remoteInst)
	}
	// ③ 只有旧版 sse 的：拒绝，并点名它缺什么
	if code, body := f.status("POST", "/api/market/mcp/install", `{"id":"legacy.example/sse-only"}`); code != 400 {
		t.Errorf("只有 sse 的条目应被拒（400），得到 %d %s", code, body)
	} else if !strings.Contains(body, "sse") {
		t.Errorf("拒绝理由应点名 sse：%s", body)
	}

	// ④ 需要必填环境变量：没填 → 当场拒绝并点名；填了 → 装得上，且值真的进了配置
	if code, body := f.status("POST", "/api/market/mcp/install", `{"id":"io.example/npm-server"}`); code != 400 {
		t.Errorf("没填必填环境变量应被拒（400），得到 %d %s", code, body)
	} else if !strings.Contains(body, "API_TOKEN") {
		t.Errorf("拒绝理由应点名缺哪个变量：%s", body)
	}
	f.call("POST", "/api/market/mcp/install", map[string]any{
		"id": "io.example/npm-server", "params": map[string]string{"API_TOKEN": "secret-value"},
	})
	// 值必须落进配置（子进程靠它拿凭据）——但**不能**从 API 回吐：那是密钥，
	// 界面读的是"有没有"，不是"是什么"。
	foundEnv := ""
	for _, sv := range f.agent.Cfg.MCP {
		if sv.Name == "npm-server" {
			foundEnv = sv.Env["API_TOKEN"]
		}
	}
	if foundEnv != "secret-value" {
		t.Errorf("填了的环境变量应落进 MCP 配置，得到 %q", foundEnv)
	}
	mcpList := f.call("GET", "/api/mcp", nil)["mcp"].([]any)
	for _, it := range mcpList {
		m, _ := it.(map[string]any)
		if _, leaked := m["env"]; leaked {
			t.Errorf("/api/mcp 不该回吐环境变量（里面是密钥）：%v", m)
		}
	}

	// ⑤ 目录里没有的 id：拒绝
	if code, _ := f.status("POST", "/api/market/mcp/install", `{"id":"no.such/entry"}`); code != 400 {
		t.Errorf("未知 id 应 400，得到 %d", code)
	}
}

// TestMarketSearchForwardsQueryToSource 搜索词必须**发给目录源**，不在本地过滤。
//
// 这条是补上一次真事故的判据：第一版把查询词用在本地的 FilterRemote 上，
// 而注册表有几千条服务器、默认只返回一页——搜 "filesystem" 一条都搜不到，
// 界面显示"没有"。本地过滤看起来"更省一次请求"，实际是把搜索结果变成假的。
func TestMarketSearchForwardsQueryToSource(t *testing.T) {
	f := newFixture(t, nil)

	var mu sync.Mutex
	var seenSearch string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只记目录那一条请求：npm 测速请求也会打到这里，它当然没有 search 参数
		if r.URL.Path == "/v0/servers" {
			mu.Lock()
			seenSearch = r.URL.Query().Get("search")
			mu.Unlock()
			_, _ = w.Write([]byte(registryFixture))
			return
		}
		_, _ = w.Write([]byte(`{"name":"left-pad"}`))
	}))
	defer ts.Close()

	oldCatalog, oldNPM, oldCN := market.MarketCatalogURL, market.MarketNPMURL, market.MarketNPMCNURL
	market.MarketCatalogURL, market.MarketNPMURL, market.MarketNPMCNURL = ts.URL+"/v0/servers", ts.URL+"/npm", ts.URL+"/npm-cn"
	defer func() {
		market.MarketCatalogURL, market.MarketNPMURL, market.MarketNPMCNURL = oldCatalog, oldNPM, oldCN
	}()
	oldState := marketCacheState
	marketCacheState = &marketState{}
	defer func() { marketCacheState = oldState }()

	f.call("GET", "/api/market/mcp?q=filesystem", nil)
	mu.Lock()
	got := seenSearch
	mu.Unlock()
	if got != "filesystem" {
		t.Errorf("查询词应原样发给目录源，得到 search=%q", got)
	}

	// 路径必须走带 search 的那条分支（而不是 limit 分支）
	mu.Lock()
	seenSearch = ""
	mu.Unlock()
	f.call("GET", "/api/market/mcp?q=", nil)
	mu.Lock()
	empty := seenSearch
	mu.Unlock()
	if empty != "" {
		t.Errorf("空查询不该带 search 参数，得到 %q", empty)
	}
}

// TestParseRegistryKeepsNewestVersionPerServer 同名多版本只留最高版。
//
// 注册表把同一台服务器的每个版本各列一条；不去重的话，搜一次会看到同一台服务器
// 占好几行，而"该装哪条"要用户自己猜——那是把排序问题推给了用户。
func TestParseRegistryKeepsNewestVersionPerServer(t *testing.T) {
	body := `{"servers":[
      {"server":{"name":"io.example/multi","title":"Multi","description":"多版本",
        "version":"1.9.0","packages":[{"registryType":"npm","identifier":"multi","version":"1.9.0","runtimeHint":"npx","transport":{"type":"stdio"}}]}},
      {"server":{"name":"io.example/multi","title":"Multi","description":"多版本",
        "version":"1.10.0","packages":[{"registryType":"npm","identifier":"multi","version":"1.10.0","runtimeHint":"npx","transport":{"type":"stdio"}}]}},
      {"server":{"name":"io.example/multi","title":"Multi","description":"多版本",
        "version":"1.2.0","packages":[{"registryType":"npm","identifier":"multi","version":"1.2.0","runtimeHint":"npx","transport":{"type":"stdio"}}]}}
    ]}`
	presets, err := market.ParseRegistry([]byte(body), "official-mcp")
	if err != nil {
		t.Fatal(err)
	}
	if len(presets) != 1 {
		t.Fatalf("同名多版本应去重成 1 条，得到 %d：%+v", len(presets), presets)
	}
	// 1.10.0 比 1.9.0 新——按数字段比，不是按字符串比（字符串比会把 "1.9.0" 判成更新）
	if presets[0].Version != "1.10.0" {
		t.Errorf("应保留最高版本 1.10.0，得到 %q", presets[0].Version)
	}
}

// TestRemoteInstallFindsEntryFromSearchNotJustBrowse 按 id 安装必须找得到"刚在搜索结果里看到的那一条"。
//
// 这条是补一次真事故的判据：安装入口一开始只在浏览页（limit=100）里查 id，
// 而用户是从**搜索结果**里点的——于是"目录里没有这条"，而它就在眼前。
func TestRemoteInstallFindsEntryFromSearchNotJustBrowse(t *testing.T) {
	f := newFixture(t, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/servers" {
			_, _ = w.Write([]byte(`{"name":"left-pad"}`))
			return
		}
		// 浏览页（带 limit、不带 search）故意空着；只有带 search 才返回那一条
		if r.URL.Query().Get("search") == "" {
			_, _ = w.Write([]byte(`{"servers":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"servers":[{"server":{"name":"io.example/only-by-search","title":"Only","description":"只在搜索里出现",
          "version":"1.0.0","packages":[{"registryType":"npm","identifier":"only-by-search","version":"1.0.0","runtimeHint":"npx","transport":{"type":"stdio"}}]}}]}`))
	}))
	defer ts.Close()

	oldCatalog, oldNPM, oldCN := market.MarketCatalogURL, market.MarketNPMURL, market.MarketNPMCNURL
	market.MarketCatalogURL, market.MarketNPMURL, market.MarketNPMCNURL = ts.URL+"/v0/servers", ts.URL+"/npm", ts.URL+"/npm-cn"
	defer func() {
		market.MarketCatalogURL, market.MarketNPMURL, market.MarketNPMCNURL = oldCatalog, oldNPM, oldCN
	}()
	oldState := marketCacheState
	marketCacheState = &marketState{}
	defer func() { marketCacheState = oldState }()

	inst := f.call("POST", "/api/market/mcp/install", map[string]any{"id": "io.example/only-by-search"})
	if inst["installed"] != true {
		t.Fatalf("应能从搜索结果里找到并装上：%v", inst)
	}
}
