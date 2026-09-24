//go:build !windows

// protect_other.go 非 Windows 平台没有 DPAPI，维持既有明文行为（0600 权限是唯一防线）。
// 桌面发行版仅 Windows；此回落只为让测试与开发机在其它 OS 上可编译。
package credentials

import "errors"

func protectBytes(plain []byte) ([]byte, error) { return nil, nil }

func unprotectBytes(blob []byte) ([]byte, error) {
	return nil, errors.New("credentials: 文件已加密，但本平台没有 DPAPI 可解密")
}
