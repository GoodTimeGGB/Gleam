package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"gleam/internal/webui"
)

func cmdWebUI(args []string) error {
	fs := flag.NewFlagSet("webui", flag.ContinueOnError)
	configPath := fs.String("config", "", "配置文件路径")
	workspace := fs.String("workspace", "", "工作区根目录")
	dataDir := fs.String("data-dir", "", "数据目录")
	mockLLM := fs.Bool("mock-llm", false, "使用 Mock 模型")
	mockScript := fs.String("mock-script", "", "Mock 响应脚本 JSON 路径")
	addr := fs.String("addr", "127.0.0.1:8787", "监听地址")
	flagArgs, _ := splitFlagArgs(args, fs)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	// Web UI 同桌面应用：没选过工作区就不绑定，交由「工作区」选择器决定。
	rt, err := buildRuntime(*configPath, *workspace, *dataDir, *mockLLM, *mockScript, nil, WithNoDefaultWorkspace())
	if err != nil {
		return err
	}
	defer rt.cleanup()
	bindSchedulerFire(rt)

	srv := webui.NewServer(rt.agent)
	// 监听地址交给台账：那一行要说"入网"，就必须知道自己开在哪个口上。
	srv.BindAddr = *addr
	announceToken(srv)
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	fmt.Fprintf(os.Stderr, "[gleam] Web UI 已启动: http://%s （Ctrl+C 停止）\n", *addr)
	// Ctrl+C 优雅退出
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		<-ch
		fmt.Fprintln(os.Stderr, "\n[gleam] 正在关闭…")
		_ = httpServer.Close()
	}()
	err = httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// announceToken 把本次启动的 API 口令写进数据目录（0600），并告诉启动者去哪儿拿。
//
// 口令本身不打印到本机日志：本机浏览器打开首页时会自动拿到，脚本读口令文件即可。
// 只有监听在非回环地址时才打印带口令的链接——那是局域网设备唯一拿到口令的途径，
// 也正是这个时候需要醒目的警告。
func announceToken(srv *webui.Server) {
	if p, err := srv.WriteTokenFile(); err != nil {
		fmt.Fprintf(os.Stderr, "[gleam] 警告：写 API 口令文件失败（%v）；脚本需改用 %s 预置口令\n", err, webui.TokenEnv)
	} else {
		fmt.Fprintf(os.Stderr, "[gleam] API 口令文件: %s（脚本请在请求头 %s 里带上它）\n", p, webui.TokenHeader)
	}
	if !srv.BindIsLoopback() {
		fmt.Fprintf(os.Stderr, "[gleam] 警告：监听在非回环地址 %s，局域网设备可以连上来。所有 /api 请求都要口令；\n"+
			"[gleam]   局域网里请用 http://<本机IP>:<端口>/?token=%s 打开。连接是明文 HTTP，口令会在网络上可见。\n",
			srv.BindAddr, srv.Token())
	}
}
