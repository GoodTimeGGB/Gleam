package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTools(t *testing.T) (*Tools, string) {
	t.Helper()
	ws := t.TempDir()
	return New(ws), ws
}

func TestFileTools_WriteReadList(t *testing.T) {
	tools, ws := newTools(t)
	ctx := context.Background()

	// write（含子目录自动创建）
	out, err := tools.requireResolve(ctx, map[string]any{}, "path")
	_ = out
	if err == nil {
		t.Error("空参数应报错")
	}
	wt := &writeTool{tools}
	w, err := wt.Execute(ctx, map[string]any{"path": filepath.Join("sub", "a.txt"), "content": "你好 Gleam"})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if w.(map[string]any)["bytes_written"] != int(12) { // "你好 Gleam" = 12 字节 UTF-8
		t.Errorf("bytes_written = %v", w)
	}

	// append 追加
	if _, err := wt.Execute(ctx, map[string]any{"path": filepath.Join("sub", "a.txt"), "content": "+", "append": true}); err != nil {
		t.Fatalf("append: %v", err)
	}

	// read
	rt := &readTool{tools}
	r, err := rt.Execute(ctx, map[string]any{"path": filepath.Join("sub", "a.txt")})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	rm := r.(map[string]any)
	if rm["content"] != "你好 Gleam+" {
		t.Errorf("content = %q", rm["content"])
	}
	if rm["truncated"] != false {
		t.Errorf("truncated = %v", rm["truncated"])
	}

	// list
	lt := &listTool{tools}
	l, err := lt.Execute(ctx, map[string]any{"path": "sub"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	lm := l.(map[string]any)
	if lm["count"] != 1 {
		t.Errorf("count = %v", lm["count"])
	}
	_ = ws
}

func TestFileTools_WorkspaceBoundary(t *testing.T) {
	tools, _ := newTools(t)
	ctx := context.Background()
	wt := &writeTool{tools}
	// 绝对路径越界
	if _, err := wt.Execute(ctx, map[string]any{"path": filepath.Join(os.TempDir(), "outside.txt"), "content": "x"}); err == nil {
		t.Error("越界写入应报错")
	}
	// 相对路径穿越
	if _, err := wt.Execute(ctx, map[string]any{"path": "../outside.txt", "content": "x"}); err == nil {
		t.Error("目录穿越应报错")
	}
	// 越界读取同样拒绝
	rt := &readTool{tools}
	if _, err := rt.Execute(ctx, map[string]any{"path": "../etc/passwd"}); err == nil {
		t.Error("越界读取应报错")
	}
}

func TestFileTools_RejectsSymlinkEscape(t *testing.T) {
	tools, ws := newTools(t)
	outside := t.TempDir()
	link := filepath.Join(ws, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := (&readTool{tools}).Execute(context.Background(), map[string]any{"path": filepath.Join("link", "secret.txt")}); err == nil {
		t.Fatal("symlink escape should be rejected")
	}
}

func TestFileTools_MkdirMoveDelete(t *testing.T) {
	tools, ws := newTools(t)
	ctx := context.Background()
	mt := &mkdirTool{tools}
	if _, err := mt.Execute(ctx, map[string]any{"path": "d1/d2"}); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// 已存在时 created=false
	if r, _ := mt.Execute(ctx, map[string]any{"path": "d1/d2"}); r.(map[string]any)["created"] != false {
		t.Error("重复 mkdir 应返回 created=false")
	}

	wt := &writeTool{tools}
	if _, err := wt.Execute(ctx, map[string]any{"path": "d1/d2/b.txt", "content": "x"}); err != nil {
		t.Fatal(err)
	}
	mv := &moveTool{tools}
	if _, err := mv.Execute(ctx, map[string]any{"src": "d1/d2/b.txt", "dst": "d1/b-moved.txt"}); err != nil {
		t.Fatalf("move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "d1", "b-moved.txt")); err != nil {
		t.Error("移动后文件不存在")
	}

	dt := &deleteTool{tools}
	// 目录不带 recursive 应报错
	if _, err := dt.Execute(ctx, map[string]any{"path": "d1"}); err == nil {
		t.Error("删目录缺 recursive 应报错")
	}
	if _, err := dt.Execute(ctx, map[string]any{"path": "d1", "recursive": true}); err != nil {
		t.Fatalf("delete recursive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "d1")); !os.IsNotExist(err) {
		t.Error("目录应已删除")
	}
}

func TestFileTools_Search(t *testing.T) {
	tools, _ := newTools(t)
	ctx := context.Background()
	wt := &writeTool{tools}
	for _, name := range []string{"a.pdf", "b.pdf", "c.txt", "d.md"} {
		if _, err := wt.Execute(ctx, map[string]any{"path": name, "content": "x"}); err != nil {
			t.Fatal(err)
		}
	}
	st := &searchTool{tools}
	r, err := st.Execute(ctx, map[string]any{"root": ".", "pattern": "*.pdf"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	rm := r.(map[string]any)
	if rm["count"] != 2 {
		t.Errorf("count = %v, files = %v", rm["count"], rm["files"])
	}
	files := rm["files"].([]string)
	if !strings.HasSuffix(files[0], "a.pdf") {
		t.Errorf("排序错误: %v", files)
	}
}

func TestFileTools_LargeReadTruncates(t *testing.T) {
	tools, ws := newTools(t)
	big := filepath.Join(ws, "big.txt")
	content := strings.Repeat("x", maxReadBytes+1024)
	if err := os.WriteFile(big, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := &readTool{tools}
	r, err := rt.Execute(context.Background(), map[string]any{"path": big})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	rm := r.(map[string]any)
	if rm["truncated"] != true {
		t.Error("超限读取应标记 truncated")
	}
	if len(rm["content"].(string)) != maxReadBytes {
		t.Errorf("截断长度 = %d", len(rm["content"].(string)))
	}
}

func TestFileTools_PathAware(t *testing.T) {
	tools, ws := newTools(t)
	paths := tools.Paths(context.Background(), map[string]any{"path": "a.txt", "src": "b.txt"})
	if len(paths) != 2 {
		t.Fatalf("Paths = %v", paths)
	}
	if !strings.HasPrefix(filepath.ToSlash(paths[0]), filepath.ToSlash(ws)) {
		t.Errorf("路径应解析到工作区内: %v", paths[0])
	}
}
