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

	rt, err := buildRuntime(*configPath, *workspace, *dataDir, *mockLLM, *mockScript, nil)
	if err != nil {
		return err
	}
	defer rt.cleanup()
	bindSchedulerFire(rt)

	srv := webui.NewServer(rt.agent)
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
