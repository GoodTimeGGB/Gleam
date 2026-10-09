package webui

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 守卫的拒绝路径必须对着「真的不带口令」的服务测，所以这里不用 newTokenTestServer。
func newGuardedServer(t *testing.T) (*fixture, *httptest.Server) {
	t.Helper()
	f := newFixture(t, nil)
	ts := httptest.NewServer(f.srv.Handler())
	t.Cleanup(ts.Close)
	return f, ts
}

type reqOpt func(*http.Request)

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }
func withHost(h string) reqOpt      { return func(r *http.Request) { r.Host = h } }

func doReq(t *testing.T, method, rawURL, body string, opts ...reqOpt) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range opts {
		o(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

const goalBody = `{"goal":"守卫测试","task_mode":"chat"}`

func TestGuard_MissingOrWrongTokenRejected(t *testing.T) {
	_, ts := newGuardedServer(t)
	for name, opts := range map[string][]reqOpt{
		"缺口令": {withHeader("Content-Type", "application/json")},
		"错口令": {withHeader("Content-Type", "application/json"), withHeader(TokenHeader, "not-the-token-at-all")},
	} {
		resp := doReq(t, "POST", ts.URL+"/api/goals", goalBody, opts...)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: POST /api/goals = %d，应为 401", name, resp.StatusCode)
		}
	}
	if resp := doReq(t, "GET", ts.URL+"/api/goals", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /api/goals 无口令 = %d，应为 401", resp.StatusCode)
	}
	if resp := doReq(t, "GET", ts.URL+"/api/events", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("SSE 无口令 = %d，应为 401", resp.StatusCode)
	}
	// 401 也要带上服务标识：单实例探测靠它认出「端口上已经是 Gleam」。
	resp := doReq(t, "GET", ts.URL+"/api/info", "")
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get(serverHeader) != "gleam" {
		t.Errorf("GET /api/info 无口令 = %d / %q", resp.StatusCode, resp.Header.Get(serverHeader))
	}
}

func TestGuard_QueryTokenOnlyForReads(t *testing.T) {
	f, ts := newGuardedServer(t)
	q := "?" + tokenQueryParam + "=" + url.QueryEscape(f.srv.Token())
	if resp := doReq(t, "GET", ts.URL+"/api/info"+q, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("GET 带查询口令 = %d，应为 200", resp.StatusCode)
	}
	resp := doReq(t, "POST", ts.URL+"/api/goals"+q, goalBody, withHeader("Content-Type", "application/json"))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST 只带查询口令 = %d，应为 401（改状态的请求必须用请求头）", resp.StatusCode)
	}
}

func TestGuard_CrossOriginPostRejected(t *testing.T) {
	f, ts := newGuardedServer(t)
	tok := withHeader(TokenHeader, f.srv.Token())
	ct := withHeader("Content-Type", "application/json")
	cases := map[string][]reqOpt{
		"外站 Origin":                  {tok, ct, withHeader("Origin", "https://evil.example")},
		"同主机不同端口":                    {tok, ct, withHeader("Origin", "http://127.0.0.1:5173")},
		"null Origin":                {tok, ct, withHeader("Origin", "null")},
		"Sec-Fetch-Site 跨站，无 Origin": {tok, ct, withHeader("Sec-Fetch-Site", "cross-site")},
	}
	for name, opts := range cases {
		resp := doReq(t, "POST", ts.URL+"/api/goals", goalBody, opts...)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: = %d，应为 403", name, resp.StatusCode)
		}
	}
	// 同源 Origin 放行
	resp := doReq(t, "POST", ts.URL+"/api/goals", goalBody, tok, ct, withHeader("Origin", ts.URL), withHeader("Sec-Fetch-Site", "same-origin"))
	if resp.StatusCode != http.StatusOK {
		t.Errorf("同源 POST = %d，应为 200", resp.StatusCode)
	}
}

