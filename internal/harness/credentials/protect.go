// protect.go 凭证文件的静态加密边界：Store 读写只经过 encrypt/decrypt 两个钩子。
// Windows 上由 DPAPI（当前用户域）实装（protect_windows.go），其余平台保持明文
// （protect_other.go）。文件格式为 magic 前缀 + base64(密文)；无 magic 的旧明文
// JSON 仍可读取，任何一次写入都会自动迁移成密文。
package credentials

import "encoding/base64"

// magic 标记加密文件；正常 JSON 以 '{' 开头，不会撞车。
const magic = "gleam-enc:v1:"

// encrypt 把明文 JSON 变成落盘字节。平台实现负责是否加密。
func encrypt(plain []byte) ([]byte, error) {
	blob, err := protectBytes(plain)
	if err != nil {
		return nil, err
	}
	if blob == nil { // 平台不加密：原样落盘
		return plain, nil
	}
	return []byte(magic + base64.StdEncoding.EncodeToString(blob)), nil
}

// decrypt 把落盘字节还原成明文 JSON；兼容未加密的旧文件。
func decrypt(data []byte) ([]byte, error) {
	raw := string(data)
	if len(raw) <= len(magic) || raw[:len(magic)] != magic {
		return data, nil // 旧明文文件
	}
	blob, err := base64.StdEncoding.DecodeString(raw[len(magic):])
	if err != nil {
		return nil, err
	}
	return unprotectBytes(blob)
}
