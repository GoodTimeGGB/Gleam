package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------- 数据出网留痕（P1-3） ----------

// TestWeb_RecordsEgress 抓取前必须留痕：web.fetch 是自动放行的只读工具，
// 它把请求发到哪个站点，此前没有任何记录——"本地优先"就成了一句无法自证的话。
func TestWeb_RecordsEgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>页面正文</body></html>"))
	}))
	defer srv.Close()

	tool := localTool()
	var host string
	var nbytes int
	tool.OnEgress = func(h string, n int) { host, nbytes = h, n }

	if _, err := tool.Execute(context.Background(), map[string]any{"url": srv.URL}); err != nil {
		t.Fatalf("抓取应成功: %v", err)
	}
	if host == "" {
		t.Fatal("抓取发生了却没有出网留痕")
	}
	if !strings.Contains(host, "127.0.0.1") {
		t.Errorf("留痕应记下目标主机，实际 %q", host)
	}
	if nbytes == 0 {
		t.Error("留痕应记下请求字节数")
	}
	// 留痕里不能带正文——回调签名就只给了主机名与字节数，
	// 这条断言是防止以后有人"顺手"把 body 也传进来。
	if strings.Contains(host, "页面正文") {
		t.Errorf("留痕不该包含内容：%q", host)
	}
}

// TestWeb_BlockedRequestDoesNotRecordEgress 被 SSRF 拦截的请求根本没出网，
// 不能记成"数据出网"——那是 denied，不是 egress。
// 两种留痕混在一起，审计就会回答错误的问题（"发给了谁"变成"想发给谁"）。
func TestWeb_BlockedRequestDoesNotRecordEgress(t *testing.T) {
	tool := New() // 默认拒绝本机/内网
	called := false
	tool.OnEgress = func(string, int) { called = true }

	if _, err := tool.Execute(context.Background(), map[string]any{"url": "http://169.254.169.254/latest/meta-data/"}); err == nil {
		t.Fatal("默认应拦下云元数据地址")
	}
	if called {
		t.Error("被拦下的请求没有出网，不该记出网留痕")
	}
}

// TestWeb_NoHookNoPanic 没装回调时照常工作（审计是旁路，不能成为抓取的前置条件）。
func TestWeb_NoHookNoPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>ok</body></html>"))
	}))
	defer srv.Close()

	if _, err := localTool().Execute(context.Background(), map[string]any{"url": srv.URL}); err != nil {
		t.Fatalf("没装出网回调时也应正常抓取: %v", err)
	}
}
