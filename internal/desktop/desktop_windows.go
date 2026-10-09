//go:build windows

package desktop

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// 窗口管理：Edge/Chrome --app 窗口独立于启动器进程，单靠记录 cmd.Process
// 既无法「复用已有窗口」，也无法在退出时可靠关窗（启动器进程常立即退出，
// 真正持有窗口的是独立浏览器进程）。因此统一用 Win32 按窗口标题枚举
// app 窗口：左键 → 已存在则前置激活、否则新开；退出 → 发 WM_CLOSE 优雅关窗。

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")

	pEnumWindows              = user32.NewProc("EnumWindows")
	pGetWindowTextW           = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	pIsWindowVisible          = user32.NewProc("IsWindowVisible")
	pGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	pGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	pAttachThreadInput        = user32.NewProc("AttachThreadInput")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pShowWindow               = user32.NewProc("ShowWindow")
	pBringWindowToTop         = user32.NewProc("BringWindowToTop")
	pPostMessageW             = user32.NewProc("PostMessageW")
	pGetCurrentThreadId       = kernel32.NewProc("GetCurrentThreadId")
	pDwmSetWindowAttribute    = dwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	swRestore = 9
	swShow    = 5
	wmClose   = 0x0010

	// DWM 窗口属性（Windows 11 支持，旧系统调用失败会被静默忽略）。
	dwmwaUseImmersiveDarkMode = 33
	dwmwaBorderColor          = 34
	dwmwaCaptionColor         = 35
	dwmwaTextColor            = 36
)

// windowTitle 是 app 窗口标题，与 index.html 的 <title> 及 --app-name 保持一致。
// 用「微光」这个不易与文件夹（如 D:\PersonalProject\Gleam 资源管理器）撞名的词匹配。
const windowTitle = "Gleam · 微光"

var (
	mu        sync.Mutex
	procs     []*exec.Cmd
	launching bool
)

// browserPaths 返回优先使用的 Edge/Chrome 可执行文件路径。
func browserPaths() []string {
	env := os.Getenv("GLEAM_BROWSER")
	var paths []string
	if env != "" {
		paths = append(paths, env)
	}
	local := os.Getenv("LOCALAPPDATA")
	programs64 := os.Getenv("ProgramFiles")
	programsX86 := os.Getenv("ProgramFiles(x86)")
	paths = append(paths,
		filepath.Join(local, "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(local, "Chromium", "Application", "chrome.exe"),
		filepath.Join(local, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
		filepath.Join(programs64, "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(programs64, "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(programsX86, "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(programsX86, "Google", "Chrome", "Application", "chrome.exe"),
	)
	return paths
}

func profileDir() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".gleam", "browser-profile")
}

func openWindowPlatform(target string, onClose func()) bool {
	// 已有 app 窗口：前置激活，不再新开
	if hwnds := findGleamWindows(); len(hwnds) > 0 {
		for _, h := range hwnds {
			activateWindow(h)
		}
		return true
	}
	// 浏览器启动到窗口标题出现有数秒窗口，期间连点左键应被合并，避免在同一
	// 浏览器实例里重复弹出多个 app 窗口。
	mu.Lock()
	if launching {
		mu.Unlock()
		return true
	}
	launching = true
	mu.Unlock()
	clearLaunching := func() {
		mu.Lock()
		launching = false
		mu.Unlock()
	}

	exe := ""
	for _, p := range browserPaths() {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			exe = p
			break
		}
	}
	if exe == "" {
		clearLaunching()
		return false
	}
	u, _ := url.Parse(target)
	appName := windowTitle
	args := []string{
		"--app=" + target,
		"--window-size=1280,840",
		"--app-name=" + appName,
		"--window-name=" + appName,
		"--user-data-dir=" + profileDir(),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-features=msWebOOUI,msPdfOOUI,msSmartScreenProtection,Translate",
		"--disable-session-crashed-bubble",
		"--disable-infobars",
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		clearLaunching()
		return false
	}
	mu.Lock()
	procs = append(procs, cmd)
	pid := cmd.Process.Pid
	mu.Unlock()

	go func() {
		_ = cmd.Wait()
		mu.Lock()
		for i, p := range procs {
			if p.Process != nil && p.Process.Pid == pid {
				procs = append(procs[:i], procs[i+1:]...)
				break
			}
		}
		launching = false
		mu.Unlock()
		if onClose != nil {
			go onClose()
		}
	}()
	// 等待窗口真正出现（或最长 10 秒兜底）后解除启动锁。
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if len(findGleamWindows()) > 0 {
				clearLaunching()
				return
			}
			time.Sleep(300 * time.Millisecond)
		}
		clearLaunching()
	}()
	_ = u
	return true
}

