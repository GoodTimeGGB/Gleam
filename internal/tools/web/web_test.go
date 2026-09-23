package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// localTool 测试用工具：httptest 服务都跑在 127.0.0.1 上，
// 而生产默认拒绝本机/内网地址（防 SSRF），所以测试里显式放宽。
func localTool() *Tool {
	t := New()
	t.AllowPrivate = true
	return t
}

// TestWeb_BlocksPrivateByDefault SSRF 防护：默认必须拒绝本机与内网地址。
// web.fetch 是自动放行的只读工具，不设防就等于给提示词注入留了最省力的出口。
func TestWeb_BlocksPrivateByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("内网数据"))
	}))
	defer srv.Close()

	tool := New() // 默认 AllowPrivate=false
	if _, err := tool.Execute(context.Background(), map[string]any{"url": srv.URL}); err == nil {
		t.Fatal("默认应拒绝 127.0.0.1")
	} else if !strings.Contains(err.Error(), "拒绝访问本机或内网地址") {
		t.Fatalf("错误信息应说明是内网拦截，实际: %v", err)
	}

	for _, u := range []string{
		"http://localhost:8080/",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/",
		"http://192.168.1.1/",
	} {
		if _, err := tool.Execute(context.Background(), map[string]any{"url": u}); err == nil {
			t.Errorf("%s 应被拒绝", u)
		}
	}
}

// TestWeb_AllowsPrivateWhenOptedIn 显式放宽后本机地址可用（本地优先场景）。
func TestWeb_AllowsPrivateWhenOptedIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("本地内容"))
	}))
	defer srv.Close()

	tool := New()
	tool.AllowPrivate = true
	out, err := tool.Execute(context.Background(), map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatalf("放宽后应可访问: %v", err)
	}
	if !strings.Contains(out.(map[string]any)["text"].(string), "本地内容") {
		t.Error("应拿到本地内容")
	}
}

// TestWeb_OutcomeReportsHTTPFailure 4xx/5xx 是「跑完了但没做成」，不是成功。
func TestWeb_OutcomeReportsHTTPFailure(t *testing.T) {
	tool := localTool()
	if oc, note := tool.Outcome(nil, map[string]any{"status": 404}); oc != "failed" || note != "HTTP 404" {
		t.Errorf("404 应报 failed/HTTP 404，实际 %q/%q", oc, note)
	}
	if oc, _ := tool.Outcome(nil, map[string]any{"status": 200}); oc != "ok" {
		t.Errorf("200 应报 ok，实际 %q", oc)
	}
}

func TestWeb_FetchText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("纯文本内容 Gleam"))
	}))
	defer srv.Close()

	tool := localTool()
	out, err := tool.Execute(context.Background(), map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	m := out.(map[string]any)
	if !strings.Contains(m["text"].(string), "纯文本内容") {
		t.Errorf("text = %v", m["text"])
	}
	if m["status"] != http.StatusOK {
		t.Errorf("status = %v", m["status"])
	}
}

func TestWeb_ExtractsHTMLText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><style>.x{color:red}</style></head>
		<body><h1>标题一</h1><script>alert(1)</script><p>正文&amp;更多</p></body></html>`))
	}))
	defer srv.Close()

	tool := localTool()
	out, _ := tool.Execute(context.Background(), map[string]any{"url": srv.URL})
	m := out.(map[string]any)
	text := m["text"].(string)
	if !strings.Contains(text, "标题一") || !strings.Contains(text, "正文&更多") {
		t.Errorf("正文提取失败: %q", text)
	}
	if strings.Contains(text, "alert") || strings.Contains(text, "color:red") {
		t.Errorf("script/style 应被剔除: %q", text)
	}
}

func TestWeb_RejectsNonHTTP(t *testing.T) {
	tool := localTool()
	if _, err := tool.Execute(context.Background(), map[string]any{"url": "file:///etc/passwd"}); err == nil {
		t.Error("file:// 应被拒绝")
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"url": "ftp://x"}); err == nil {
		t.Error("ftp:// 应被拒绝")
	}
}

func TestWeb_HTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	tool := localTool()
	out, err := tool.Execute(context.Background(), map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatalf("非 2xx 不应返回 error（由调用方判断状态）: %v", err)
	}
	if out.(map[string]any)["status"] != http.StatusNotFound {
		t.Error("应透传状态码")
	}
}

func TestWeb_Permission(t *testing.T) {
	tool := localTool()
	if tool.Name() != "web.fetch" {
		t.Errorf("name = %s", tool.Name())
	}
	_ = tool.Permission() // readonly，无 panic 即可
}
