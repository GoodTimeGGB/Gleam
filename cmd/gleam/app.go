package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	goruntime "runtime"
	"strings"
	"time"

	"gleam/internal/desktop"
	"gleam/internal/systray"
	"gleam/internal/webui"
)

// cmdApp 桌面端应用模式：启动引擎 + Web UI + 系统托盘。
//
// 交互模型：
//   - 双击 GleamDesktop.exe 启动后进程常驻后台，托盘显示图标。
//   - 托盘左键/双击 → 打开/前置应用窗口；右键 → 打开/退出。
//   - 关闭窗口不退出进程（主服务、调度器、记忆都继续运行）。
//   - 托盘菜单「退出」才真正结束进程。
//   - 再次双击 GleamDesktop.exe：若已有实例，则请求现有实例弹窗并立即退出（单实例）。
//
// 窗口实现说明：
//
//	使用 Edge/Chrome 的 --app 模式启动独立浏览器窗口（独立 msedge.exe 进程），
//	UI 渲染与主进程完全隔离，不会因为 UI 线程阻塞导致主进程"未响应"。
//	这是 Slack/Discord/VS Code 等产品长期验证的稳定方案。
func cmdApp(args []string) error {
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	configPath := fs.String("config", "", "配置文件路径")
	workspace := fs.String("workspace", "", "工作区根目录")
	dataDir := fs.String("data-dir", "", "数据目录")
	mockLLM := fs.Bool("mock-llm", false, "使用 Mock 模型")
	mockScript := fs.String("mock-script", "", "Mock 响应脚本 JSON 路径")
	addr := fs.String("addr", "127.0.0.1:8787", "监听地址")
	noBrowser := fs.Bool("no-browser", false, "启动时不自动打开窗口（仅托盘）")
	flagArgs, _ := splitFlagArgs(args, fs)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	// 单实例：默认端口已被 Gleam 占用时请求现有实例弹窗后退出
	listenAddr := *addr
	if busy, gleam := probeAddr(*addr); busy {
		if gleam {
			_, _ = http.Post("http://"+*addr+"/api/show-window", "", nil)
			fmt.Fprintf(os.Stderr, "[gleam] 已有实例运行于 http://%s，已请求其弹出窗口\n", *addr)
			return nil
		}
		listenAddr = "127.0.0.1:0"
	}

	rt, err := buildRuntime(*configPath, *workspace, *dataDir, *mockLLM, *mockScript, nil)
	if err != nil {
		return err
	}
	defer rt.cleanup()
	bindSchedulerFire(rt)

	srv := webui.NewServer(rt.agent)
	inner := srv.Handler()
	lastSeen := time.Now()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastSeen = time.Now()
		inner.ServeHTTP(w, r)
	})
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	// 台账要说**实测**的监听地址：默认端口被占用时这里会退到随机端口（127.0.0.1:0），
	// 把 --addr 那个字符串报上去就是说谎。
	srv.BindAddr = ln.Addr().String()
	url := "http://" + ln.Addr().String() + "/"
	fmt.Fprintf(os.Stderr, "[gleam] 桌面端已启动: %s（托盘常驻，关闭窗口不退出）\n", url)

	go func() {
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "[gleam] HTTP 错误:", err)
		}
	}()

	openWindow := func() { desktop.OpenWindow(url, nil) }
	srv.ShowWindowFunc = openWindow

	// Ctrl+C 优雅退出（命令行启动时）
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		<-ch
		desktop.CloseWindow()
		_ = httpServer.Close()
		systray.Quit()
	}()

	// 首启自动打开窗口
	if !*noBrowser {
		openWindow()
	}

	// 托盘状态轮询：tooltip 实时显示任务/待审批数
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for range t.C {
			running := rt.agent.RunningTasks()
			pending := srv.PendingApprovals()
			statusLine := "引擎空闲"
			if running > 0 {
				statusLine = fmt.Sprintf("运行中：%d 个任务", running)
			}
			if pending > 0 {
				statusLine += fmt.Sprintf(" · %d 项待批准", pending)
			}
			systray.SetTooltip("Gleam · 微光 — " + statusLine)
		}
	}()

	// 托盘主循环（阻塞直到用户点「退出」）
	err = systray.Run(systray.Config{
		Tooltip:    "Gleam · 微光（本地智能体）",
		OnActivate: openWindow,
		MenuItems: []systray.MenuItem{
			{Title: "打开 Gleam", OnClick: openWindow},
			{Title: "", Disabled: true, SepAfter: true},
			{Title: "退出", OnClick: func() {
				desktop.CloseWindow()
				_ = httpServer.Close()
				systray.Quit()
			}},
		},
	})
	if errors.Is(err, systray.ErrNotImplemented) {
		// 非 Windows 平台：回退到打开默认浏览器后阻塞
		if !*noBrowser {
			openBrowser(url)
		}
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		<-ch
		_ = httpServer.Close()
		return nil
	}
	_ = lastSeen
	return err
}

