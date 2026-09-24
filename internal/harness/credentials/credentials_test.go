// credentials_test.go 密钥保存自测：落盘不含明文（Windows/DPAPI）、旧明文文件迁移、
// 清除语义、损坏文件不崩。
package credentials

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSetGetClearRoundTrip(t *testing.T) {
	s := openStore(t)
	if got := s.GetLLMAPIKey(); got != "" {
		t.Errorf("初始应为空，得到 %q", got)
	}
	if err := s.SetLLMAPIKey("sk-test-abcdef"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetLLMAPIKey(); got != "sk-test-abcdef" {
		t.Errorf("回读 = %q", got)
	}
	// 清除语义：空串 = 删除，且文件仍可用
	if err := s.SetLLMAPIKey(""); err != nil {
		t.Fatal(err)
	}
	if got := s.GetLLMAPIKey(); got != "" {
		t.Errorf("清除后 = %q，应为空", got)
	}
}

// 磁盘静态加密：Windows 上任何一次写入后，原始文件里不得再出现明文 key。
func TestNoPlaintextKeyOnDisk(t *testing.T) {
	s := openStore(t)
	if err := s.SetLLMAPIKey("sk-super-secret-123"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if strings.Contains(string(raw), "sk-super-secret-123") {
			t.Error("DPAPI 应已加密，但明文 key 仍出现在文件里")
		}
		if !strings.HasPrefix(string(raw), magic) {
			t.Errorf("缺少加密标记前缀，头部 = %.20q", string(raw))
		}
	}
	// 任何平台都必须能回读
	if s.GetLLMAPIKey() != "sk-super-secret-123" {
		t.Error("回读失败")
	}
}

// 旧版本明文文件：可读，且下一次写入自动迁移为新格式。
func TestLegacyPlaintextMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, File)
	if err := os.WriteFile(path, []byte(`{"llm_api_key":"sk-old-plain"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.GetLLMAPIKey(); got != "sk-old-plain" {
		t.Fatalf("旧明文文件应可读，得到 %q", got)
	}
	if err := s.SetLLMAPIKey("sk-new"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if runtime.GOOS == "windows" && strings.Contains(string(raw), "sk-old-plain") {
		t.Error("迁移后旧明文 key 不应残留在文件里")
	}
	if s.GetLLMAPIKey() != "sk-new" {
		t.Error("迁移后回读失败")
	}
}

// 文件被截断/损坏时：Get 吞错返回空；Set 拒绝覆盖（宁可让用户看一眼文件，
// 不能把"读不懂"静默当成"没有"，否则一次写就把旧凭证悄悄抹了）。
func TestCorruptFile(t *testing.T) {
	s := openStore(t)
	if err := os.WriteFile(s.Path(), []byte(magic+"@@@not-base64@@@"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.GetLLMAPIKey(); got != "" {
		t.Errorf("损坏文件应返回空，得到 %q", got)
	}
	if err := s.SetLLMAPIKey("k"); err == nil {
		t.Error("损坏文件上写入应报错而不是静默覆盖")
	}
}

// 云端会话与配置共存：写 key 不丢会话，反之亦然。
func TestFieldsCoexist(t *testing.T) {
	s := openStore(t)
	if err := s.SetSession(&CloudSession{UserID: "u1", AccessToken: "tok"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLLMAPIKey("sk-x"); err != nil {
		t.Fatal(err)
	}
	sess := s.GetSession()
	if sess == nil || sess.UserID != "u1" || sess.AccessToken != "tok" {
		t.Errorf("会话被覆盖: %+v", sess)
	}
	if err := s.ClearSession(); err != nil {
		t.Fatal(err)
	}
	if s.GetSession() != nil {
		t.Error("ClearSession 后仍有会话")
	}
	if s.GetLLMAPIKey() != "sk-x" {
		t.Error("登出不应动 key")
	}
}
