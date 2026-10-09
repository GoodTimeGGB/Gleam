//go:build windows

package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Window management notes:
//
// Edge/Chrome --app windows live outside the Gleam process. Recording cmd.Process
// alone cannot reuse an existing shell or close it reliably (the launcher often
// exits immediately; the real owner is a Chromium process tree).
//
// We therefore:
//   - Enumerate top-level windows by title ("Gleam · 微光") for activate / close
//   - Prefer Edge; use a versioned --user-data-dir under ~/.gleam/browser-profile/<ver>
//   - Never spawn a second --app while that profile is still locked (blank white
//     shells are the usual Chromium SingletonLock symptom)
//   - After close: wait until titled windows are gone, kill tracked process trees,
//     then clear stale lock files before the next open
//   - Relax readiness: titled HWNDs count even when not yet IsWindowVisible; ShowWindow
//   - On --app paint failure: retry once with GPU soft-fallback flags + alternate
//     profile. Do NOT silently open the system browser — tray "在浏览器中打开" is
//     the explicit last resort
//
// a81cbb8 / #16 context:
//   close-path early return left SingletonLock held → blank --app on reopen.
//   Silent default-browser fallback weakened standalone-shell UX (#17).

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")

	pEnumWindows              = user32.NewProc("EnumWindows")
	pGetWindowTextW           = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	pIsWindow                 = user32.NewProc("IsWindow")
	pIsWindowVisible          = user32.NewProc("IsWindowVisible")
	pIsIconic                 = user32.NewProc("IsIconic")
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

	dwmwaUseImmersiveDarkMode = 33
	dwmwaBorderColor          = 34
	dwmwaCaptionColor         = 35
	dwmwaTextColor            = 36

	appLaunchReadyTimeout = 8 * time.Second
	appCloseWaitTimeout   = 5 * time.Second
	pollInterval          = 200 * time.Millisecond
)

// windowTitle matches index.html <title> and --app-name / --window-name.
const windowTitle = "Gleam · 微光"

var (
	mu        sync.Mutex
	procs     []*exec.Cmd
	launching bool
)

