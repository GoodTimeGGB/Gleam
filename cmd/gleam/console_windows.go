//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

var procGetConsoleCP = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleCP")

// hasConsole 报告当前进程是否附着控制台。
// windowsgui 子系统的桌面版双击启动时没有控制台（GetConsoleCP 返回 0），
// 据此默认进入应用模式；探测本身异常时按"无控制台"处理（桌面版语义优先）。
func hasConsole() (has bool) {
	defer func() {
		if recover() != nil {
			has = false
		}
	}()
	r, _, _ := procGetConsoleCP.Call()
	return r != 0
}

// procAttr 为子进程设置隐藏窗口属性，避免在 windowsgui 模式下弹出黑框。
func procAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}

// keep unused import check happy
var _ = exec.Cmd{}
