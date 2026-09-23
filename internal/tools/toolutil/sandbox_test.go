package toolutil

import (
	"os"
	"path/filepath"
	"testing"
)

// 边界只有一处实现（file 与 shell 共用），这里守住它的语义。
func TestResolveInRoots(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// 根内绝对路径
	if got, err := ResolveInRoots(sub, []string{root}); err != nil || got != sub {
		t.Errorf("根内路径应放行: %q %v", got, err)
	}
	// 相对路径基于第一个根
	if got, err := ResolveInRoots("sub", []string{root}); err != nil || got != sub {
		t.Errorf("相对路径应基于第一个根: %q %v", got, err)
	}
	// 根自身
	if _, err := ResolveInRoots(root, []string{root}); err != nil {
		t.Errorf("根自身应放行: %v", err)
	}
	// 根外
	if _, err := ResolveInRoots(other, []string{root}); err == nil {
		t.Error("根外路径应拒绝")
	}
	// 前缀相似但不同根：/ws2 不是 /ws 的子路径（字符串前缀比较会误判）
	sibling := root + "-sibling"
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sibling)
	if _, err := ResolveInRoots(sibling, []string{root}); err == nil {
		t.Error("前缀相似的兄弟目录应拒绝（Rel 判定，不是字符串前缀）")
	}
	// 空路径
	if _, err := ResolveInRoots("  ", []string{root}); err == nil {
		t.Error("空路径应拒绝")
	}
	// 未配置根：失败要朝着安全的方向失败
	if _, err := ResolveInRoots(sub, nil); err == nil {
		t.Error("未配置根目录时应拒绝任何路径")
	}
}
