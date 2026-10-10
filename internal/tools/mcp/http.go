package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gleam/internal/buildinfo"
)

// 远端 MCP（streamable-http）传输。
//
// **这是 Gleam 里第二条"连出去"的路**（第一条是模型服务）。所以三件事必须一起做，
// 少一件就是"连了而没交代"：
//  1. 出网留痕：每次请求回调 OnRemoteEgress(host, bytes)，由接入层记进安全门控的
//     台账（`/api/connections` 上那一行读数就是它）；
//  2. 目标可见：URL 与主机在 MCP 列表页如实列出，用户能看见自己连到了哪；
//  3. 凭据只出在请求头里：拿到的 header 值（API Key 之类）只进 `net/http` 的请求头，
//     不写日志、不进错误文案。
//
// 协议要点（MCP Streamable HTTP）：
//   - 每次调用都是一个 POST，`Accept` 同时接受 json 与 text/event-stream；
//   - 响应可能是**单个 JSON**，也可能是一条 **SSE 流**（服务器选了 SSE 那条）；
//   - 服务器可以在 initialize 的响应头里给一个 `Mcp-Session-Id`，后续请求要带上；
//   - 通知（notifications/*）只 POST、不看响应体（服务器回 202）。

const remoteTimeout = 30 * time.Second

// OnRemoteEgress 远端 MCP 的出网回调。由接入层设置（见 cmd/gleam/main.go），
// 用来把"连出去多少字节"记进门控台账。为 nil 时静默——但那样台账上就没读数了，
// 所以生产装配一定会设它。
var OnRemoteEgress func(host string, nbytes int)

// SetRemoteEgressHook 装配出网回调。只有一个出口，避免两处各自设置互相覆盖。
func SetRemoteEgressHook(fn func(host string, nbytes int)) { OnRemoteEgress = fn }

type httpTransport struct {
	name    string
	url     string
	headers map[string]string
	client  *http.Client
	logf    func(string, ...any)

	mu        sync.Mutex
	nextID    int64
	sessionID string
	closed    bool
}

func newHTTPTransport(cfg ServerConfig, logf func(string, ...any)) (*httpTransport, error) {
	u, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("mcp: 远端地址不可用 %q", cfg.URL)
	}
	if u.Scheme != "https" && !isLoopbackHost(u.Hostname()) {
		// 明文 http 会把凭据送在网上。本地回环例外（本机 dev 服务器）。
		return nil, fmt.Errorf("mcp: 远端地址必须是 https（拿到的是 %q）", u.Scheme)
	}
	return &httpTransport{
		name: cfg.Name, url: u.String(), headers: cfg.Headers,
		client: &http.Client{Timeout: remoteTimeout}, logf: logf,
	}, nil
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasPrefix(h, "127.")
}

func (t *httpTransport) notify(method string, params any) error {
	_, err := t.roundTrip(context.Background(), rpcMessage{
		JSONRPC: "2.0", Method: method, Params: paramsRaw(params),
	}, false)
	return err
}

func (t *httpTransport) call(ctx context.Context, method string, params any, out any) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return fmt.Errorf("mcp: %s 已关闭", t.name)
	}
	t.nextID++
	id := t.nextID
	t.mu.Unlock()

	idRaw, _ := json.Marshal(id)
	msg, err := t.roundTrip(ctx, rpcMessage{
		JSONRPC: "2.0", ID: idRaw, Method: method, Params: paramsRaw(params),
	}, true)
	if err != nil {
		return err
	}
	return decodeResult(t.name, method, msg, out)
}

