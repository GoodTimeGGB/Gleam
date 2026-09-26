package agent

import (
	"strings"
	"testing"
)

// TestSanitizeMCPName 名字收敛必须是「替换非法字符」，不是「替换合法字符」。
//
// 反例来自原来的一行 `mcpNameRe.ReplaceAllString(base, "-")`：它把每个**合法**字符都换成
// 连字符，"npx" → "---"。于是所有用 npx 启动的自定义服务器共用一个名字，第二台装不上；
// 真正该换掉的非法字符反而留了下来。
func TestSanitizeMCPName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"npx", "npx"}, // 合法名字必须原样留下
		{"mcp-server-filesystem", "mcp-server-filesystem"},
		{"Server_FileSystem", "server_filesystem"}, // 只小写，不动字符
		{"mcp server.fetch", "mcp-server-fetch"},   // 空格与点都是非法字符
		{"中文服务", ""},                               // 全非法时交白卷，由调用方兜底成 custom
		{"---", ""},                                // 首尾连字符不算名字的一部分
		{"  spaced  ", "spaced"},
		{"a!!!!!!!!!!!!!!!b", "a-b"},                       // 连续非法字符并成一个分隔
		{strings.Repeat("a", 40), strings.Repeat("a", 32)}, // 32 字符封顶
	}
	for _, c := range cases {
		got := sanitizeMCPName(c.in)
		if got != c.want {
			t.Errorf("sanitizeMCPName(%q) = %q，期望 %q", c.in, got, c.want)
		}
		if got != "" && !mcpNameRe.MatchString(got) {
			t.Errorf("sanitizeMCPName(%q) = %q，仍不是合法名", c.in, got)
		}
	}
}
