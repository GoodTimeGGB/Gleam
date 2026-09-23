package desktop

import (
	"context"
	"fmt"

	"gleam/internal/tools/toolutil"
	"gleam/pkg/types"
)

// 剪贴板拆成「读」「写」两个工具，而不是一个带 action 参数的工具。
//
// 原因：Permission() 不接受参数，一个"有时只读、有时写系统"的工具只能声明一种权限。
// 原先它声明 PermissionReadOnly，于是 action=write 也走只读通道、被自动放行，
// **整个审批闸门被绕过**——而门控的裁决发生在参数替换之前，拿到参数也改不了这一点。
// 拆开之后「读」自动放行、「写」按中风险走审批，与 file.read / file.write 对齐。
//
// 这条来自 harness 工程的一条判据：能力最小化要落在**工具粒度**上，
// 一个工具混装两种副作用等级时，声明哪一种都是错的。

// ClipboardReadTool 读取系统剪贴板（只读）。
type ClipboardReadTool struct{}

func NewClipboardRead() *ClipboardReadTool { return &ClipboardReadTool{} }

func (t *ClipboardReadTool) Name() string { return "desktop.clipboard.read" }
func (t *ClipboardReadTool) Description() string {
	return "读取系统剪贴板中的文本（只读）"
}
func (t *ClipboardReadTool) Permission() types.Permission { return types.PermissionReadOnly }
func (t *ClipboardReadTool) Schema() map[string]any {
	return toolutil.Schema("读取系统剪贴板文本", nil, map[string]any{})
}
func (t *ClipboardReadTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	text, err := clipboardRead()
	if err != nil {
		return nil, fmt.Errorf("读取剪贴板失败: %w", err)
	}
	return map[string]any{"text": text}, nil
}

// ClipboardWriteTool 写入系统剪贴板（会覆盖用户当前内容，中风险）。
type ClipboardWriteTool struct{}

func NewClipboardWrite() *ClipboardWriteTool { return &ClipboardWriteTool{} }

func (t *ClipboardWriteTool) Name() string { return "desktop.clipboard.write" }
func (t *ClipboardWriteTool) Description() string {
	return "把文本写入系统剪贴板（会覆盖用户当前剪贴板内容，需要用户批准）"
}
func (t *ClipboardWriteTool) Permission() types.Permission {
	return types.PermissionUserApproved
}
func (t *ClipboardWriteTool) Schema() map[string]any {
	return toolutil.Schema("写入文本到系统剪贴板", []string{"text"}, map[string]any{
		"text": toolutil.SchemaProp("要写入的文本", "string"),
	})
}
func (t *ClipboardWriteTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	text, err := toolutil.RequireStr(args, "text")
	if err != nil {
		return nil, err
	}
	if err := clipboardWrite(text); err != nil {
		return nil, fmt.Errorf("写入剪贴板失败: %w", err)
	}
	return map[string]any{"ok": true, "bytes": len(text)}, nil
}

// 以下函数由平台特定文件实现：
// Windows: clipboard_windows.go | macOS/Linux: clipboard_other.go
