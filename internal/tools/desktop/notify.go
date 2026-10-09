package desktop

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	"gleam/internal/tools/toolutil"
	"gleam/pkg/types"
)

// NotifyTool 发送桌面通知。
type NotifyTool struct{}

func NewNotify() *NotifyTool { return &NotifyTool{} }

func (t *NotifyTool) Name() string { return "desktop.notify" }
func (t *NotifyTool) Description() string {
	return "发送桌面通知（系统弹窗/Toast）。用于任务完成、重要事件提醒"
}
func (t *NotifyTool) Permission() types.Permission { return types.PermissionReadOnly }
func (t *NotifyTool) Schema() map[string]any {
	return toolutil.Schema("发送桌面通知", []string{"title"}, map[string]any{
		"title":   toolutil.SchemaProp("通知标题", "string"),
		"message": toolutil.SchemaProp("通知内容", "string"),
	})
}
func (t *NotifyTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	title, err := toolutil.RequireStr(args, "title")
	if err != nil {
		return nil, err
	}
	message := toolutil.Str(args, "message")

	if err := Notify(title, message); err != nil {
		return nil, fmt.Errorf("发送通知失败: %w", err)
	}
	return map[string]any{"ok": true, "title": title, "message": message}, nil
}

// Notify 发送系统通知（按平台分支）。
//
// **导出是为了只有一份实现。** 定时任务跑完之后也要通知用户（调度器那条路径），
// 如果那里另写一份平台分支，两份迟早会漂移——而漂移的症状是"某个平台上通知不响"，
// 恰好是最不容易被发现的失败：没人会去三个平台上分别验证一遍通知。
func Notify(title, message string) error {
	switch runtime.GOOS {
	case "windows":
		// 使用 PowerShell BurntToast 或原生 toast
		ps := fmt.Sprintf(
			`[System.Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms') | Out-Null; $n=New-Object System.Windows.Forms.NotifyIcon; $n.Icon=[System.Drawing.SystemIcons]::Information; $n.Visible=$true; $n.ShowBalloonTip(5000,'%s','%s',[System.Windows.Forms.ToolTipIcon]::Info); Start-Sleep -Seconds 6; $n.Dispose()`,
			escapePS(title), escapePS(message))
		return exec.Command("powershell", "-NoProfile", "-Command", ps).Run()
	case "darwin":
		script := fmt.Sprintf(`display notification %q with title %q`, message, title)
		return exec.Command("osascript", "-e", script).Run()
	default:
		// 优先 notify-send（Linux 桌面环境标配）
		if _, err := exec.LookPath("notify-send"); err == nil {
			return exec.Command("notify-send", title, message).Run()
		}
		return fmt.Errorf("未找到通知工具")
	}
}

func escapePS(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' {
			out = append(out, '\'', '\'')
		} else {
			out = append(out, c)
		}
	}
	return string(out)
}
