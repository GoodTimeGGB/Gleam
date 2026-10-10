package market

import "testing"

// TestCompareVersionSegments 版本号按数字段比，不是按字符串比。
//
// 这条判据要的就是 1.10.0 与 1.9.0 那一对：字符串比较会得出 "1.9.0" 更大，
// 于是去重保留旧版本——用户装到的是上一版，而界面看起来完全正常。
func TestCompareVersionSegments(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.10.0", "1.9.0", 1},
		{"1.9.0", "1.10.0", -1},
		{"2.0", "2.0.0", 0},
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3-beta", "1.2.3", 0}, // 预发布后缀不参与比较（比的是"哪条更新"）
		{"0.1.0", "0.2.0", -1},
	}
	for _, c := range cases {
		if got := compareVersion(c.a, c.b); got != c.want {
			t.Errorf("compareVersion(%q,%q) = %d，期望 %d", c.a, c.b, got, c.want)
		}
	}
}
