// Package registry 实现 Harness 的工具注册表：
// 运行时热注册/替换/注销，无需重启进程；并发安全。
package registry

import (
	"fmt"
	"sort"
	"sync"

	"gleam/pkg/types"
)

// Registry 工具注册表。
type Registry struct {
	mu    sync.RWMutex
	tools map[string]types.Tool
}

func New() *Registry {
	return &Registry{tools: map[string]types.Tool{}}
}

// Register 注册新工具；同名已存在时报错（热更新请用 Replace）。
func (r *Registry) Register(t types.Tool) error {
	if t == nil {
		return fmt.Errorf("registry: 工具为空")
	}
	name := t.Name()
	if name == "" {
		return fmt.Errorf("registry: 工具名为空")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("registry: 工具 %q 已注册", name)
	}
	r.tools[name] = t
	return nil
}

// Replace 注册或覆盖工具（热加载入口）。
func (r *Registry) Replace(t types.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// MustRegister 注册失败时 panic（仅用于内置工具装配）。
func (r *Registry) MustRegister(t types.Tool) {
	if err := r.Register(t); err != nil {
		panic(err)
	}
}

// Unregister 注销工具。
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; !ok {
		return false
	}
	delete(r.tools, name)
	return true
}

// Get 按名取工具。
func (r *Registry) Get(name string) (types.Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Names 返回全部工具名（排序）。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// List 返回全部工具（按名称排序）。
func (r *Registry) List() []types.Tool {
	names := r.Names()
	out := make([]types.Tool, 0, len(names))
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, n := range names {
		out = append(out, r.tools[n])
	}
	return out
}

// Len 当前工具数量。
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}
