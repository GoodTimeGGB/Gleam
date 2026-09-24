//go:build windows

// protect_windows.go 用 Windows DPAPI（CryptProtectData，当前用户域）给凭证文件加密封装。
// 纯标准库 syscall 实现，不引第三方依赖；额外绑定一段应用熵，防止同用户下其它进程
// 直接调用未保护解密把文件当明文读走。
package credentials

import (
	"errors"
	"syscall"
	"unsafe"
)

var (
	crypt32              = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtectData = crypt32.NewProc("CryptProtectData")
	procCryptUnprotect   = crypt32.NewProc("CryptUnprotectData")
	kernel32LocalFree    = syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree")
)

// CRYPTPROTECT_UI_FORBIDDEN：静默调用，禁止弹任何系统授权框。
const cryptProtectUIForbidden = 0x1

// appEntropy 应用级附加熵。不是秘密（防的是"顺手解密"，不是同用户恶意进程）。
var appEntropy = []byte("Gleam.credentials.v1")

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newBlob(b []byte) dataBlob {
	d := dataBlob{cbData: uint32(len(b))}
	if len(b) > 0 {
		d.pbData = &b[0]
	}
	return d
}

func (d dataBlob) copyOut() []byte {
	if d.cbData == 0 || d.pbData == nil {
		return nil
	}
	out := make([]byte, d.cbData)
	copy(out, unsafe.Slice(d.pbData, d.cbData))
	return out
}

func protectBytes(plain []byte) ([]byte, error) {
	in := newBlob(plain)
	ent := newBlob(appEntropy)
	var out dataBlob
	r, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		uintptr(unsafe.Pointer(&ent)),
		0, 0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmtErr("CryptProtectData", err)
	}
	blob := out.copyOut()
	if out.pbData != nil {
		kernel32LocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	}
	return blob, nil
}

func unprotectBytes(blob []byte) ([]byte, error) {
	in := newBlob(blob)
	ent := newBlob(appEntropy)
	var out dataBlob
	r, _, err := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		uintptr(unsafe.Pointer(&ent)),
		0, 0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmtErr("CryptUnprotectData", err)
	}
	plain := out.copyOut()
	if out.pbData != nil {
		kernel32LocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	}
	return plain, nil
}

func fmtErr(op string, err error) error {
	if err == nil || err == syscall.Errno(0) {
		return errors.New(op + ": DPAPI 调用失败")
	}
	return errors.New(op + ": DPAPI 调用失败: " + err.Error())
}
