// Package auth 实现云端账号（Supabase Auth）：邮箱密码注册登录、
// GitHub/Google OAuth（PKCE + 本地回环回调）、会话刷新与登出。
// 仅"登录身份"存云端；会话令牌保存在本地凭证文件，其他业务数据一律不离开本机。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gleam/internal/harness/credentials"
)

// Manager 云端认证管理器。
type Manager struct {
	creds *credentials.Store
	http  *http.Client

	mu       sync.Mutex
	pending  *pendingOAuth
	listener *http.Server
}

type pendingOAuth struct {
	provider      string
	verifier      string
	codeCh        chan string
	errCh         chan error
	redirect      string
	expectedState string
}

// New 创建认证管理器。
func New(creds *credentials.Store) *Manager {
	return &Manager{
		creds: creds,
		http:  &http.Client{Timeout: 30 * time.Second},
	}
}

var (
	// ErrNotConfigured 尚未填写 Supabase 连接信息。
	ErrNotConfigured = errors.New("云端登录尚未配置：请先在「我的 → 账号」填写 Supabase 项目地址与 anon key")
	// ErrNotSignedIn 未登录。
	ErrNotSignedIn = errors.New("尚未登录")
)

func (m *Manager) cfg() (*credentials.CloudConfig, error) {
	c := m.creds.GetCloudConfig()
	if c == nil || c.SupabaseURL == "" || c.SupabaseAnonKey == "" {
		return nil, ErrNotConfigured
	}
	return c, nil
}

// Configured 是否已填写云端连接配置。
func (m *Manager) Configured() bool {
	_, err := m.cfg()
	return err == nil
}

// Configure 保存 Supabase 连接信息（anon key 为公开客户端标识，非机密）。
func (m *Manager) Configure(supabaseURL, anonKey string) error {
	supabaseURL = strings.TrimRight(strings.TrimSpace(supabaseURL), "/")
	anonKey = strings.TrimSpace(anonKey)
	if supabaseURL == "" || anonKey == "" {
		return errors.New("项目地址与 anon key 均不能为空")
	}
	if !strings.HasPrefix(supabaseURL, "https://") {
		return errors.New("项目地址需以 https:// 开头")
	}
	return m.creds.SetCloudConfig(credentials.CloudConfig{SupabaseURL: supabaseURL, SupabaseAnonKey: anonKey})
}

// SessionView 返回脱敏后的当前会话视图（供前端）。
type SessionView struct {
	SignedIn    bool   `json:"signed_in"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	Provider    string `json:"provider,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	Configured  bool   `json:"configured"`
}

// Session 返回当前会话（供需要强类型的调用方）。
func (m *Manager) View() SessionView {
	v := SessionView{Configured: m.Configured()}
	if s := m.creds.GetSession(); s != nil {
		v.SignedIn = true
		v.Email = s.Email
		v.DisplayName = s.DisplayName
		v.AvatarURL = s.AvatarURL
		v.Provider = s.Provider
		if !s.ExpiresAt.IsZero() {
			v.ExpiresAt = s.ExpiresAt.Format(time.RFC3339)
		}
	}
	return v
}

// Session 返回当前会话的 JSON 友好映射（实现 agent.AuthProvider）。
func (m *Manager) Session() map[string]any {
	v := m.View()
	out := map[string]any{
		"signed_in":    v.SignedIn,
		"configured":   v.Configured,
		"email":        v.Email,
		"display_name": v.DisplayName,
		"avatar_url":   v.AvatarURL,
		"provider":     v.Provider,
		"expires_at":   v.ExpiresAt,
	}
	// 回显连接配置（anon key 为公开客户端标识，可回显便于修改）。
	if c := m.creds.GetCloudConfig(); c != nil {
		out["supabase_url"] = c.SupabaseURL
		out["supabase_anon_key"] = c.SupabaseAnonKey
	}
	return out
}

// sbUser Supabase user 字段子集。
type sbUser struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	FullName  string `json:"full_name"`
	AvatarURL string `json:"avatar_url"`
}

// sbToken token 端点返回。
type sbToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	User         sbUser `json:"user"`
}

func (m *Manager) post(path string, body url.Values) (*sbToken, error) {
	c, err := m.cfg()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.SupabaseURL+path, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", c.SupabaseAnonKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if s := m.creds.GetSession(); s != nil && s.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.AccessToken)
	}
	return m.doToken(req)
}

