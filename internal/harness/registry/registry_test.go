package registry

import (
	"context"
	"sync"
	"testing"

	"gleam/pkg/types"
)

type fakeTool struct {
	name string
	perm types.Permission
}

func (f *fakeTool) Name() string                                         { return f.name }
func (f *fakeTool) Description() string                                  { return "fake " + f.name }
func (f *fakeTool) Schema() map[string]any                               { return map[string]any{"type": "object"} }
func (f *fakeTool) Permission() types.Permission                         { return f.perm }
func (f *fakeTool) Execute(context.Context, map[string]any) (any, error) { return "ok", nil }

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := New()
	if err := r.Register(&fakeTool{name: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(&fakeTool{name: "a"}); err == nil {
		t.Error("重复注册应报错")
	}
	if got, ok := r.Get("a"); !ok || got.Name() != "a" {
		t.Errorf("Get = %v %v", got, ok)
	}
	if _, ok := r.Get("nope"); ok {
		t.Error("不存在的工具不应返回")
	}
	if r.Len() != 1 {
		t.Errorf("Len = %d", r.Len())
	}
	names := r.Names()
	if len(names) != 1 || names[0] != "a" {
		t.Errorf("Names = %v", names)
	}
}

func TestRegistry_ReplaceAndUnregister(t *testing.T) {
	r := New()
	r.MustRegister(&fakeTool{name: "a"})
	r.Replace(&fakeTool{name: "a", perm: types.PermissionFullAccess})
	got, _ := r.Get("a")
	if got.Permission() != types.PermissionFullAccess {
		t.Error("Replace 未生效")
	}
	if !r.Unregister("a") {
		t.Error("Unregister 失败")
	}
	if r.Unregister("a") {
		t.Error("重复 Unregister 应返回 false")
	}
	if r.Len() != 0 {
		t.Error("注销后应为空")
	}
}

func TestRegistry_ListSorted(t *testing.T) {
	r := New()
	for _, n := range []string{"c", "a", "b"} {
		r.MustRegister(&fakeTool{name: n})
	}
	names := r.Names()
	if names[0] != "a" || names[2] != "c" {
		t.Errorf("未排序: %v", names)
	}
}

func TestRegistry_Concurrent(t *testing.T) {
	r := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.Replace(&fakeTool{name: "hot"})
			r.Get("hot")
			r.List()
			r.Names()
		}(i)
	}
	wg.Wait()
	if r.Len() != 1 {
		t.Errorf("并发后数量 = %d", r.Len())
	}
}

func TestRegistry_Errors(t *testing.T) {
	r := New()
	if err := r.Register(nil); err == nil {
		t.Error("nil 工具应报错")
	}
	if err := r.Register(&fakeTool{name: ""}); err == nil {
		t.Error("空名应报错")
	}
}
