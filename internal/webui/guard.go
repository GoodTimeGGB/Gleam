package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gleam/internal/atomicfile"
)

// 本机 API 守卫。
//
// **为什么要有**：这个服务开在 127.0.0.1 上，但「只有本机能连」不等于「只有 Gleam 的界面能用」。
// 用户浏览器里打开的任何网页，都能向回环地址发出跨站请求：响应它读不到，但请求本身会被执行——
// 而这里的请求是「提交一个目标」「批准一次审批」。再加上 DNS 重绑定，一个外部域名可以
// 在浏览器眼里变成同源，连响应也读得到。所以这一层要回答三件事：
//
//  1. 请求是冲着我来的吗？—— Host 白名单（挡 DNS 重绑定：重绑定必须用域名，而这里只认 IP 字面量与 localhost）。
//  2. 是我自己的页面发的吗？—— 改状态的请求校验 Origin / Sec-Fetch-Site；JSON 体必须声明 application/json
//     （text/plain 与表单是浏览器免预检就能跨站发的形态）。
//  3. 发请求的人拿得到本次启动的口令吗？—— 每次启动随机生成，只注入给本机打开的首页，
//     并写一份 0600 的口令文件给同一用户的脚本与 CLI 用。
//
// 口令挡不住「同一用户下的本机进程」：它们能读口令文件。这是有意的边界——那类进程本来就能
// 直接读写 ~/.gleam，口令要挡的是浏览器里的网页与局域网里的其他设备。

// TokenHeader 前端与脚本携带口令的请求头。
const TokenHeader = "X-Gleam-Token"

// TokenEnv 预置口令的环境变量（脚本、冒烟、将来的桌面壳用）：设了就用它，不再随机生成。
const TokenEnv = "GLEAM_WEBUI_TOKEN"

// TokenFileName 口令文件名，位于数据目录下，权限 0600。
const TokenFileName = "webui.token"

// tokenQueryParam GET 请求可以用查询参数带口令：EventSource 与 <img> 设不了请求头。
const tokenQueryParam = "token"

// serverHeader 让单实例探测能认出「端口上是 Gleam」，而不必先拿到口令。
const serverHeader = "X-Gleam-Server"

const minTokenLen = 16

