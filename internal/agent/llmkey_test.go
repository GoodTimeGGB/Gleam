// llmkey_test.go 「这把 key 允许发给谁」的规则自测。
// 线路级的证明在 internal/webui/settings_test.go（假厂商收没收到鉴权头）；
// 这里钉的是规则本身，尤其是环境变量/config.yaml 注入那条——它不经过设置页。
package agent

import (
	"path/filepath"
	"testing"

	"gleam/internal/config"
	"gleam/internal/harness/credentials"
	"gleam/internal/llm"
)

const (
	glmURL = "https://open.bigmodel.cn/api/paas/v4"
	dsURL  = "https://api.deepseek.com/v1"
)

func newKeyEnv(t *testing.T) (*config.Config, *credentials.Store) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.Workspace = dir
	creds, err := credentials.Open(filepath.Join(dir, "credentials"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg, creds
}

// 环境变量注入的密钥同样受主机约束：启动时贴上当次主机，换厂商后不再跟着走。
// 这条最容易漏——它不走设置页，测试连接时也只看内存里那一份。
func TestLLMKey_InjectedKeyBoundAtStartup(t *testing.T) {
	cfg, creds := newKeyEnv(t)
	cfg.LLM.APIKey = "sk-env" // 模拟 GLEAM_API_KEY
	MarkInjectedLLMKey(cfg, glmURL)

	if k, _ := LLMKeyFor(cfg, creds, glmURL, ""); k != "sk-env" {
		t.Errorf("当次主机应可用，得到 %q", k)
	}
	if k, _ := LLMKeyFor(cfg, creds, dsURL, ""); k != "" {
		t.Errorf("换厂商后不该把 env 密钥发出去，得到 %q", k)
	}
	// 已标过主机的不再改：标记的含义是"这把当初配给谁"
	MarkInjectedLLMKey(cfg, dsURL)
	if cfg.LLM.APIKeyScope != llm.KeyScope(glmURL) {
		t.Errorf("scope 被覆盖成 %q", cfg.LLM.APIKeyScope)
	}
}

// 设置页保存后的生效值：填了新 key 就绑到新主机；没填就按主机向凭证要。
func TestBindLLMKey_FormAndSwitch(t *testing.T) {
	cfg, creds := newKeyEnv(t)

	if k, err := BindLLMKey(cfg, creds, glmURL, "  sk-glm  ", false); err != nil || k != "sk-glm" {
		t.Fatalf("绑定 = %q err=%v", k, err)
	}
	if cfg.LLM.APIKey != "sk-glm" || cfg.LLM.APIKeyScope != llm.KeyScope(glmURL) {
		t.Fatalf("生效值 = %q / %q", cfg.LLM.APIKey, cfg.LLM.APIKeyScope)
	}

	// 换厂商、不填 key：生效密钥清空，但原来那把仍在磁盘上等着被换回来
	if k, err := BindLLMKey(cfg, creds, dsURL, "", false); err != nil || k != "" {
		t.Fatalf("换厂商应拿不到密钥 = %q err=%v", k, err)
	}
	if cfg.LLM.APIKey != "" {
		t.Errorf("内存里不该还留着上一家的 key: %q", cfg.LLM.APIKey)
	}
	if k, _ := creds.ResolveLLMAPIKey(llm.KeyScope(glmURL)); k != "sk-glm" {
		t.Errorf("凭证不该被动过，得到 %q", k)
	}
	if k, _ := BindLLMKey(cfg, creds, glmURL, "", false); k != "sk-glm" {
		t.Errorf("换回应免重填，得到 %q", k)
	}

	// 留空 = 不修改：同一主机上再次保存别的字段，不能把 key 洗掉
	cfg.LLM.APIKey, cfg.LLM.APIKeyScope = "", ""
	if k, _ := BindLLMKey(cfg, creds, glmURL, "", false); k != "sk-glm" {
		t.Errorf("同主机重存应找回原 key，得到 %q", k)
	}

	// 显式清除：内存与磁盘同时
	if _, err := BindLLMKey(cfg, creds, glmURL, "", true); err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.APIKey != "" || cfg.LLM.APIKeyScope != "" {
		t.Errorf("清除后内存残留 %q / %q", cfg.LLM.APIKey, cfg.LLM.APIKeyScope)
	}
	if k, h := creds.ResolveLLMAPIKey(llm.KeyScope(glmURL)); k != "" || h != "" {
		t.Errorf("清除后磁盘残留 %q / %q", k, h)
	}
}

// 表单里显式填的 key 优先于一切（用户正在试一个新端点）。
func TestLLMKeyFor_ExplicitWins(t *testing.T) {
	cfg, creds := newKeyEnv(t)
	if _, err := BindLLMKey(cfg, creds, glmURL, "sk-glm", false); err != nil {
		t.Fatal(err)
	}
	if k, host := LLMKeyFor(cfg, creds, dsURL, "sk-trial"); k != "sk-trial" || host != llm.KeyScope(dsURL) {
		t.Errorf("显式 key = %q / %q", k, host)
	}
}
