package desktop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"gleam/internal/tools/toolutil"
	"gleam/pkg/types"
)

// ScreenshotTool 截取屏幕画面并保存为文件。
type ScreenshotTool struct{}

func NewScreenshot() *ScreenshotTool { return &ScreenshotTool{} }

func (t *ScreenshotTool) Name() string { return "desktop.screenshot" }
func (t *ScreenshotTool) Description() string {
	return "截取当前屏幕画面并保存为 PNG 文件。返回文件路径（会写入磁盘，需要用户批准）"
}
func (t *ScreenshotTool) Permission() types.Permission {
	// 这个工具会把 PNG 落到磁盘上，原先声明 PermissionReadOnly 是错的——
	// 只读会走自动放行通道，等于绕过审批闸门。
	return types.PermissionUserApproved
}

// Paths 实现 types.PathAware：截屏会落盘，把目标路径交给门控判断是否在信任路径内。
// 未指定 path 时返回 nil（= 无法确认操作路径），由门控要求人工确认，而不是默认放行。
func (t *ScreenshotTool) Paths(ctx context.Context, args map[string]any) []string {
	if p := toolutil.Str(args, "path"); p != "" {
		return []string{p}
	}
	return nil
}

func (t *ScreenshotTool) Schema() map[string]any {
	return toolutil.Schema("截取屏幕", nil, map[string]any{
		"path": toolutil.SchemaProp("保存路径（默认工作目录下 screenshot-时间戳.png）", "string"),
	})
}
func (t *ScreenshotTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	path := toolutil.Str(args, "path")
	if path == "" {
		path = filepath.Join(".", fmt.Sprintf("screenshot-%d.png", time.Now().Unix()))
	}

	var err error
	switch runtime.GOOS {
	case "windows":
		err = takeScreenshotWindows(path)
	case "darwin":
		err = takeScreenshotMac(path)
	default:
		err = takeScreenshotLinux(path)
	}
	if err != nil {
		return nil, fmt.Errorf("截屏失败: %w", err)
	}
	abs, _ := filepath.Abs(path)
	return map[string]any{"path": path, "absolute": abs}, nil
}

// Windows: 使用 PowerShell + .NET 截屏
func takeScreenshotWindows(path string) error {
	ps := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms,System.Drawing; $b=[System.Drawing.Rectangle]::new(0,0,[System.Windows.Forms.Screen]::PrimaryScreen.Bounds.Width,[System.Windows.Forms.Screen]::PrimaryScreen.Bounds.Height); $bmp=[System.Drawing.Bitmap]::new($b.Width,$b.Height); $g=[System.Drawing.Graphics]::FromImage($bmp); $g.CopyFromScreen(0,0,0,0,$b.Size); $bmp.Save('%s',[System.Drawing.Imaging.ImageFormat]::Png); $g.Dispose(); $bmp.Dispose()`, path)
	cmd := exec.Command("powershell", "-NoProfile", "-Command", ps)
	return cmd.Run()
}

func takeScreenshotMac(path string) error {
	return exec.Command("screencapture", "-x", path).Run()
}

func takeScreenshotLinux(path string) error {
	// 优先 scrot，回退 gnome-screenshot
	if _, err := exec.LookPath("scrot"); err == nil {
		return exec.Command("scrot", path).Run()
	}
	if _, err := exec.LookPath("gnome-screenshot"); err == nil {
		return exec.Command("gnome-screenshot", "-f", path).Run()
	}
	if _, err := exec.LookPath("import"); err == nil {
		return exec.Command("import", "-window", "root", path).Run()
	}
	// 检查文件是否被创建
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("未找到截屏工具（scrot/gnome-screenshot/import）")
	}
	return nil
}