func closeWindowPlatform() {
	hwnds := findGleamWindows()

	// 优先向 app 窗口发 WM_CLOSE，优雅关闭（浏览器进程随之退出）。
	for _, h := range hwnds {
		pPostMessageW.Call(h, wmClose, 0, 0)
	}

	if len(hwnds) > 0 {
		// WM_CLOSE 是异步的：投递后立即返回，浏览器需要数百毫秒处理关闭。
		// 这里轮询等待窗口真正消失，最长 3 秒——否则下面 HTTP server 一关，
		// 浏览器页面失去后端就变成白屏，而窗口本身还挂着。
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if len(findGleamWindows()) == 0 {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}

		// 优雅关闭超时：找到仍存在的窗口所属进程，强杀整棵进程树。
		// 不能只杀 procs 里记录的启动器——Edge 的多进程架构下，启动器早已退出，
		// 真正持有窗口的是孙子辈的渲染进程。按窗口句柄反查 PID 才杀得对。
		for _, h := range findGleamWindows() {
			var pid uint32
			pGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
			if pid > 0 {
				killProcessTree(pid)
			}
		}
		return
	}

	// 没找到窗口句柄时，兜底结束仍被跟踪的启动器进程。
	mu.Lock()
	targets := append([]*exec.Cmd(nil), procs...)
	mu.Unlock()
	for _, c := range targets {
		if c.Process != nil {
			_ = c.Process.Kill()
		}
	}
}

// killProcessTree 强杀指定 PID 及其所有子进程。
// taskkill /T 走进程树，比手动枚举子进程可靠（Edge 的进程层级深且随时 fork）。
func killProcessTree(pid uint32) {
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprint(pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Run()
}

func windowOpenPlatform() bool {
	if len(findGleamWindows()) > 0 {
		return true
	}
	mu.Lock()
	defer mu.Unlock()
	return len(procs) > 0
}

// ---------- Win32 窗口枚举 / 激活 ----------

var (
	enumMu    sync.Mutex
	enumFound []uintptr
	enumCb    = syscall.NewCallback(enumProc)
)

// findGleamWindows 返回所有可见、标题匹配的 app 顶层窗口句柄。
func findGleamWindows() []uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumFound = enumFound[:0]
	pEnumWindows.Call(enumCb, 0)
	out := append([]uintptr(nil), enumFound...)
	enumFound = enumFound[:0]
	return out
}

func enumProc(hwnd uintptr, _ uintptr) uintptr {
	vis, _, _ := pIsWindowVisible.Call(hwnd)
	if vis == 0 {
		return 1 // 继续枚举
	}
	n, _, _ := pGetWindowTextLengthW.Call(hwnd)
	if n <= 0 {
		return 1
	}
	buf := make([]uint16, n+1)
	got, _, _ := pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if got <= 0 {
		return 1
	}
	title := syscall.UTF16ToString(buf[:got])
	if strings.Contains(title, windowTitle) {
		applyDarkTitlebar(hwnd)
		enumFound = append(enumFound, hwnd)
	}
	return 1
}

// applyDarkTitlebar 把 app 窗口标题栏刷成与深色界面一致，消除顶部那条浅色
// 「浏览器感」。DWM 属性为 Win11 能力，旧系统返回错误时静默忽略，不影响使用。
func applyDarkTitlebar(hwnd uintptr) {
	// 0x00BBGGRR
	var (
		useDark int32  = 1
		caption uint32 = 0x00120D0B // #0B0D12
		text    uint32 = 0x00F2F2F2 // 近白
	)
	pDwmSetWindowAttribute.Call(hwnd, uintptr(dwmwaUseImmersiveDarkMode),
		uintptr(unsafe.Pointer(&useDark)), unsafe.Sizeof(useDark))
	pDwmSetWindowAttribute.Call(hwnd, uintptr(dwmwaCaptionColor),
		uintptr(unsafe.Pointer(&caption)), unsafe.Sizeof(caption))
	pDwmSetWindowAttribute.Call(hwnd, uintptr(dwmwaTextColor),
		uintptr(unsafe.Pointer(&text)), unsafe.Sizeof(text))
}

// activateWindow 还原最小化并把窗口带到前台。
// 后台进程直接 SetForegroundWindow 常被系统拒绝：把「调用线程」与「当前前台
// 窗口所在线程」的输入队列临时绑定（AttachThreadInput），当前调用方就获得了
// 设置前台窗口的权限，随后再解绑。
func activateWindow(hwnd uintptr) {
	pShowWindow.Call(hwnd, swRestore)
	pShowWindow.Call(hwnd, swShow)

	curTid, _, _ := pGetCurrentThreadId.Call()
	fg, _, _ := pGetForegroundWindow.Call()
	fgTid := uintptr(0)
	if fg != 0 && fg != hwnd {
		r, _, _ := pGetWindowThreadProcessId.Call(fg, 0)
		fgTid = r
	}

	attached := false
	if fgTid != 0 && fgTid != curTid {
		if ok, _, _ := pAttachThreadInput.Call(curTid, fgTid, 1); ok != 0 {
			attached = true
		}
	}
	pSetForegroundWindow.Call(hwnd)
	pBringWindowToTop.Call(hwnd)
	if attached {
		pAttachThreadInput.Call(curTid, fgTid, 0)
	}
}
