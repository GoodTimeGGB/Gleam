package market

import (
	"strings"
	"testing"
)

// TestRuntimeForMapsKnownCommands 命令 → 运行时的映射。
//
// Windows 上 npx 实际是 npx.cmd，所以必须去掉扩展名再比——直接比字符串会让
// "这台机器缺 npx" 这条判断在 Windows 上永远不成立。
func TestRuntimeForMapsKnownCommands(t *testing.T) {
	cases := map[string]string{
		"npx":                 "npx",
		"npx.cmd":             "npx",
		"NPX":                 "npx",
		"uvx":                 "uvx",
		"/usr/local/bin/uvx":  "uvx",
		`C:\tools\docker.exe`: "docker",
		"cargo":               "cargo",
		"python":              "", // 认不出：不猜
		"my-server":           "",
		"":                    "",
	}
	for cmd, want := range cases {
		if got := RuntimeFor(cmd); got != want {
			t.Errorf("RuntimeFor(%q) = %q，期望 %q", cmd, got, want)
		}
	}
}

// TestRuntimeMissingIgnoresUnknownCommands 认不出的命令不算"缺东西"。
//
// 这条是防一类具体的错：把"我不认识这个命令"说成"你缺运行时"，
// 于是自定义命令（绝对路径、自家脚本）永远装不上，而提示还是错的方向。
func TestRuntimeMissingIgnoresUnknownCommands(t *testing.T) {
	for _, cmd := range []string{"my-server", "/opt/bin/thing", "python3", ""} {
		if st, missing := RuntimeMissing(cmd); missing {
			t.Errorf("%q 不该被判成缺运行时（得到 %+v）", cmd, st)
		}
	}
}

// TestRuntimesHaveActionableWhy 每个运行时都要有一句"缺了怎么办"，而且要能照着做。
func TestRuntimesHaveActionableWhy(t *testing.T) {
	list := Runtimes()
	if len(list) < 3 {
		t.Fatalf("运行时表太短：%d", len(list))
	}
	for _, r := range list {
		if strings.TrimSpace(r.Why) == "" {
			t.Errorf("%s 缺了之后没有说法（用户会卡在这一步）", r.Name)
		}
		if !strings.Contains(r.Why, "http") && !strings.Contains(r.Why, "设置") {
			t.Errorf("%s 的说法里既没有下载地址也没有站内入口，照着做不了：%q", r.Name, r.Why)
		}
		if r.Found && strings.TrimSpace(r.Path) == "" {
			t.Errorf("%s 说找到了却没给路径", r.Name)
		}
	}
}

// TestRankRemotePutsInstallableFirst 排名的两条判据：能装的在前、名字越贴越前。
func TestRankRemotePutsInstallableFirst(t *testing.T) {
	in := []RemotePreset{
		{ID: "ai.smithery/figma", Name: "Figma", Installable: false},
		{ID: "com.figma.mcp/mcp", Name: "Figma MCP", Installable: true},
		{ID: "x/other", Name: "Other", Installable: true},
	}
	out := RankRemote(in, "figma")
	if !out[0].Installable {
		t.Fatalf("能装的应排在最前：%+v", out)
	}
	// 两条都能装时，名字更贴的在前
	if out[0].Name != "Figma MCP" {
		t.Errorf("名字更贴近查询的应更靠前，得到 %q", out[0].Name)
	}
	if out[len(out)-1].Installable {
		t.Errorf("装不了的应排在后面：%+v", out)
	}
	// 空查询时也不能把装不了的排到能装的前面
	empty := RankRemote(in, "")
	if !empty[0].Installable {
		t.Errorf("空查询也应能装的在前：%+v", empty)
	}
}
