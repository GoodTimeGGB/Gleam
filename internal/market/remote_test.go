package market

import (
	"strings"
	"testing"
)

const remoteFixture = `{"servers":[
 {"server":{"name":"com.figma.mcp/mcp","title":"Figma","description":"Figma 远端",
   "version":"1.0.0","remotes":[{"type":"streamable-http","url":"https://mcp.figma.com/mcp"}]}},
 {"server":{"name":"ai.smithery/smithery-notion","title":"Notion","description":"带鉴权的远端",
   "version":"1.0.0","remotes":[{"type":"streamable-http","url":"https://server.smithery.ai/notion/mcp",
     "headers":[{"name":"Authorization","description":"Bearer token for Smithery authentication",
       "isRequired":true,"isSecret":true,"value":"Bearer {smithery_api_key}"}]}]}},
 {"server":{"name":"legacy/sse-only","title":"Legacy","description":"只有旧版 sse",
   "version":"1.0.0","remotes":[{"type":"sse","url":"https://old.invalid/sse"}]}}
]}`

// TestParseRegistryMarksStreamableHTTPInstallable 远端 streamable-http 条目要成为可安装项。
//
// 这是「一大类装不了」的正面判据：Figma / Notion 这类条目在注册表里**只有 remotes、
// 没有本机包**，把它们标成装不了等于市场里一大半点不动。
func TestParseRegistryMarksStreamableHTTPInstallable(t *testing.T) {
	presets, err := ParseRegistry([]byte(remoteFixture), "official-mcp")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]RemotePreset{}
	for _, p := range presets {
		byID[p.ID] = p
	}

	figma := byID["com.figma.mcp/mcp"]
	if !figma.Installable || figma.Kind != "http" || figma.URL != "https://mcp.figma.com/mcp" {
		t.Fatalf("Figma 这类远端条目应可安装且标成 http：%+v", figma)
	}
	if figma.Command != "" {
		t.Errorf("远端条目不该有命令：%q", figma.Command)
	}

	// 带鉴权的远端：请求头模板里的占位符要变成一条必填、且标记为凭据的参数
	notion := byID["ai.smithery/smithery-notion"]
	if !notion.Installable || notion.Kind != "http" {
		t.Fatalf("带鉴权的远端也应可安装：%+v", notion)
	}
	if len(notion.Headers) != 1 || notion.Headers[0].Name != "Authorization" {
		t.Fatalf("请求头没解析出来：%+v", notion.Headers)
	}
	if len(notion.Params) != 1 {
		t.Fatalf("模板里的占位符应变成一条参数：%+v", notion.Params)
	}
	pm := notion.Params[0]
	if pm.Key != "smithery_api_key" || pm.Kind != "header" || !pm.Required || !pm.Secret {
		t.Errorf("参数应是必填、标记为凭据的请求头项：%+v", pm)
	}
	if !strings.Contains(pm.Label, "Smithery") {
		t.Errorf("标签该用注册表给的描述（它更懂要填什么）：%q", pm.Label)
	}

	// 旧版 sse：本版没实现，必须如实说不支持、并点名差别
	legacy := byID["legacy/sse-only"]
	if legacy.Installable {
		t.Errorf("只有 sse 的条目不该标成可安装：%+v", legacy)
	}
	if !strings.Contains(legacy.Unsupported, "sse") || !strings.Contains(legacy.Unsupported, "streamable-http") {
		t.Errorf("理由要点名它只有 sse、而本版实现了 streamable-http：%q", legacy.Unsupported)
	}
}

// TestBuildHeadersSubstitutesPlaceholders 占位符替换与必填校验。
func TestBuildHeadersSubstitutesPlaceholders(t *testing.T) {
	p := RemotePreset{Headers: []RemoteHeader{
		{Name: "Authorization", Value: "Bearer {key}", Required: true, Secret: true},
		{Name: "X-Tenant", Value: "acme", Required: false},
	}}
	got, err := BuildHeaders(p, map[string]string{"key": "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	if got["Authorization"] != "Bearer abc123" {
		t.Errorf("占位符没替换：%q", got["Authorization"])
	}
	if got["X-Tenant"] != "acme" {
		t.Errorf("没有占位符的固定头也要带上：%q", got["X-Tenant"])
	}

	// 必填没填：必须报错，且点名缺哪个——带着 "Bearer {key}" 去请求只会得到一个 401，
	// 而 401 看不出"你没填"。
	if _, err := BuildHeaders(p, map[string]string{}); err == nil {
		t.Fatal("必填占位符没填应报错")
	} else if !strings.Contains(err.Error(), "key") {
		t.Errorf("报错应点名缺哪一项：%v", err)
	}
}

// TestPickStreamableRemoteSkipsLegacySSE 只认 streamable-http，别把 sse 当成能用。
func TestPickStreamableRemoteSkipsLegacySSE(t *testing.T) {
	if _, ok := pickStreamableRemote([]registryRemote{{Type: "sse", URL: "https://x.invalid"}}); ok {
		t.Error("sse 不该被选中")
	}
	r, ok := pickStreamableRemote([]registryRemote{
		{Type: "sse", URL: "https://old.invalid"},
		{Type: "streamable-http", URL: "https://new.invalid/mcp"},
	})
	if !ok || r.URL != "https://new.invalid/mcp" {
		t.Errorf("两种都在时应选 streamable-http，得到 %+v ok=%v", r, ok)
	}
}
