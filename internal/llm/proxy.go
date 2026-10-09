package llm

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// 出网方式。Gleam 访问模型服务只有这一条路，走不走代理由这里说了算：
//
//	system —— 跟随 HTTPS_PROXY / HTTP_PROXY / NO_PROXY（Go 默认行为，也是历史行为）
//	manual —— 用配置里那串地址，忽略环境变量
//	none   —— 直连，同样忽略环境变量（内网、或已有透明代理时用得上）
//
// 为什么在进程启动时定死、而不是每次请求现问：http.Transport 是要复用的（连接池、TLS 会话），
// 每个请求换一个 transport 等于每次都重新握手。所以改设置后重启生效，界面里也是这么写的。
var (
	proxyMu        sync.RWMutex
	proxyTransport http.RoundTripper = http.DefaultTransport
)

// SetProxyMode 按配置设定本进程的出网方式。rawURL 只在 manual 下用得上。
func SetProxyMode(mode, rawURL string) error {
	tr, err := buildTransport(mode, rawURL)
	if err != nil {
		return err
	}
	proxyMu.Lock()
	proxyTransport = tr
	proxyMu.Unlock()
	return nil
}

// Transport 返回当前该用的传输层。客户端在构造时取一次。
func Transport() http.RoundTripper {
	proxyMu.RLock()
	defer proxyMu.RUnlock()
	return proxyTransport
}

func buildTransport(mode, rawURL string) (http.RoundTripper, error) {
	switch strings.TrimSpace(mode) {
	case "", "system":
		return http.DefaultTransport, nil
	case "none":
		tr, err := cloneDefault()
		if err != nil {
			return nil, err
		}
		// 置 nil 而不是留空：不显式关掉，默认传输层的 ProxyFromEnvironment 还在，
		// 「不使用代理」就会变成一句空话
		tr.Proxy = nil
		return tr, nil
	case "manual":
		raw := strings.TrimSpace(rawURL)
		if raw == "" {
			return nil, fmt.Errorf("选了手动代理，但没填代理地址")
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("代理地址不合法：%s（形如 http://127.0.0.1:7890）", raw)
		}
		switch u.Scheme {
		case "http", "https", "socks5":
		default:
			return nil, fmt.Errorf("只支持 http / https / socks5 代理，收到 %q", u.Scheme)
		}
		tr, err := cloneDefault()
		if err != nil {
			return nil, err
		}
		tr.Proxy = http.ProxyURL(u)
		return tr, nil
	default:
		return nil, fmt.Errorf("未知的代理方式：%s", mode)
	}
}

// cloneDefault 克隆默认传输层再改代理：直接 new 一个会丢掉拨号超时、TLS 配置与 HTTP/2。
func cloneDefault() (*http.Transport, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("默认传输层不是 *http.Transport，代理设置无从附着")
	}
	return base.Clone(), nil
}