// roundTrip 发一次 POST 并取回对应的 JSON-RPC 消息。
// wantResult=false 时只发不看（通知），服务器通常回 202。
func (t *httpTransport) roundTrip(ctx context.Context, req rpcMessage, wantResult bool) (rpcMessage, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return rpcMessage{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	hreq, err := http.NewRequestWithContext(cctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return rpcMessage{}, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	// 规范要求同时接受两种：服务器自己选走 JSON 还是 SSE
	hreq.Header.Set("Accept", "application/json, text/event-stream")
	hreq.Header.Set("User-Agent", "gleam/"+buildinfo.Version)
	for k, v := range t.headers {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			hreq.Header.Set(k, v)
		}
	}
	t.mu.Lock()
	sid := t.sessionID
	t.mu.Unlock()
	if sid != "" {
		hreq.Header.Set("Mcp-Session-Id", sid)
	}

	resp, err := t.client.Do(hreq)
	if err != nil {
		return rpcMessage{}, fmt.Errorf("mcp: %s 连不上 %s：%w", t.name, hostOfURL(t.url), err)
	}
	defer resp.Body.Close()
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		t.mu.Lock()
		t.sessionID = s
		t.mu.Unlock()
	}

	// 请求**已经发出去了**，从这里往下无论成功、4xx 还是读失败都要留痕。
	// 只在成功路径记的话，401（凭据不对）这种最常见的失败就一条痕迹都没有——
	// 而那一刻请求头里的凭据已经发到对方主机了。留痕漏在失败路径上，等于
	// "出错时反而看不见自己连过哪"，正是这一层最不该出现的方向。
	rb := &countingReader{r: io.LimitReader(resp.Body, 8<<20)}
	// 闭包不可省：`defer t.noteEgress(rb.n)` 会在 defer 那一刻就求值（=0），
	// 于是每次只记 0 字节，而 0 被当成"没有出网"跳过。
	defer func() { t.noteEgress(rb.n) }()

	if !wantResult && resp.StatusCode < 400 {
		_, _ = io.Copy(io.Discard, rb)
		return rpcMessage{}, nil
	}
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(rb, 4096))
		return rpcMessage{}, fmt.Errorf("mcp: %s %s 返回 %d：%s",
			t.name, hostOfURL(t.url), resp.StatusCode, clipSecret(string(msg)))
	}

	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		return t.readSSE(rb)
	}
	var msg rpcMessage
	if err := json.NewDecoder(rb).Decode(&msg); err != nil {
		return rpcMessage{}, fmt.Errorf("mcp: %s 的响应读不懂：%w", t.name, err)
	}
	return msg, nil
}

// countingReader 数读过的字节数。用指针接收者，好让 defer 读到最终值。
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// readSSE 从事件流里取出第一条带结果的 JSON-RPC 消息。
//
// 只取第一条就够：MCP 的请求-响应是一问一答，我们也没有订阅服务器主动推送
// （那需要一条常驻 GET 流，是另一件事）。
func (t *httpTransport) readSSE(r io.Reader) (rpcMessage, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	var data []string
	for {
		line, err := br.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if len(data) > 0 {
				var msg rpcMessage
				if jerr := json.Unmarshal([]byte(strings.Join(data, "\n")), &msg); jerr == nil && (len(msg.ID) > 0 || msg.Error != nil) {
					return msg, nil
				}
				data = nil
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		if err != nil {
			if len(data) > 0 {
				var msg rpcMessage
				if jerr := json.Unmarshal([]byte(strings.Join(data, "\n")), &msg); jerr == nil && (len(msg.ID) > 0 || msg.Error != nil) {
					return msg, nil
				}
			}
			if err == io.EOF {
				return rpcMessage{}, fmt.Errorf("mcp: %s 的事件流里没有带结果的响应", t.name)
			}
			return rpcMessage{}, err
		}
	}
}

func (t *httpTransport) close() error {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	return nil
}

// noteEgress 记一次出网。主机从 URL 取，不带路径与查询串——
// 查询串里可能有凭据，留痕只该记"连到了哪、多少字节"。
func (t *httpTransport) noteEgress(n int64) {
	if OnRemoteEgress == nil || n <= 0 {
		return
	}
	OnRemoteEgress(hostOfURL(t.url), int(n))
}

func hostOfURL(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

// clipSecret 把可能出现在错误正文里的凭据挖掉：远端 4xx 常把请求头回显在正文里。
func clipSecret(s string) string {
	s = strings.TrimSpace(s)
	for _, key := range []string{"Authorization", "authorization", "api_key", "apikey", "token", "Bearer "} {
		if i := strings.Index(s, key); i >= 0 {
			end := i + len(key)
			// 只留键名，后面的值一律截掉
			s = s[:end] + " ***"
			break
		}
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
