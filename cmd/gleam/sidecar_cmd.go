package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gleam/internal/webui"
)

// sidecarReadyPrefix 是 desktop-sidecar 在 stdout 上打印的就绪行前缀。
// 桌面壳（Electron 主进程）读到这一行才去探活并加载界面；stdout 上只会有这一行协议输出，
// 其余日志一律走 stderr。
const sidecarReadyPrefix = "GLEAM_READY "

// sidecarShutdownGrace 收到关闭指令后留给在途请求的时间；SSE 长连接会先被主动断开。
const sidecarShutdownGrace = 3 * time.Second

// sidecarReady 就绪行的 JSON 载荷。
type sidecarReady struct {
	Addr    string `json:"addr"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

// cmdDesktopSidecar 是给桌面壳用的隐藏子命令（不出现在 help 里）。
//
// 与 webui 的区别只在生命周期：
//   - 默认监听 127.0.0.1:0（随机端口），实际地址通过 stdout 就绪行告诉父进程；
//   - 口令由父进程经 GLEAM_WEBUI_TOKEN 预置（NewServer 已支持），不在任何地方打印；
//   - stdin 是生命线：父进程关掉 stdin（包括父进程被强杀、管道被内核关闭）即优雅退出，
//     收到一行 {"cmd":"shutdown"} 也优雅退出。这样桌面壳崩了也不会留下孤儿进程。
func cmdDesktopSidecar(args []string) error {
	fs := flag.NewFlagSet("desktop-sidecar", flag.ContinueOnError)
	configPath := fs.String("config", "", "配置文件路径")
	workspace := fs.String("workspace", "", "工作区根目录")
	dataDir := fs.String("data-dir", "", "数据目录")
	mockLLM := fs.Bool("mock-llm", false, "使用 Mock 模型")
	mockScript := fs.String("mock-script", "", "Mock 响应脚本 JSON 路径")
	addr := fs.String("addr", "127.0.0.1:0", "监听地址（必须是回环地址）")
	flagArgs, _ := splitFlagArgs(args, fs)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if !isLoopbackListenAddr(*addr) {
		return fmt.Errorf("desktop-sidecar 只允许监听回环地址，收到 %q", *addr)
	}
	if strings.TrimSpace(os.Getenv(webui.TokenEnv)) == "" {
		fmt.Fprintf(os.Stderr, "[gleam] 警告：未设置 %s，desktop-sidecar 将使用随机口令（桌面壳拿不到它）\n", webui.TokenEnv)
	}

	// 桌面应用：没选过工作区就停在「不指定工作区」，不用进程 CWD 兜底。
	rt, err := buildRuntime(*configPath, *workspace, *dataDir, *mockLLM, *mockScript, nil, WithNoDefaultWorkspace(), WithAsyncMCP())
	if err != nil {
		return err
	}
	defer rt.cleanup()
	bindSchedulerFire(rt)

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	srv := webui.NewServer(rt.agent)
	// 口令已被 NewServer 读走；从环境里抹掉，免得被工具 / MCP 子进程继承下去。
	_ = os.Unsetenv(webui.TokenEnv)
	srv.BindAddr = ln.Addr().String()
	announceToken(srv)
	// MCP 连接放到就绪之后：慢的 MCP（npx 拉 Node 实测 ~2.2 s）不该挡住「已经能用」。
	go rt.startMCP()
	return runSidecar(srv.Handler(), ln, os.Stdin, os.Stdout)
}

// isLoopbackListenAddr 判断监听地址是否落在回环上（host 为空即 0.0.0.0，不算）。
func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// runSidecar 在 ln 上提供 h，打印就绪行，然后一直跑到 stdin 关闭或收到 shutdown 指令。
// 拆出来是为了测试：真实入口传 os.Stdin / os.Stdout。
func runSidecar(h http.Handler, ln net.Listener, stdin io.Reader, stdout io.Writer) error {
	// 所有请求的 context 都派生自 baseCtx：关闭时先 cancel 它，SSE 之类的长连接立刻收尾，
	// Shutdown 就不用干等它们超时。
	baseCtx, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	httpServer := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(ln) }()

	ready, _ := json.Marshal(sidecarReady{Addr: ln.Addr().String(), PID: os.Getpid(), Version: version})
	if _, err := fmt.Fprintf(stdout, "%s%s\n", sidecarReadyPrefix, ready); err != nil {
		// 父进程连 stdout 都不收了，没有继续跑的意义。
		cancelBase()
		_ = httpServer.Close()
		return fmt.Errorf("写就绪行失败: %w", err)
	}

	stop := make(chan string, 1)
	var once sync.Once
	signalStop := func(why string) { once.Do(func() { stop <- why }) }
	go func() {
		sc := bufio.NewScanner(stdin)
		for sc.Scan() {
			var msg struct {
				Cmd string `json:"cmd"`
			}
			if json.Unmarshal(sc.Bytes(), &msg) == nil && msg.Cmd == "shutdown" {
				signalStop("shutdown 指令")
				return
			}
		}
		signalStop("stdin 已关闭")
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case why := <-stop:
		fmt.Fprintf(os.Stderr, "[gleam] desktop-sidecar 收到退出信号（%s），正在关闭…\n", why)
	}
	cancelBase()
	ctx, cancel := context.WithTimeout(context.Background(), sidecarShutdownGrace)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		_ = httpServer.Close()
	}
	return nil
}
