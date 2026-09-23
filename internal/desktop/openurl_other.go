//go:build !windows

package desktop

import (
	"os/exec"
	"runtime"
)

// openURLPlatform 用系统默认浏览器打开外部链接。
func openURLPlatform(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}
