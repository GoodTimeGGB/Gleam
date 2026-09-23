// Package web 实现网页抓取工具。
package web

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gleam/internal/tools/toolutil"
	"gleam/pkg/types"
)

const (
	maxBodyBytes = 2 * 1024 * 1024 // 下载上限 2MB
	maxTextLen   = 20 * 1024       // 返回文本上限
)

// Tool web.fetch 工具。
type Tool struct {
	HTTP *http.Client

	// AllowPrivate 允许访问本机与内网地址。默认 false。
	//
	// 为什么默认拒绝：web.fetch 是**自动放行**的只读工具（PermissionReadOnly），
	// 是提示词注入最省力的出口——一个被注入的网页就能让 Agent 去读内网服务或
	// 云元数据接口（169.254.169.254），再把结果带回提示词里。
	// 但 Gleam 是本地优先的桌面 Agent，"读本机 dev server / 内网 wiki"是正常需求，
	// 所以做成**安全默认 + 显式放宽**（配置项 safety.allow_private_web），而不是一刀切。
	AllowPrivate bool

	// OnEgress 可选：每次真正发包前回调一次（目标主机 + 请求字节数）。
	//
	// 为什么单独给它一个口子：web.fetch 是**自动放行**的只读工具，
	// 它把请求发到哪个站点此前没有任何留痕。审计要能回答"什么数据出了本机"，
	// 这条路径不能漏。回调只拿到主机名与字节数——**拿不到正文**，
	// 所以不可能把抓到的内容误写进审计（见 safety.AuditEntry.Egress）。
	OnEgress func(host string, nbytes int)
}

func New() *Tool {
	return &Tool{HTTP: &http.Client{Timeout: 20 * time.Second}}
}

func (t *Tool) Name() string        { return "web.fetch" }
func (t *Tool) Description() string { return "抓取网页内容并提取正文文本（只读）" }
func (t *Tool) Permission() types.Permission {
	return types.PermissionReadOnly
}
func (t *Tool) Schema() map[string]any {
	return toolutil.Schema("抓取网页", []string{"url"}, map[string]any{
		"url": toolutil.SchemaProp("网页地址（http/https）", "string"),
	})
}
func (t *Tool) Execute(ctx context.Context, args map[string]any) (any, error) {
	u, err := toolutil.RequireStr(args, "url")
	if err != nil {
		return nil, err
	}
	if err := t.guardURL(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Gleam/0.1 (local-first desktop agent)")

	// 出网留痕：放在校验之后、真正发包之前——被 guardURL 拦下的请求根本没出网，
	// 不该记成"数据出网"（那是 denied，不是 egress，两种留痕混在一起会让审计失真）。
	if t.OnEgress != nil {
		host := u
		if parsed, perr := url.Parse(u); perr == nil && parsed.Host != "" {
			host = parsed.Host
		}
		t.OnEgress(host, len(u))
	}

	// 重定向必须逐个校验：只挡入口不挡跳转等于没挡——
	// 一个公网地址 302 到 169.254.169.254 就能把云元数据读出来。
	client := *t.HTTP
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("重定向次数过多")
		}
		return t.guardURL(r.URL.String())
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	contentType := resp.Header.Get("Content-Type")
	if err != nil && len(body) == 0 {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	text := string(body)
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml") {
		text = extractText(text)
	}
	truncated := len(text) > maxTextLen
	if truncated {
		text = text[:maxTextLen]
	}
	return map[string]any{
		"url":          u,
		"status":       resp.StatusCode,
		"content_type": contentType,
		"bytes":        len(body),
		"text":         text,
		"truncated":    truncated,
	}, nil
}

var (
	scriptRe = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	tagRe    = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRe  = regexp.MustCompile(`[ \t]+`)
	linesRe  = regexp.MustCompile(`\n{3,}`)
)

// Outcome 实现 types.OutcomeReporter：HTTP 4xx/5xx 是「跑完了但没做成」，
// 不是成功。以前它只体现在 payload 的 status 字段里，执行器看不见。
func (t *Tool) Outcome(args map[string]any, out any) (types.StepOutcome, string) {
	m, ok := out.(map[string]any)
	if !ok {
		return types.OutcomeOK, ""
	}
	if code := toolutil.IntOf(m["status"]); code >= 400 {
		return types.OutcomeFailed, fmt.Sprintf("HTTP %d", code)
	}
	return types.OutcomeOK, ""
}

// ---------- SSRF 防护 ----------

// guardURL 拒绝指向本机 / 内网 / 链路本地 / 云元数据地址的请求。
//
// web.fetch 的 URL 来自模型，而模型可能被它读到的网页内容影响（提示词注入）。
// 只校验 scheme 等于把 SSRF 大门敞开：一个被注入的页面就能让 Agent 去读内网服务
// 或云元数据接口（169.254.169.254），再把结果带回提示词里。
//
// 已知边界：这是「解析后校验」，理论上仍存在 DNS rebinding（校验时解析到公网 IP、
// 真正连接时解析到内网）。彻底堵住需要自己接管 Dial、校验实际连接的地址；
// 对本地优先的桌面 Agent 来说，挡住「顺手读内网」这一档已覆盖绝大多数风险。
func (t *Tool) guardURL(raw string) error {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return fmt.Errorf("仅支持 http/https 地址，收到 %q", raw)
	}
	if t.AllowPrivate {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("地址解析失败: %w", err)
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("地址缺少主机名: %q", raw)
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("域名解析失败（%s）: %w", host, err)
	}
	for _, ip := range ips {
		if blockedIP(ip) {
			return fmt.Errorf("拒绝访问本机或内网地址：%s 解析为 %s（如确需访问，请打开 safety.allow_private_web）", host, ip)
		}
	}
	return nil
}

// blockedIP 判断 IP 是否属于禁止访问的范围。
func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || // 127.0.0.0/8、::1
		ip.IsPrivate() || // 10/8、172.16/12、192.168/16、fc00::/7
		ip.IsLinkLocalUnicast() || // 169.254/16（含云元数据 169.254.169.254）、fe80::/10
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() // 0.0.0.0、::
}

// extractText 粗提取 HTML 正文文本。
func extractText(html string) string {
	html = scriptRe.ReplaceAllString(html, " ")
	html = tagRe.ReplaceAllString(html, "\n")
	html = strings.ReplaceAll(html, "&nbsp;", " ")
	html = strings.ReplaceAll(html, "&amp;", "&")
	html = strings.ReplaceAll(html, "&lt;", "<")
	html = strings.ReplaceAll(html, "&gt;", ">")
	html = strings.ReplaceAll(html, "&quot;", "\"")
	html = spaceRe.ReplaceAllString(html, " ")
	html = linesRe.ReplaceAllString(html, "\n\n")
	return strings.TrimSpace(html)
}
