// Package file 实现文件操作工具集：list/read/write/mkdir/move/delete/search。
// 所有路径限制在配置的工作区根目录内（防目录穿越），形成纵深防御的第一层。
package file

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gleam/internal/harness/registry"
	"gleam/internal/tools/toolutil"
	"gleam/pkg/types"
)

const maxReadBytes = 256 * 1024 // 单次读取上限 256KB

// Tools 文件工具集，Roots 为允许访问的根目录。
type Tools struct {
	Roots []string
}

func New(roots ...string) *Tools {
	return &Tools{Roots: roots}
}

// RegisterAll 将全部文件工具注册到注册表。
func (t *Tools) RegisterAll(reg *registry.Registry) {
	for _, tool := range []types.Tool{
		&listTool{t}, &readTool{t}, &writeTool{t}, &mkdirTool{t},
		&moveTool{t}, &deleteTool{t}, &searchTool{t},
	} {
		reg.MustRegister(tool)
	}
}

// resolve 解析并校验路径：绝对路径必须位于某个根目录下；相对路径基于第一个根目录。
// 边界语义见 toolutil.ResolveInRoots（与 shell 工具共用同一份实现）。
//
// 根目录优先取 ctx 里那一份（本次任务的边界），没有才回落到构造时配的 t.Roots：
// 任务在自己的 worktree 里跑时，只有 ctx 那份能把它引到真实的文件上。
func (t *Tools) resolve(ctx context.Context, p string) (string, error) {
	return toolutil.ResolveInRoots(p, toolutil.RootsFrom(ctx, t.Roots))
}

// requireResolve 取必填路径参数并解析校验。
func (t *Tools) requireResolve(ctx context.Context, args map[string]any, key string) (string, error) {
	p, err := toolutil.RequireStr(args, key)
	if err != nil {
		return "", err
	}
	return t.resolve(ctx, p)
}

// Paths 实现 types.PathAware：返回参数中涉及的路径，供安全门控判断信任范围。
func (t *Tools) Paths(ctx context.Context, args map[string]any) []string {
	keys := []string{"path", "src", "dst", "dir", "root"}
	var out []string
	for _, k := range keys {
		if v := toolutil.Str(args, k); v != "" {
			if abs, err := t.resolve(ctx, v); err == nil {
				out = append(out, abs)
			} else {
				out = append(out, v)
			}
		}
	}
	return out
}

type listTool struct{ *Tools }

func (t *listTool) Name() string { return "file.list" }
func (t *listTool) Description() string {
	return "列出目录内容（名称、大小、修改时间），用于了解目录结构"
}
func (t *listTool) Permission() types.Permission { return types.PermissionReadOnly }
func (t *listTool) Schema() map[string]any {
	return toolutil.Schema("列出目录内容", []string{"path"}, map[string]any{
		"path": toolutil.SchemaProp("目录路径（工作区内）", "string"),
	})
}
func (t *listTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	dir, err := t.requireResolve(ctx, args, "path")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type entry struct {
		Name    string    `json:"name"`
		IsDir   bool      `json:"is_dir"`
		Size    int64     `json:"size,omitempty"`
		ModTime time.Time `json:"mod_time"`
	}
	out := make([]entry, 0, len(entries))
	for _, e := range entries {
		en := entry{Name: e.Name(), IsDir: e.IsDir()}
		if info, err := e.Info(); err == nil {
			en.Size = info.Size()
			en.ModTime = info.ModTime()
		}
		out = append(out, en)
	}
	return map[string]any{"path": dir, "count": len(out), "entries": out}, nil
}

// Outcome 实现 types.OutcomeReporter：空目录是空结果，不是错误。
func (t *listTool) Outcome(args map[string]any, out any) (types.StepOutcome, string) {
	return toolutil.EmptyIfNone(out)
}

type readTool struct{ *Tools }

func (t *readTool) Name() string { return "file.read" }
func (t *readTool) Description() string {
	return "读取文本文件内容（最大 256KB，超出截断）"
}
func (t *readTool) Permission() types.Permission { return types.PermissionReadOnly }
func (t *readTool) Schema() map[string]any {
	return toolutil.Schema("读取文件", []string{"path"}, map[string]any{
		"path": toolutil.SchemaProp("文件路径（工作区内）", "string"),
	})
}
func (t *readTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	p, err := t.requireResolve(ctx, args, "path")
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// 循环读取：单次 Read 不保证填满缓冲区
	buf := make([]byte, maxReadBytes+1)
	n := 0
	for n < len(buf) {
		m, err := f.Read(buf[n:])
		n += m
		if err != nil {
			if err == io.EOF {
				break
			}
			if n == 0 {
				return nil, err
			}
			break
		}
		if m == 0 {
			break
		}
	}
	truncated := false
	if n > maxReadBytes {
		n = maxReadBytes
		truncated = true
	}
	content := string(buf[:n])
	info, _ := f.Stat()
	size := int64(n)
	if info != nil {
		size = info.Size()
	}
	return map[string]any{
		"path":      p,
		"content":   content,
		"size":      size,
		"truncated": truncated,
	}, nil
}

type writeTool struct{ *Tools }

