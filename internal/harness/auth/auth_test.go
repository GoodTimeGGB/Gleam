package auth

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"gleam/internal/harness/credentials"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	creds, err := credentials.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := creds.SetCloudConfig(credentials.CloudConfig{
		SupabaseURL: "https://example.supabase.co", SupabaseAnonKey: "test-anon",
	}); err != nil {
		t.Fatal(err)
	}
	return New(creds)
}

// TestCallbackPageEscapesMessage 回调页会把 provider 回传的 error_description
// 原样显示，若不转义就是本地回环上的 HTML 注入。
func TestCallbackPageEscapesMessage(t *testing.T) {
	payload := `<script>alert(1)</script>`
	page := callbackPage(false, payload)
	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Errorf("callbackPage 未转义: %s", page)
	}
	if !strings.Contains(page, "&lt;script&gt;") {
		t.Errorf("callbackPage 应转义为实体: %s", page)
	}
}

// TestStartOAuthCarriesState authorize URL 必须带随机 state（CSRF/session-fixation 防护）。
func TestStartOAuthCarriesState(t *testing.T) {
	m := newTestManager(t)
	authURL, err := m.StartOAuth("github")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatal("authorize URL 缺少 state")
	}
	if u.Query().Get("code_challenge_method") != "S256" {
		t.Error("PKCE 方法应为 S256")
	}
}

// TestOAuthCallbackRejectsStateMismatch 缺少/错误的 state 一律拒绝，绝不把 code 交给换令牌流程。
func TestOAuthCallbackRejectsStateMismatch(t *testing.T) {
	m := newTestManager(t)
	authURL, err := m.StartOAuth("github")
	if err != nil {
		t.Fatal(err)
	}
	base := queryOf(authURL, "redirect_to")
	if base == "" {
		t.Fatal("authorize URL 缺少 redirect_to")
	}
	waitForListener(t, base)

	// 1) 缺少 state：拒绝，且不得有 code 进入 codeCh
	get(t, base+"?code=SHOULD_NOT_BE_USED")
	// 2) 错误 state：同样拒绝
	get(t, base+"?code=SHOULD_NOT_BE_USED&state=forged")
	if len(m.pending.codeCh) != 0 {
		t.Fatal("state 不匹配的回调不应把 code 送入换令牌流程")
	}
}

func queryOf(rawurl, key string) string {
	u, _ := url.Parse(rawurl)
	return u.Query().Get(key)
}

func waitForListener(t *testing.T, callbackURL string) {
	t.Helper()
	u, _ := url.Parse(callbackURL)
	host := "http://" + u.Host
	for i := 0; i < 50; i++ {
		resp, err := http.Get(host + "/callback")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("回环监听器未能启动")
}

func get(t *testing.T, full string) string {
	t.Helper()
	resp, err := http.Get(full)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