func TestGuard_NonJSONBodyRejected(t *testing.T) {
	f, ts := newGuardedServer(t)
	tok := withHeader(TokenHeader, f.srv.Token())
	for _, ct := range []string{"text/plain", "text/plain;charset=UTF-8", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"} {
		resp := doReq(t, "POST", ts.URL+"/api/goals", goalBody, tok, withHeader("Content-Type", ct))
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q = %d，应为 415", ct, resp.StatusCode)
		}
	}
	// 有体但没声明类型也不行
	resp := doReq(t, "POST", ts.URL+"/api/goals", goalBody, tok)
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("有体无 Content-Type = %d，应为 415", resp.StatusCode)
	}
	// 无体的 POST（心跳）照常
	if resp := doReq(t, "POST", ts.URL+"/api/heartbeat", "", tok); resp.StatusCode != http.StatusOK {
		t.Errorf("无体心跳 = %d，应为 200", resp.StatusCode)
	}
	// 带参数的 JSON 类型放行
	if resp := doReq(t, "POST", ts.URL+"/api/goals", goalBody, tok, withHeader("Content-Type", "application/json; charset=utf-8")); resp.StatusCode != http.StatusOK {
		t.Errorf("application/json; charset=utf-8 = %d，应为 200", resp.StatusCode)
	}
}

func TestGuard_BadHostRejected(t *testing.T) {
	f, ts := newGuardedServer(t)
	_, port, _ := strings.Cut(strings.TrimPrefix(ts.URL, "http://"), ":")
	tok := withHeader(TokenHeader, f.srv.Token())
	for _, h := range []string{
		"evil.example:" + port,     // DNS 重绑定的典型形态：外部域名指向 127.0.0.1
		"127.0.0.1.nip.io:" + port, // 解析到回环的域名也不行
		"127.0.0.1:1",              // 端口不对
		"192.168.1.10:" + port,     // 回环监听时不认非回环 IP
	} {
		for _, path := range []string{"/api/info", "/"} {
			resp := doReq(t, "GET", ts.URL+path, "", tok, withHost(h))
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("Host %q %s = %d，应为 403", h, path, resp.StatusCode)
			}
		}
	}
	for _, h := range []string{"127.0.0.1:" + port, "localhost:" + port, "[::1]:" + port} {
		if resp := doReq(t, "GET", ts.URL+"/api/info", "", tok, withHost(h)); resp.StatusCode != http.StatusOK {
			t.Errorf("Host %q = %d，应为 200", h, resp.StatusCode)
		}
	}
}

func TestGuard_EmptyHostRejected(t *testing.T) {
	f := newFixture(t, nil)
	req := httptest.NewRequest("GET", "/api/info", nil)
	req.Host = ""
	req.Header.Set(TokenHeader, f.srv.Token())
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("空 Host = %d，应为 403", rec.Code)
	}
}

func TestGuard_BindAddrLiteralAllowed(t *testing.T) {
	f, ts := newGuardedServer(t)
	_, port, _ := strings.Cut(strings.TrimPrefix(ts.URL, "http://"), ":")
	f.srv.BindAddr = "gleam-box.lan:" + port
	resp := doReq(t, "GET", ts.URL+"/api/info", "", withHeader(TokenHeader, f.srv.Token()), withHost("gleam-box.lan:"+port))
	if resp.StatusCode != http.StatusOK {
		t.Errorf("BindAddr 原文作为 Host = %d，应为 200", resp.StatusCode)
	}
}

func TestGuard_IndexInjectsTokenForLoopback(t *testing.T) {
	f, ts := newGuardedServer(t)
	resp := doReq(t, "GET", ts.URL+"/", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d", resp.StatusCode)
	}
	body := readAll(t, resp)
	want := `<meta name="gleam-token" content="` + f.srv.Token() + `">`
	if !strings.Contains(body, want) {
		t.Errorf("首页没有注入口令标签")
	}
	if !strings.Contains(body, "<title>") {
		t.Errorf("首页内容不完整")
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("首页 Cache-Control = %q，应为 no-store", cc)
	}
	if rp := resp.Header.Get("Referrer-Policy"); rp != "no-referrer" {
		t.Errorf("首页 Referrer-Policy = %q", rp)
	}
}