// postJSON 用于 JSON body 的端点（signup）。
func (m *Manager) postJSON(path string, payload any) (*sbToken, error) {
	c, err := m.cfg()
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, c.SupabaseURL+path, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", c.SupabaseAnonKey)
	req.Header.Set("Content-Type", "application/json")
	return m.doToken(req)
}

func (m *Manager) doToken(req *http.Request) (*sbToken, error) {
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接云端失败：%w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, friendlyAuthError(resp.StatusCode, raw)
	}
	var t sbToken
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("解析云端响应失败：%w", err)
	}
	if t.AccessToken == "" {
		// signup 在"需邮箱确认"时不返回会话
		var hint struct {
			ConfirmationSentAt string `json:"confirmation_sent_at"`
			Msg                string `json:"msg"`
		}
		_ = json.Unmarshal(raw, &hint)
		return nil, errors.New("注册已提交。若项目开启了邮箱确认，请先到邮箱点击确认链接，再回来登录")
	}
	return &t, nil
}

func (m *Manager) saveSession(t *sbToken, provider string) error {
	sess := &credentials.CloudSession{
		Provider:     "supabase",
		UserID:       t.User.ID,
		Email:        t.User.Email,
		AvatarURL:    t.User.AvatarURL,
		DisplayName:  t.User.FullName,
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
	}
	if provider != "" {
		sess.Provider = "supabase:" + provider
	}
	if t.ExpiresIn > 0 {
		sess.ExpiresAt = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	}
	if sess.DisplayName == "" {
		sess.DisplayName = strings.Split(sess.Email, "@")[0]
	}
	return m.creds.SetSession(sess)
}

// SignUp 邮箱密码注册。
func (m *Manager) SignUp(email, password string) error {
	email, password = strings.TrimSpace(email), strings.TrimSpace(password)
	if email == "" || len(password) < 6 {
		return errors.New("请填写邮箱，且密码至少 6 位")
	}
	t, err := m.postJSON("/auth/v1/signup", map[string]string{"email": email, "password": password})
	if err != nil {
		return err
	}
	if t == nil || t.AccessToken == "" {
		return nil // 需要邮箱确认
	}
	return m.saveSession(t, "email")
}

// SignIn 邮箱密码登录。
func (m *Manager) SignIn(email, password string) error {
	email, password = strings.TrimSpace(email), strings.TrimSpace(password)
	if email == "" || password == "" {
		return errors.New("请输入邮箱和密码")
	}
	form := url.Values{}
	form.Set("email", email)
	form.Set("password", password)
	t, err := m.post("/auth/v1/token?grant_type=password", form)
	if err != nil {
		return err
	}
	return m.saveSession(t, "email")
}

// SignOut 登出（尽力通知云端撤销令牌），无论成败都清除本地会话。
func (m *Manager) SignOut() error {
	if s := m.creds.GetSession(); s != nil && s.AccessToken != "" {
		if c, err := m.cfg(); err == nil {
			req, _ := http.NewRequest(http.MethodPost, c.SupabaseURL+"/auth/v1/logout", nil)
			req.Header.Set("apikey", c.SupabaseAnonKey)
			req.Header.Set("Authorization", "Bearer "+s.AccessToken)
			if resp, err := m.http.Do(req); err == nil {
				resp.Body.Close()
			}
		}
	}
	return m.creds.ClearSession()
}

// ---------- OAuth（PKCE + 本地回环） ----------