// newToken 生成 32 字节随机口令（base64url，无填充）。
func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败意味着系统熵源坏了；宁可起不来，也不要发一个可猜的口令。
		panic(fmt.Sprintf("webui: 生成口令失败: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// tokenFromEnv 读取预置口令；太短视为未设置（并提示），免得有人设成 "1" 还以为受保护。
func tokenFromEnv() string {
	v := strings.TrimSpace(os.Getenv(TokenEnv))
	if v == "" {
		return ""
	}
	if len(v) < minTokenLen {
		fmt.Fprintf(os.Stderr, "[gleam] %s 少于 %d 个字符，已忽略并改用随机口令\n", TokenEnv, minTokenLen)
		return ""
	}
	return v
}

// Token 返回本次启动的 API 口令。
func (s *Server) Token() string { return s.token }

// TokenFilePath 口令文件的位置（<DataDir>/webui.token）。
func (s *Server) TokenFilePath() string {
	return filepath.Join(s.Agent.DataDir(), TokenFileName)
}

// WriteTokenFile 把口令写进数据目录（0600），供同一用户的 CLI / 脚本读取。
// 由启动 HTTP 服务的那一层调用：只有真的对外提供 HTTP 时才需要这份文件。
func (s *Server) WriteTokenFile() (string, error) {
	p := s.TokenFilePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	if err := atomicfile.Write(p, []byte(s.token+"\n"), 0o600); err != nil {
		return "", err
	}
	return p, nil
}

// guard 包在整个路由外面：Host 校验对所有路径生效（首页里带着口令，必须先挡住重绑定）；
// Origin / Content-Type / 口令只管 /api/*。
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(serverHeader, "gleam")
		if !s.hostAllowed(r) {
			writeErr(w, http.StatusForbidden, "Host 不在允许范围内")
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if isStateChanging(r.Method) {
			if !originAllowed(r) {
				writeErr(w, http.StatusForbidden, "跨站请求已拒绝")
				return
			}
			if !contentTypeAllowed(r) {
				writeErr(w, http.StatusUnsupportedMediaType, "请求体必须是 application/json")
				return
			}
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/show-window" && isLoopbackRemote(r) {
			// 单实例唤起：第二个进程在拿到数据目录之前就要发这一条，拿不到口令。
			// 它只会「把已有窗口拉到前台」，不读不写任何数据；仍受上面的 Host / Origin 约束，
			// 且只认回环来源。
			next.ServeHTTP(w, r)
			return
		}
		if !s.tokenOK(r) {
			writeErr(w, http.StatusUnauthorized, "缺少或错误的 API 口令")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) tokenOK(r *http.Request) bool {
	got := r.Header.Get(TokenHeader)
	if got == "" {
		if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
			got = strings.TrimSpace(strings.TrimPrefix(a, "Bearer "))
		}
	}
	if got == "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		got = r.URL.Query().Get(tokenQueryParam)
	}
	return tokenEqual(got, s.token)
}

func tokenEqual(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// originAllowed 改状态的请求：没有 Origin（CLI、脚本）可以，但浏览器声明了 cross-site 不行；
// 有 Origin 就必须与本服务同源（http://<已通过校验的 Host>）。
func originAllowed(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	return strings.EqualFold(o, "http://"+r.Host)
}

// contentTypeAllowed 有请求体就必须是 application/json；声明了别的类型（哪怕体是空的）也拒绝。
// 没有请求体、也没声明类型的 POST（心跳、取消、回调触发）照常放行。
func contentTypeAllowed(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return r.ContentLength == 0
	}
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && mt == "application/json"
}

// hostAllowed Host 必须指向本服务：
//   - 端口必须是监听端口；
//   - 主机名只认 localhost 与 IP 字面量（DNS 重绑定需要一个域名，IP 字面量重绑不了）；
//   - 监听在回环时只认回环 IP；
//   - 显式写在 BindAddr 里的 host:port 原样放行（例如 --addr myhost:8787）。
func (s *Server) hostAllowed(r *http.Request) bool {
	host := r.Host
	if host == "" {
		return false
	}
	if s.BindAddr != "" && strings.EqualFold(host, s.BindAddr) {
		return true
	}
	port := listenPort(r, s.BindAddr)
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		// 没带端口：只有监听在 80 时浏览器才会这么发。
		h, p = strings.Trim(host, "[]"), "80"
	}
	if port == "" || p != port {
		return false
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	return !bindIsLoopback(s.BindAddr)
}

// listenPort 优先用连接实际落到的本地地址（最可信），其次用 BindAddr。
func listenPort(r *http.Request, bindAddr string) string {
	if la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok && la != nil {
		if _, p, err := net.SplitHostPort(la.String()); err == nil {
			return p
		}
	}
	if _, p, err := net.SplitHostPort(bindAddr); err == nil {
		return p
	}
	return ""
}

// bindIsLoopback 空的 BindAddr 按回环处理（测试与未声明的宿主都是回环）。
func bindIsLoopback(bindAddr string) bool {
	if strings.TrimSpace(bindAddr) == "" {
		return true
	}
	h, _, err := net.SplitHostPort(bindAddr)
	if err != nil {
		h = bindAddr
	}
	h = strings.Trim(h, "[]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// BindIsLoopback 供宿主层判断要不要打印「非回环」警告。
func (s *Server) BindIsLoopback() bool { return bindIsLoopback(s.BindAddr) }

func isLoopbackRemote(r *http.Request) bool {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		h = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

// tokenMeta 首页注入的口令标签。只给本机来源，或已经带着正确口令打开首页的人
// （非回环监听时，局域网设备用启动日志里打印的 ?token= 链接打开）。
func (s *Server) tokenMeta(r *http.Request) string {
	if !isLoopbackRemote(r) && !tokenEqual(r.URL.Query().Get(tokenQueryParam), s.token) {
		return ""
	}
	return `<meta name="gleam-token" content="` + s.token + `">`
}

// serveIndex 输出内嵌首页，并在 <head> 后插入口令标签。
func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	raw, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "index.html 缺失", http.StatusInternalServerError)
		return
	}
	page := string(raw)
	if meta := s.tokenMeta(r); meta != "" {
		page = strings.Replace(page, "<head>", "<head>\n  "+meta, 1)
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	// 首页里带着口令：不进任何缓存；带 ?token= 打开时不把地址经 Referer 带给外站。
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(page))
}

// tokenOrNew 预置口令优先，否则随机生成。
func tokenOrNew() string {
	if t := tokenFromEnv(); t != "" {
		return t
	}
	return newToken()
}
