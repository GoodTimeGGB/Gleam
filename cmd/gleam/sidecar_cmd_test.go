package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"gleam/internal/agent"
	"gleam/internal/webui"
)

// startSidecar 在随机回环端口上跑 runSidecar，返回就绪行、stdin 写端与退出通道。
func startSidecar(t *testing.T, h http.Handler) (sidecarReady, *io.PipeWriter, <-chan error) {
	t.Helper()
	return startSidecarFor(t, func(string) http.Handler { return h })
}

// startSidecarFor 先开监听，再用实际地址构造 handler（真实 Web UI 的 Host 校验要知道 BindAddr）。
func startSidecarFor(t *testing.T, mk func(addr string) http.Handler) (sidecarReady, *io.PipeWriter, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := mk(ln.Addr().String())
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- runSidecar(h, ln, inR, outW) }()

	lineCh := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(outR).ReadString('\n')
		lineCh <- line
		_, _ = io.Copy(io.Discard, outR)
	}()
	var line string
	select {
	case line = <-lineCh:
	case <-time.After(5 * time.Second):
		t.Fatal("5 秒内没有就绪行")
	}
	if !strings.HasPrefix(line, sidecarReadyPrefix) {
		t.Fatalf("就绪行前缀不对: %q", line)
	}
	var ready sidecarReady
	if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), sidecarReadyPrefix)), &ready); err != nil {
		t.Fatalf("就绪行 JSON 解析失败: %v (%q)", err, line)
	}
	if ready.Addr != ln.Addr().String() || ready.PID <= 0 || ready.Version == "" {
		t.Fatalf("就绪行字段不对: %+v（监听 %s）", ready, ln.Addr())
	}
	return ready, inW, done
}

func waitExit(t *testing.T, done <-chan error, within time.Duration) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runSidecar 返回错误: %v", err)
		}
	case <-time.After(within):
		t.Fatalf("%v 内没有退出", within)
	}
}

// stdin 关闭（父进程退出/被杀）→ 优雅退出，并且挂着的 SSE 长连接不会拖住关闭。
func TestRunSidecar_ExitsWhenStdinCloses(t *testing.T) {
	streaming := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(streaming)
		<-r.Context().Done()
	})
	ready, stdin, done := startSidecar(t, h)

	resp, err := http.Get("http://" + ready.Addr + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	<-streaming

	start := time.Now()
	_ = stdin.Close()
	waitExit(t, done, 2*time.Second)
	if d := time.Since(start); d >= sidecarShutdownGrace {
		t.Fatalf("关闭花了 %v：SSE 连接拖住了 Shutdown", d)
	}
	if _, err := net.DialTimeout("tcp", ready.Addr, 500*time.Millisecond); err == nil {
		t.Fatal("退出后端口仍在监听")
	}
}

// {"cmd":"shutdown"} → 优雅退出；其它行被忽略。
func TestRunSidecar_ShutdownCommand(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	ready, stdin, done := startSidecar(t, h)
	defer stdin.Close()

	if _, err := io.WriteString(stdin, "not json\n{\"cmd\":\"ping\"}\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get("http://" + ready.Addr + "/")
	if err != nil {
		t.Fatalf("无关输入之后服务应仍在: %v", err)
	}
	resp.Body.Close()

	if _, err := io.WriteString(stdin, "{\"cmd\":\"shutdown\"}\n"); err != nil {
		t.Fatal(err)
	}
	waitExit(t, done, 2*time.Second)
}

// 用真实 Web UI handler：父进程预置的口令能用，不带口令的请求被拒。
func TestRunSidecar_RealHandlerUsesPresetToken(t *testing.T) {
	const tok = "sidecar-test-token-0123456789"
	t.Setenv(webui.TokenEnv, tok)
	rt, err := buildRuntime("", t.TempDir(), t.TempDir(), true, "", agent.NopNotifier{})
	if err != nil {
		t.Fatalf("装配 runtime 失败: %v", err)
	}
	defer rt.cleanup()
	ready, stdin, done := startSidecarFor(t, func(addr string) http.Handler {
		srv := webui.NewServer(rt.agent)
		srv.BindAddr = addr
		return srv.Handler()
	})
	defer func() { _ = stdin.Close(); waitExit(t, done, 3*time.Second) }()

	get := func(token string) int {
		req, _ := http.NewRequest("GET", "http://"+ready.Addr+"/api/info", nil)
		if token != "" {
			req.Header.Set(webui.TokenHeader, token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := get(tok); c != http.StatusOK {
		t.Fatalf("带预置口令应 200，得到 %d", c)
	}
	if c := get(""); c != http.StatusUnauthorized {
		t.Fatalf("不带口令应 401，得到 %d", c)
	}
}

func TestIsLoopbackListenAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:0":    true,
		"[::1]:0":        true,
		"localhost:8787": true,
		":0":             false,
		"0.0.0.0:0":      false,
		"192.168.1.2:80": false,
		"garbage":        false,
	} {
		if got := isLoopbackListenAddr(addr); got != want {
			t.Errorf("isLoopbackListenAddr(%q) = %v，期望 %v", addr, got, want)
		}
	}
}
