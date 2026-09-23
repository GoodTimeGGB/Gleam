package types

import "testing"

// TestClassifyError 归因分布的判据是"能不能回答该先修哪层"，
// 不是"每条都对"——所以每一类至少要有一个代表性文本归得对，
// 归不了的如实落 unknown（不硬塞）。
func TestClassifyError(t *testing.T) {
	cases := []struct {
		msg  string
		want ErrorKind
	}{
		{"步骤超时（30s）: context deadline exceeded", ErrTimeout},
		{"too many requests: rate limited", ErrTimeout},
		{"permission denied: 403", ErrPermission},
		{"用户拒绝执行该操作（不想覆盖已有文件）", ErrPermission},
		{"file not found: notes.txt", ErrNotFound},
		{"引用的步骤 \"s2\" 未成功执行", ErrParam},
		{"missing required argument: path", ErrParam},
		{"命令以退出码 1 结束", ErrBusiness},
		{"502 bad gateway from upstream", ErrUpstream},
		{"boom", ErrUnknown},
	}
	for _, c := range cases {
		if got := ClassifyError(c.msg); got != c.want {
			t.Errorf("ClassifyError(%q) = %q，应为 %q", c.msg, got, c.want)
		}
	}
}

// TestContextBreakdownStateShare 状态占比是"上下文膨胀"的判据线（> 60%），
// 除零必须安全（空提示词不该 panic）。
func TestContextBreakdownStateShare(t *testing.T) {
	var empty ContextBreakdown
	if empty.StateShare() != 0 {
		t.Error("空 breakdown 的占比应为 0")
	}
	b := ContextBreakdown{State: 600, Total: 1000}
	if b.StateShare() != 0.6 {
		t.Errorf("600/1000 应为 0.6，实际 %v", b.StateShare())
	}
}
