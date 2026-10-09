package webui

import "runtime"

// isUnixPerm Windows 上 os.Chmod 只体现只读位，0600 无从断言。
func isUnixPerm() bool { return runtime.GOOS != "windows" }
