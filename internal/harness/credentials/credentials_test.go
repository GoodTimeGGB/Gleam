// credentials_test.go 密钥保存自测：落盘不含明文（Windows/DPAPI）、旧明文文件迁移、
// 主机绑定（换厂商不外发）、清除语义、损坏文件不崩。
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

const hostA = "open.bigmodel.cn"

func TestSetGetClearRoundTrip(t *testing.T) {
	s := openStore(t)
	if got, _ := s.ResolveLLMAPIKey(hostA); got != "" {
		t.Errorf("初始应为空，得到 %q", got)
	}
	if err := s.SetLLMAPIKey("sk-test-abcdef", hostA); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ResolveLLMAPIKey(hostA); got != "sk-test-abcdef" {
		t.Errorf("回读 = %q", got)
	}
	// 清除语义：空串 = 删除（连同主机），且文件仍可用
	if err := s.SetLLMAPIKey("", ""); err != nil {
		t.Fatal(err)
	}
	if got, host := s.ResolveLLMAPIKey(hostA); got != "" || host != "" {
		t.Errorf("清除后应全空，得到 %q / %q", got, host)
	}
}

// 主机绑定：存着 A 家的密钥，问 B 家要就是不给；问回 A 家又还在。
// 这一条挡的是"设置页换了厂商，旧 key 被原样发给新端点"。
func TestResolveLLMAPIKeyBoundToHost(t *testing.T) {
	s := openStore(t)
	if err := s.SetLLMAPIKey("sk-glm", hostA); err != nil {
		t.Fatal(err)
	}
	k, stored := s.ResolveLLMAPIKey("api.deepseek.com")
	if k != "" {
		t.Errorf("别家主机不该拿到密钥，拿到 %q", k)
	}
	if stored != hostA {
		t.Errorf("应回传密钥所属主机以便界面说明，得到 %q", stored)
	}
	// 换到别家、没填新 key：不能顺手把原来那把抹掉，用户切回来还得能用
	if k2, stored2 := s.ResolveLLMAPIKey(hostA); k2 != "sk-glm" || stored2 != hostA {
		t.Errorf("原密钥应原样保留，得到 %q / %q", k2, stored2)
	}
}

// 老版本存的密钥没有主机字段：第一次按主机取用时补绑，此后就受主机约束。
// 不补的话每次升级都全员"密钥凭空丢失"；补了之后仍挡得住换厂商外发。
func TestLegacyKeyBindsOnFirstUse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, File)
	if err := os.WriteFile(path, []byte(`{"llm_api_key":"sk-legacy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if k, _ := s.ResolveLLMAPIKey(hostA); k != "sk-legacy" {
		t.Fatalf("首次取用应给出并补绑，得到 %q", k)
	}
	if k, _ := s.ResolveLLMAPIKey("api.deepseek.com"); k != "" {
		t.Errorf("补绑后不该再发给别家，得到 %q", k)
	}
}

// 磁盘静态加密：Windows 上任何一次写入后，原始文件里不得再出现明文 key。
func TestNoPlaintextKeyOnDisk(t *testing.T) {
	s := openStore(t)
	if err := s.SetLLMAPIKey("sk-super-secret-123", hostA); err != nil {
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
	if k, _ := s.ResolveLLMAPIKey(hostA); k != "sk-super-secret-123" {
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
	if got, _ := s.ResolveLLMAPIKey(hostA); got != "sk-old-plain" {
		t.Fatalf("旧明文文件应可读，得到 %q", got)
	}
	if err := s.SetLLMAPIKey("sk-new", hostA); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if runtime.GOOS == "windows" && strings.Contains(string(raw), "sk-old-plain") {
		t.Error("迁移后旧明文 key 不应残留在文件里")
	}
	if k, _ := s.ResolveLLMAPIKey(hostA); k != "sk-new" {
		t.Error("迁移后回读失败")
	}
}

// 文件被截断/损坏时：读返回空；写拒绝覆盖（宁可让用户看一眼文件，
// 不能把"读不懂"静默当成"没有"，否则一次写就把旧凭证悄悄抹了）。
func TestCorruptFile(t *testing.T) {
	s := openStore(t)
	if err := os.WriteFile(s.Path(), []byte(magic+"@@@not-base64@@@"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ResolveLLMAPIKey(hostA); got != "" {
		t.Errorf("损坏文件应返回空，得到 %q", got)
	}
	if err := s.SetLLMAPIKey("k", hostA); err == nil {
		t.Error("损坏文件上写入应报错而不是静默覆盖")
	}
}

// 云端会话与配置共存：写 key 不丢会话，反之亦然。
func TestFieldsCoexist(t *testing.T) {
	s := openStore(t)
	if err := s.SetSession(&CloudSession{UserID: "u1", AccessToken: "tok"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLLMAPIKey("sk-x", hostA); err != nil {
		t.Fatal(err)
	}
	sess := s.GetSession()
	if sess == nil || sess.UserID != "u1" || sess.AccessToken != "tok" {
		t.Errorf("会话被覆盖: %+v", sess)
	}
	if k, _ := s.ResolveLLMAPIKey(hostA); k != "sk-x" {
		t.Errorf("密钥被覆盖: %q", k)
	}
	if err := s.ClearSession(); err != nil {
		t.Fatal(err)
	}
	if s.GetSession() != nil {
		t.Error("ClearSession 后仍有会话")
	}
	if k, _ := s.ResolveLLMAPIKey(hostA); k != "sk-x" {
		t.Error("登出不应动 key")
	}
}