func (t *writeTool) Name() string { return "file.write" }
func (t *writeTool) Description() string {
	return "写入文本文件（可追加）；目录不存在时自动创建"
}
func (t *writeTool) Permission() types.Permission {
	return types.PermissionUserApproved
}
func (t *writeTool) Schema() map[string]any {
	return toolutil.Schema("写文件", []string{"path", "content"}, map[string]any{
		"path":    toolutil.SchemaProp("文件路径", "string"),
		"content": toolutil.SchemaProp("要写入的内容", "string"),
		"append":  toolutil.SchemaProp("是否追加到文件尾（默认覆盖）", "boolean"),
	})
}
func (t *writeTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	p, err := t.requireResolve(ctx, args, "path")
	if err != nil {
		return nil, err
	}
	content := toolutil.Str(args, "content")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if toolutil.Bool(args, "append") {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(p, flags, 0o644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	n, err := f.WriteString(content)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": p, "bytes_written": n, "append": toolutil.Bool(args, "append")}, nil
}

type mkdirTool struct{ *Tools }

func (t *mkdirTool) Name() string        { return "file.mkdir" }
func (t *mkdirTool) Description() string { return "创建目录（含父目录）" }
func (t *mkdirTool) Permission() types.Permission {
	return types.PermissionUserApproved
}
func (t *mkdirTool) Schema() map[string]any {
	return toolutil.Schema("创建目录", []string{"path"}, map[string]any{
		"path": toolutil.SchemaProp("目录路径", "string"),
	})
}
func (t *mkdirTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	p, err := t.requireResolve(ctx, args, "path")
	if err != nil {
		return nil, err
	}
	_, statErr := os.Stat(p)
	if statErr == nil {
		return map[string]any{"path": p, "created": false}, nil
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		return nil, err
	}
	return map[string]any{"path": p, "created": true}, nil
}

type moveTool struct{ *Tools }

func (t *moveTool) Name() string        { return "file.move" }
func (t *moveTool) Description() string { return "移动/重命名文件或目录" }
func (t *moveTool) Permission() types.Permission {
	return types.PermissionUserApproved
}
func (t *moveTool) Schema() map[string]any {
	return toolutil.Schema("移动文件", []string{"src", "dst"}, map[string]any{
		"src": toolutil.SchemaProp("源路径", "string"),
		"dst": toolutil.SchemaProp("目标路径", "string"),
	})
}
func (t *moveTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	src, err := t.requireResolve(ctx, args, "src")
	if err != nil {
		return nil, err
	}
	dst, err := t.requireResolve(ctx, args, "dst")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(src, dst); err != nil {
		return nil, err
	}
	return map[string]any{"src": src, "dst": dst}, nil
}

type deleteTool struct{ *Tools }

func (t *deleteTool) Name() string { return "file.delete" }
func (t *deleteTool) Description() string {
	return "删除文件或目录（目录需 recursive=true，高风险）"
}
func (t *deleteTool) Permission() types.Permission {
	return types.PermissionFullAccess
}
func (t *deleteTool) Schema() map[string]any {
	return toolutil.Schema("删除文件/目录", []string{"path"}, map[string]any{
		"path":      toolutil.SchemaProp("目标路径", "string"),
		"recursive": toolutil.SchemaProp("删除目录时必须为 true", "boolean"),
	})
}
func (t *deleteTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	p, err := t.requireResolve(ctx, args, "path")
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		if !toolutil.Bool(args, "recursive") {
			return nil, fmt.Errorf("%q 是目录，需要 recursive=true 才能删除", p)
		}
		if err := os.RemoveAll(p); err != nil {
			return nil, err
		}
	} else if err := os.Remove(p); err != nil {
		return nil, err
	}
	return map[string]any{"path": p, "deleted": true}, nil
}

type searchTool struct{ *Tools }

func (t *searchTool) Name() string { return "file.search" }
func (t *searchTool) Description() string {
	return "按文件名通配符递归搜索文件（如 *.pdf），最多返回 200 条"
}
func (t *searchTool) Permission() types.Permission { return types.PermissionReadOnly }
func (t *searchTool) Schema() map[string]any {
	return toolutil.Schema("搜索文件", []string{"root", "pattern"}, map[string]any{
		"root":    toolutil.SchemaProp("搜索起始目录", "string"),
		"pattern": toolutil.SchemaProp("文件名通配符，如 *.txt", "string"),
	})
}
func (t *searchTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	root, err := t.requireResolve(ctx, args, "root")
	if err != nil {
		return nil, err
	}
	pattern := toolutil.Str(args, "pattern")
	if pattern == "" {
		pattern = "*"
	}
	var hits []string
	deadline := time.Now().Add(20 * time.Second)
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 跳过不可访问的子树
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("搜索超时")
		}
		if !d.IsDir() {
			if ok, _ := filepath.Match(pattern, d.Name()); ok {
				hits = append(hits, path)
				if len(hits) >= 200 {
					return fmt.Errorf("已达结果上限")
				}
			}
		}
		return nil
	})
	sort.Strings(hits)
	result := map[string]any{"root": root, "pattern": pattern, "count": len(hits), "files": hits}
	if err != nil {
		result["note"] = err.Error()
	}
	return result, nil
}

// Outcome 实现 types.OutcomeReporter。
//
// 两件事必须分开：搜索超时/达上限是"没做完"（失败），一条都没搜到是"空结果"
// （不是错误，但要让反思器看见——否则模型会以为拿到了数据，或者陷在
// "换个关键词再试—还是空—再换"的空转里）。
func (t *searchTool) Outcome(args map[string]any, out any) (types.StepOutcome, string) {
	if m, ok := out.(map[string]any); ok {
		if note, _ := m["note"].(string); note != "" {
			return types.OutcomeFailed, note
		}
	}
	return toolutil.EmptyIfNone(out)
}
