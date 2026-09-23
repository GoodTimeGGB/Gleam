// Package systray 提供极简的 Windows 系统托盘支持（纯 Go，无 CGO）。
// 其他平台以空实现 stub，保证跨平台编译通过。
package systray

import (
	"errors"
	"sync"
)

// MenuItem 托盘菜单项。
type MenuItem struct {
	Title    string
	OnClick  func()
	Disabled bool
	SepAfter bool
}

// Config 托盘配置。
type Config struct {
	Tooltip    string
	IconBytes  []byte // 必须是 .ico 格式字节（可留空，使用默认图标）
	OnActivate func() // 左键单击/双击回调（打开主窗口）
	MenuItems  []MenuItem
}

// Runner 托盘实例。
type Runner struct {
	mu     sync.Mutex
	cfg    Config
	quit   chan struct{}
	closed bool
}

// ErrNotImplemented 在非支持平台返回。
var ErrNotImplemented = errors.New("systray: not implemented on this platform")

// Run 启动托盘图标（阻塞直到 Quit 被调用）。
// 在非 Windows 平台立即返回 ErrNotImplemented。
func Run(cfg Config) error {
	return runPlatform(cfg)
}

// Quit 停止托盘。
func Quit() { quitPlatform() }

// SetTooltip 更新托盘提示文字。
func SetTooltip(tip string) { setTooltipPlatform(tip) }

// SetIcon 替换图标（ico 字节）。
func SetIcon(ico []byte) { setIconPlatform(ico) }

// UpdateMenu 重建菜单。
func UpdateMenu(items []MenuItem) { updateMenuPlatform(items) }
