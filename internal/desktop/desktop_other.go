//go:build !windows

package desktop

func openWindowPlatform(string, func()) bool { return false }
func closeWindowPlatform()                   {}
func windowOpenPlatform() bool               { return false }