func pkcePair() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// StartOAuth 启动第三方登录：生成本地回环地址并返回供浏览器打开的授权 URL。
// 授权完成后令牌自动换好并存入本地凭证；前端随后轮询 Session 即可。
func (m *Manager) StartOAuth(provider string) (string, error) {
	provider = strings.TrimSpace(strings.ToLower(provider))
	if provider != "github" && provider != "google" {
		return "", errors.New("目前支持 github 或 google 登录")
	}
	c, err := m.cfg()
	if err != nil {
		return "", err
	}
	verifier, challenge, err := pkcePair()
	if err != nil {
		return "", err
	}
	// CSRF 防护：随机 state 随授权 URL 发出，回调必须原样带回并核对，
	// 否则攻击者可用自己发起的 code 顶替当前登录（session fixation）。
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return "", err
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	// 本地回环监听器：接收 provider 重定向回来的 code
	ln, err := newLoopbackListener()
	if err != nil {
		return "", err
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Port())
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	// 校验失败的回调只回页面、不进 channel：errCh 只有 1 格，塞进去会让后续
	// 合法回调的 code 永久阻塞（缓冲满）。取消登录由 5 分钟超时兜底。
	reject := func(w http.ResponseWriter, reason string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, callbackPage(false, "登录失败："+reason))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// 先核对 state：不匹配说明回调不是本会话发起，直接拒绝，绝不接受 code。
		if got := q.Get("state"); got == "" || got != state {
			reject(w, "登录状态校验失败，请重新发起登录")
			return
		}
		if e := q.Get("error_description"); e != "" {
			errCh <- errors.New(e)
			reject(w, e)
			return
		}
		code := q.Get("code")
		if code == "" {
			errCh <- errors.New("回调缺少授权码")
			reject(w, "缺少授权码")
			return
		}
		codeCh <- code
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, callbackPage(true, "登录成功，可以回到 Gleam 了"))
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()

	pending := &pendingOAuth{provider: provider, verifier: verifier, codeCh: codeCh, errCh: errCh, redirect: redirect, expectedState: state}
	m.mu.Lock()
	m.pending = pending
	m.listener = srv
	m.mu.Unlock()

	go m.finishOAuth(ln, srv, pending)

	u, err := url.Parse(c.SupabaseURL + "/auth/v1/authorize")
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("provider", provider)
	q.Set("redirect_to", redirect)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (m *Manager) finishOAuth(ln io.Closer, srv *http.Server, p *pendingOAuth) {
	defer func() {
		_ = srv.Close()
		_ = ln.Close()
	}()
	var code string
	select {
	case code = <-p.codeCh:
	case err := <-p.errCh:
		_ = err
		return
	case <-time.After(5 * time.Minute):
		return
	}
	body := url.Values{}
	body.Set("grant_type", "pkce")
	body.Set("code", code)
	body.Set("code_verifier", p.verifier)
	c, err := m.cfg()
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, c.SupabaseURL+"/auth/v1/token", strings.NewReader(body.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("apikey", c.SupabaseAnonKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	t, err := m.doToken(req)
	if err != nil {
		return
	}
	_ = m.saveSession(t, p.provider)
}

// EnsureFreshToken 访问令牌临近过期时用 refresh_token 续期。
func (m *Manager) EnsureFreshToken() error {
	s := m.creds.GetSession()
	if s == nil {
		return ErrNotSignedIn
	}
	if !s.ExpiresAt.IsZero() && time.Until(s.ExpiresAt) > 5*time.Minute {
		return nil
	}
	c, err := m.cfg()
	if err != nil {
		return err
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", s.RefreshToken)
	req, _ := http.NewRequest(http.MethodPost, c.SupabaseURL+"/auth/v1/token", strings.NewReader(form.Encode()))
	req.Header.Set("apikey", c.SupabaseAnonKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	t, err := m.doToken(req)
	if err != nil {
		return err
	}
	return m.saveSession(t, strings.TrimPrefix(s.Provider, "supabase:"))
}

func friendlyAuthError(status int, body []byte) error {
	var e struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
		Msg         string `json:"msg"`
		Code        int    `json:"code"`
	}
	_ = json.Unmarshal(body, &e)
	msg := e.Description
	if msg == "" {
		msg = e.Msg
	}
	switch {
	case strings.Contains(msg, "Invalid login"):
		return errors.New("邮箱或密码不正确")
	case strings.Contains(msg, "already registered") || strings.Contains(msg, "already been registered"):
		return errors.New("该邮箱已注册，请直接登录")
	case strings.Contains(msg, "Email not confirmed"):
		return errors.New("邮箱尚未确认，请先查收确认邮件")
	case msg != "":
		return errors.New(msg)
	case status == http.StatusUnauthorized:
		return errors.New("邮箱或密码不正确")
	case status == http.StatusTooManyRequests:
		return errors.New("请求过于频繁，请稍后再试")
	default:
		return fmt.Errorf("云端返回错误（%d）", status)
	}
}

func callbackPage(ok bool, msg string) string {
	color := "#34D399"
	if !ok {
		color = "#EF4444"
	}
	// msg 可能含 provider 回传的 error_description（外部可控），必须转义后再进 HTML。
	msg = html.EscapeString(msg)
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>登录</title>
<style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0B0D12;color:#E6EAF2;
font-family:"Segoe UI",-apple-system,sans-serif}.card{text-align:center;padding:40px}
.dot{width:48px;height:48px;border-radius:50%;margin:0 auto 18px;background:` + color + `;
box-shadow:0 0 24px ` + color + `66}h2{font-size:18px;font-weight:600;margin:0 0 8px}p{color:#8B94A7;margin:0}</style></head>
<body><div class="card"><div class="dot"></div><h2>` + msg + `</h2><p>你可以关闭这个页面</p></div></body></html>`
}