func TestGuard_IndexNoTokenForRemoteWithoutQuery(t *testing.T) {
	f := newFixture(t, nil)
	f.srv.BindAddr = "0.0.0.0:8787"
	h := f.srv.Handler()
	for _, tc := range []struct {
		name, target string
		want         bool
	}{
		{"局域网直接打开", "/", false},
		{"局域网带正确口令打开", "/?token=" + url.QueryEscape(f.srv.Token()), true},
		{"局域网带错误口令打开", "/?token=wrong-token-value-123", false},
	} {
		req := httptest.NewRequest("GET", "http://192.168.1.10:8787"+tc.target, nil)
		req.RemoteAddr = "192.168.1.20:50000"
		req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, fakeAddr("192.168.1.10:8787")))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: = %d", tc.name, rec.Code)
		}
		got := strings.Contains(rec.Body.String(), `name="gleam-token"`)
		if got != tc.want {
			t.Errorf("%s: 注入 = %v，应为 %v", tc.name, got, tc.want)
		}
	}
}

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

func TestGuard_ShowWindowLoopbackWithoutToken(t *testing.T) {
	f, ts := newGuardedServer(t)
	called := make(chan struct{}, 1)
	f.srv.ShowWindowFunc = func() { called <- struct{}{} }
	resp := doReq(t, "POST", ts.URL+"/api/show-window", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("回环 show-window 无口令 = %d，应为 200", resp.StatusCode)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("ShowWindowFunc 没被调用")
	}
	// 免口令不等于免其他校验：浏览器跨站发来的仍然拒绝。
	resp = doReq(t, "POST", ts.URL+"/api/show-window", "", withHeader("Origin", "https://evil.example"))
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("跨站 show-window = %d，应为 403", resp.StatusCode)
	}
}

// TestGuard_HappyPathWithSSE 正常形态：首页拿口令 → 请求头带口令提交目标 → SSE 用查询参数带口令收到事件。
func TestGuard_HappyPathWithSSE(t *testing.T) {
	f, ts := newGuardedServer(t)
	tok := f.srv.Token()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events?token="+url.QueryEscape(tok), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("SSE = %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	events := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "event: ") {
				events <- strings.TrimPrefix(line, "event: ")
			}
		}
		close(events)
	}()

	post := doReq(t, "POST", ts.URL+"/api/goals", goalBody,
		withHeader("Content-Type", "application/json"), withHeader(TokenHeader, tok),
		withHeader("Origin", ts.URL), withHeader("Sec-Fetch-Site", "same-origin"))
	if post.StatusCode != http.StatusOK {
		t.Fatalf("提交目标 = %d", post.StatusCode)
	}
	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("SSE 在收到事件前就断了")
		}
		t.Logf("首个 SSE 事件: %s", ev)
	case <-ctx.Done():
		t.Fatal("10s 内没有收到任何 SSE 事件")
	}
}

func TestGuard_TokenFile(t *testing.T) {
	f := newFixture(t, nil)
	p, err := f.srv.WriteTokenFile()
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(f.dataDir, TokenFileName) {
		t.Errorf("口令文件位置 = %s", p)
	}
	b, err := os.ReadFile(p)
	if err != nil || strings.TrimSpace(string(b)) != f.srv.Token() {
		t.Fatalf("口令文件内容不对: %v", err)
	}
	if st, err := os.Stat(p); err == nil && isUnixPerm() && st.Mode().Perm() != 0o600 {
		t.Errorf("口令文件权限 = %v，应为 0600", st.Mode().Perm())
	}
}

func TestGuard_TokenFromEnv(t *testing.T) {
	t.Setenv(TokenEnv, "short")
	if got := tokenOrNew(); got == "short" || len(got) < minTokenLen {
		t.Errorf("过短的预置口令不该被采用: %q", got)
	}
	t.Setenv(TokenEnv, "a-sufficiently-long-preset-token")
	if got := tokenOrNew(); got != "a-sufficiently-long-preset-token" {
		t.Errorf("预置口令没被采用: %q", got)
	}
	t.Setenv(TokenEnv, "")
	a, b := tokenOrNew(), tokenOrNew()
	if a == b || len(a) < 40 {
		t.Errorf("随机口令应每次不同且足够长: %q %q", a, b)
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	var sb strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		sb.WriteString(sc.Text())
		sb.WriteByte('\n')
	}
	return sb.String()
}