// openBrowser 非 Windows 平台回退：用系统默认浏览器打开。
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	}
	_ = cmd.Start()
}

// probeAddr 探测地址占用情况；第二个返回值表示是否为本机已有的 Gleam。
func probeAddr(addr string) (busy, isGleam bool) {
	conn, err := net.DialTimeout("tcp", addr, 800*time.Millisecond)
	if err != nil {
		return false, false
	}
	_ = conn.Close()
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get("http://" + addr + "/api/info")
	if err != nil {
		return true, false
	}
	defer resp.Body.Close()
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	return true, strings.Contains(string(buf[:n]), `"name":"gleam"`)
}

// openAppWindow 用 Edge/Chrome 的应用模式打开独立窗口（无地址栏与标签页，观感为桌面应用）；
// 找不到时按平台回退系统默认浏览器。
func openAppWindow(url string) {
	cmd := launchAppCmd(url)
	if cmd == nil {
		return
	}
	go func() { _ = cmd.Wait() }()
}

// launchAppCmd 启动应用模式浏览器进程。
func launchAppCmd(url string) *exec.Cmd {
	for _, p := range browserPathsFor(goruntime.GOOS) {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		cmd := exec.Command(p,
			"--app="+url,
			"--window-size=1280,840",
			"--no-first-run",
			"--no-default-browser-check",
			"--disable-features=msWebOOUI,msPdfOOUI,msSmartScreenProtection",
		)
		cmd.SysProcAttr = procAttr()
		if err := cmd.Start(); err == nil {
			return cmd
		}
	}
	for _, parts := range browserFallbackCmds(goruntime.GOOS, url) {
		cmd := exec.Command(parts[0], parts[1:]...)
		cmd.SysProcAttr = procAttr()
		if err := cmd.Start(); err == nil {
			return cmd
		}
	}
	return nil
}

// browserFallbackCmds 按平台返回默认浏览器回退命令序列（依序尝试，首个成功即用）。
// 纯函数：便于对三个平台的回退行为做确定性单元测试。
func browserFallbackCmds(goos, url string) [][]string {
	switch goos {
	case "darwin":
		// macOS：open -na 以独立实例启动 Chrome/Edge 应用模式；都没有则开默认浏览器
		var cmds [][]string
		for _, app := range []string{"Google Chrome", "Microsoft Edge", "Chromium"} {
			cmds = append(cmds, []string{"open", "-na", app, "--args", "--app=" + url, "--window-size=1440,900"})
		}
		return append(cmds, []string{"open", url})
	case "windows":
		return [][]string{{"rundll32", "url.dll,FileProtocolHandler", url}}
	case "linux":
		return [][]string{{"xdg-open", url}}
	default:
		return [][]string{{"open", url}}
	}
}

// browserPathsFor 按平台返回 Edge/Chrome 可执行文件候选路径（纯函数，测试友好）。
func browserPathsFor(goos string) []string {
	switch goos {
	case "darwin":
		paths := []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
		if home := os.Getenv("HOME"); home != "" {
			paths = append(paths,
				home+"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
				home+"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge")
		}
		return paths
	case "linux":
		return []string{
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/microsoft-edge",
			"/usr/bin/microsoft-edge-stable",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
		}
	default: // windows
		progFiles := os.Getenv("ProgramFiles")
		progFilesX86 := os.Getenv("ProgramFiles(x86)")
		localAppData := os.Getenv("LocalAppData")
		var paths []string
		for _, base := range []string{progFilesX86, progFiles} {
			if base != "" {
				paths = append(paths, base+`\Microsoft\Edge\Application\msedge.exe`)
			}
		}
		for _, base := range []string{localAppData, progFiles, progFilesX86} {
			if base != "" {
				paths = append(paths, base+`\Google\Chrome\Application\chrome.exe`)
			}
		}
		return paths
	}
}