// browserPaths returns Edge-first executable candidates (Chrome is fallback).
// GLEAM_BROWSER overrides the entire preference list when set.
func browserPaths() []string {
	if env := os.Getenv("GLEAM_BROWSER"); env != "" {
		return []string{env}
	}
	local := os.Getenv("LOCALAPPDATA")
	programs64 := os.Getenv("ProgramFiles")
	programsX86 := os.Getenv("ProgramFiles(x86)")
	raw := []string{
		filepath.Join(local, "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(programs64, "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(programsX86, "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(programs64, "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(programsX86, "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(local, "Chromium", "Application", "chrome.exe"),
		filepath.Join(local, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
	}
	return preferEdgeOverChrome(raw)
}

func profileDir() string { return profileDirPath() }

func resolveBrowserExe() string {
	for _, p := range browserPaths() {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func openWindowPlatform(target string, onClose func()) bool {
	if hwnds := findGleamWindows(); len(hwnds) > 0 {
		for _, h := range hwnds {
			activateWindow(h)
		}
		return true
	}

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

	profile := profileDir()
	_ = os.MkdirAll(profile, 0o755)

	// Profile still locked with no titled window: wait briefly, activate if a
	// titled HWND appears, otherwise clear stale markers. Never open the system
	// browser here — that is tray "在浏览器中打开" only.
	if anyProfileLockPresent(profile) {
		if !waitUntil(2*time.Second, pollInterval, func() bool {
			return !anyProfileLockPresent(profile) || len(findGleamWindows()) > 0
		}) {
			if hwnds := findGleamWindows(); len(hwnds) > 0 {
				for _, h := range hwnds {
					activateWindow(h)
				}
				clearLaunching()
				return true
			}
		}
		if hwnds := findGleamWindows(); len(hwnds) > 0 {
			for _, h := range hwnds {
				activateWindow(h)
			}
			clearLaunching()
			return true
		}
		if !anyProfileLockPresent(profile) || len(findGleamWindows()) == 0 {
			removeStaleProfileLocks(profile)
		}
	}

	exe := resolveBrowserExe()
	if exe == "" {
		clearLaunching()
		return false
	}

	// Attempt 0: primary versioned profile + normal flags.
	ok, pid := launchAppAttempt(exe, chromeAppArgs(target, profile, windowTitle), onClose)
	if ok {
		ready := waitUntil(appLaunchReadyTimeout, pollInterval, func() bool {
			return len(findGleamWindows()) > 0
		})
		hwnds := findGleamWindows()
		if appShellReady(len(hwnds), ready) {
			clearLaunching()
			return true
		}
		teardownAppSpawn(pid, profile)
		if !shouldRetryAppLaunch(0, len(hwnds), ready) {
			clearLaunching()
			return false
		}
	} else if pid > 0 {
		teardownAppSpawn(pid, profile)
	}

	// Attempt 1 (once): alternate profile + GPU soft-fallback flags.
	alt := alternateProfileDir(profile)
	_ = os.MkdirAll(alt, 0o755)
	removeStaleProfileLocks(alt)
	ok, pid = launchAppAttempt(exe, chromeAppArgsWithGPUFallback(target, alt, windowTitle), onClose)
	if !ok {
		clearLaunching()
		return false
	}
	ready := waitUntil(appLaunchReadyTimeout, pollInterval, func() bool {
		return len(findGleamWindows()) > 0
	})
	hwnds := findGleamWindows()
	if appShellReady(len(hwnds), ready) {
		clearLaunching()
		return true
	}
	teardownAppSpawn(pid, alt)
	clearLaunching()
	return false
}

// launchAppAttempt starts Chromium --app with the given args. Returns (started, pid).
func launchAppAttempt(exe string, args []string, onClose func()) (bool, int) {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return false, 0
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
	return true, pid
}

func teardownAppSpawn(pid int, profile string) {
	if pid > 0 {
		killProcessTree(uint32(pid))
	}
	mu.Lock()
	for i, p := range procs {
		if p.Process != nil && p.Process.Pid == pid {
			procs = append(procs[:i], procs[i+1:]...)
			break
		}
	}
	mu.Unlock()
	removeStaleProfileLocks(profile)
}

func closeWindowPlatform() {
	hwnds := findGleamWindows()
	for _, h := range hwnds {
		pPostMessageW.Call(h, wmClose, 0, 0)
	}

	_ = waitUntil(appCloseWaitTimeout, pollInterval, func() bool {
		return len(findGleamWindows()) == 0
	})

	for _, h := range findGleamWindows() {
		var pid uint32
		pGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
		if pid > 0 {
			killProcessTree(pid)
		}
	}

	mu.Lock()
	targets := append([]*exec.Cmd(nil), procs...)
	procs = nil
	launching = false
	mu.Unlock()
	for _, c := range targets {
		if c.Process != nil {
			killProcessTree(uint32(c.Process.Pid))
		}
	}

	_ = waitUntil(2*time.Second, pollInterval, func() bool {
		return len(findGleamWindows()) == 0
	})

	if len(findGleamWindows()) == 0 {
		primary := profileDir()
		removeStaleProfileLocks(primary)
		removeStaleProfileLocks(alternateProfileDir(primary))
	}
}

// killProcessTree force-kills pid and its children via taskkill /T.
func killProcessTree(pid uint32) {
	if pid == 0 {
		return
	}
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

// ---------- Win32 window enum / activate ----------

var (
	enumMu    sync.Mutex
	enumFound []uintptr
	enumCb    = syscall.NewCallback(enumProc)
)

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
	// Relaxed readiness: do not require IsWindowVisible up front. Chromium --app
	// can briefly own a titled but non-visible HWND; we ShowWindow and still count it.
	alive, _, _ := pIsWindow.Call(hwnd)
	if alive == 0 {
		return 1
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
	if !strings.Contains(title, windowTitle) {
		return 1
	}
	vis, _, _ := pIsWindowVisible.Call(hwnd)
	iconic, _, _ := pIsIconic.Call(hwnd)
	if vis == 0 || iconic != 0 {
		pShowWindow.Call(hwnd, swRestore)
		pShowWindow.Call(hwnd, swShow)
	}
	applyDarkTitlebar(hwnd)
	enumFound = append(enumFound, hwnd)
	return 1
}

func applyDarkTitlebar(hwnd uintptr) {
	var (
		useDark int32  = 1
		caption uint32 = 0x00120D0B // #0B0D12
		text    uint32 = 0x00F2F2F2
	)
	pDwmSetWindowAttribute.Call(hwnd, uintptr(dwmwaUseImmersiveDarkMode),
		uintptr(unsafe.Pointer(&useDark)), unsafe.Sizeof(useDark))
	pDwmSetWindowAttribute.Call(hwnd, uintptr(dwmwaCaptionColor),
		uintptr(unsafe.Pointer(&caption)), unsafe.Sizeof(caption))
	pDwmSetWindowAttribute.Call(hwnd, uintptr(dwmwaTextColor),
		uintptr(unsafe.Pointer(&text)), unsafe.Sizeof(text))
}

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
