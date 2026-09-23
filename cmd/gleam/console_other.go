//go:build !windows

package main

import "syscall"

// hasConsole 非 Windows 平台始终视为有控制台。
func hasConsole() bool { return true }

// procAttr 在非 Windows 平台无特殊属性。
func procAttr() *syscall.SysProcAttr { return nil }
