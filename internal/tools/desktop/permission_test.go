package desktop

import (
	"path/filepath"
	"testing"
	"time"

	"gleam/internal/harness/safety"
	"gleam/pkg/types"
)

// TestClipboardSplitBySideEffect 剪贴板必须按副作用拆成读、写两个工具。
//
// Permission() 不接受参数，一个"有时只读、有时写系统"的工具只能声明一种权限。
// 原先的单一工具声明 PermissionReadOnly，于是 action=write 也走只读通道被自动放行，
// **整个审批闸门被绕过**——而门控的裁决发生在参数替换之前，拿到参数也改不了。
// 这条是 harness 工程"能力最小化要落在工具粒度上"的直接落点。
func TestClipboardSplitBySideEffect(t *testing.T) {
	read := NewClipboardRead()
	if read.Permission() != types.PermissionReadOnly {
		t.Errorf("剪贴板读取应为只读，实际 %v", read.Permission())
	}
	write := NewClipboardWrite()
	if write.Permission() != types.PermissionUserApproved {
		t.Errorf("剪贴板写入应为需批准，实际 %v", write.Permission())
	}
	if read.Name() == write.Name() {
		t.Error("读写必须是两个不同的工具名")
	}
}

// TestScreenshotDeclaresWritePermission 截屏会往磁盘落 PNG，不能声明只读。
func TestScreenshotDeclaresWritePermission(t *testing.T) {
	tool := NewScreenshot()
	if tool.Permission() == types.PermissionReadOnly {
		t.Fatal("截屏会落盘，不能声明只读（只读会走自动放行，绕过审批闸门）")
	}
	if tool.Permission() != types.PermissionUserApproved {
		t.Errorf("应为中风险，实际 %v", tool.Permission())
	}
	if got := tool.Paths(map[string]any{"path": "/tmp/a.png"}); len(got) != 1 || got[0] != "/tmp/a.png" {
		t.Errorf("Paths 应透传目标路径，实际 %v", got)
	}
	if got := tool.Paths(map[string]any{}); got != nil {
		t.Errorf("未指定路径应返回 nil（= 无法确认操作路径），实际 %v", got)
	}
}

// TestGateCatchesClipboardWrite 端到端确认闸门真的拦得住：
// 只读放行、写入必须审批、路径在信任区内的截屏可自动放行。
func TestGateCatchesClipboardWrite(t *testing.T) {
	dir := t.TempDir()
	g := safety.New("auto", nil, []string{dir}, []string{dir}, time.Second)

	if d := g.EvaluateStep(NewClipboardRead(), map[string]any{}, false); d.NeedApproval {
		t.Error("剪贴板读取是只读，不该需要审批")
	}
	if d := g.EvaluateStep(NewClipboardWrite(), map[string]any{"text": "x"}, false); !d.NeedApproval {
		t.Error("剪贴板写入在 auto 模式下必须需要审批")
	}

	// 截屏：路径在信任区内可放行；未给路径则要求人工确认（不默认放行）
	inTrust := map[string]any{"path": filepath.Join(dir, "shot.png")}
	if d := g.EvaluateStep(NewScreenshot(), inTrust, false); d.NeedApproval {
		t.Errorf("信任区内的截屏应自动放行，实际：%s", d.Reason)
	}
	if d := g.EvaluateStep(NewScreenshot(), map[string]any{}, false); !d.NeedApproval {
		t.Error("未指定路径的截屏应要求人工确认")
	}
}
