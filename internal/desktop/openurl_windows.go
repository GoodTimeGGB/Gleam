//go:build windows

package desktop

import "os/exec"

// openURLPlatform 用系统默认浏览器打开外部链接（用于第三方登录授权页）。
func openURLPlatform(rawURL string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL).Start()
}
