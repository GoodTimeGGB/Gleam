package std

import (
	"context"
	"testing"

	"gleam/pkg/types"
)

// P2-4 memory.delete：删除是用户数据的有损操作——权限必须是审批级，
// 软删走可选的 MemoryDeleter 接口，不破坏只实现了读写查的适配器。
type fakeMemDeleter struct {
	fakeMem
	deleted map[string]bool
}

func (f *fakeMemDeleter) SoftDelete(id string) bool {
	if !f.deleted[id] {
		f.deleted[id] = true
		return true
	}
	return false
}

func TestMemDeleteTool(t *testing.T) {
	store := &fakeMemDeleter{deleted: map[string]bool{}}
	id, _ := store.Remember("该被忘掉的条目", nil)
	tool := NewMemDelete(store)

	if tool.Permission() != types.PermissionUserApproved {
		t.Errorf("memory.delete 权限 = %v，应为审批级", tool.Permission())
	}

	out, err := tool.Execute(context.Background(), map[string]any{"id": id})
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["deleted"] != true {
		t.Errorf("out = %v", out)
	}
	// 重复删除同一 ID：已删过的返回错误（幂等地拒绝，而不是假装又删了一次）
	if _, err := tool.Execute(context.Background(), map[string]any{"id": id}); err == nil {
		t.Error("重复删除应报错")
	}
	// 缺参数
	if _, err := tool.Execute(context.Background(), map[string]any{}); err == nil {
		t.Error("缺 id 应报错")
	}
}

// 只实现 MemoryStore 的适配器：删除必须显式报"不支持"，不能 panic 或静默。
func TestMemDeleteTool_UnsupportedStore(t *testing.T) {
	tool := NewMemDelete(&fakeMem{})
	if _, err := tool.Execute(context.Background(), map[string]any{"id": "x"}); err == nil {
		t.Error("不支持的存储应报错")
	}
}
