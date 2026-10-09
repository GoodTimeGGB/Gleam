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
//   - Use a dedicated --user-data-dir under ~/.gleam/browser-profile
//   - Never spawn a second --app while that profile is still locked (blank white
//     shells are the usual Chromium SingletonLock symptom)
//   - After close: wait until titled windows are gone, kill tracked process trees,
//     then clear stale lock files before the next open
//   - If --app fails to produce a titled window, fall back to the system default browser
//
// a81cbb8 regression (corrected here):
//   closeWindowPlatform waited for WM_CLOSE then returned early without finishing
//   process-tree / profile-lock cleanup, and taskkill'ed PIDs from
//   GetWindowThreadProcessId (often a renderer, not the profile-owning browser).
//   That left SingletonLock held → tray Open / show-window spawned another --app
//   against a locked profile → blank white window while http://127.0.0.1:8787/ still
//   worked in a normal browser tab.

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
	// Profile still locked with no titled window: a previous Chromium tree is alive
	// (classic post-a81cbb8 reopen failure). Do not spawn a second --app.
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
			clearLaunching()
			return openDefaultBrowser(target)
		}
		if hwnds := findGleamWindows(); len(hwnds) > 0 {
			for _, h := range hwnds {
				activateWindow(h)
			}
			clearLaunching()
			return true
		}
		// Lock cleared or stale — drop markers before spawn.
		if !anyProfileLockPresent(profile) || len(findGleamWindows()) == 0 {
			removeStaleProfileLocks(profile)
		}
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
		return openDefaultBrowser(target)
	}

	args := chromeAppArgs(target, profile, windowTitle)
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		clearLaunching()
		return openDefaultBrowser(target)
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

	ready := waitUntil(appLaunchReadyTimeout, pollInterval, func() bool {
		return len(findGleamWindows()) > 0
	})
	hwnds := findGleamWindows()
	if shouldFallbackAfterAppLaunch(len(hwnds), ready) {
		// Blank / unpainted --app: tear down this spawn and open the real browser.
		killProcessTree(uint32(pid))
		mu.Lock()
		for i, p := range procs {
			if p.Process != nil && p.Process.Pid == pid {
				procs = append(procs[:i], procs[i+1:]...)
				break
			}
		}
		mu.Unlock()
		removeStaleProfileLocks(profile)
		clearLaunching()
		return openDefaultBrowser(target)
	}
	clearLaunching()
	return true
}

func openDefaultBrowser(target string) bool {
	if err := openURLPlatform(target); err != nil {
		return false
	}
	return true
}

func closeWindowPlatform() {
	hwnds := findGleamWindows()
	for _, h := range hwnds {
		pPostMessageW.Call(h, wmClose, 0, 0)
	}

	// Always wait until titled Gleam windows are gone before tearing down the HTTP
	// server / allowing a reopen — otherwise the shell paints white against a dead backend.
	_ = waitUntil(appCloseWaitTimeout, pollInterval, func() bool {
		return len(findGleamWindows()) == 0
	})

	// Force-kill anything still showing a Gleam title (best-effort). Prefer tracked
	// launcher PIDs for /T trees; window PIDs alone are unreliable (often renderers).
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

	// With no titled window left, Chromium lock files are stale or about to be —
	// clear them so the next Open Gleam does not hit SingletonLock → blank shell.
	if len(findGleamWindows()) == 0 {
		removeStaleProfileLocks(profileDir())
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
	vis, _, _ := pIsWindowVisible.Call(hwnd)
	if vis == 0 {
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
	if strings.Contains(title, windowTitle) {
		applyDarkTitlebar(hwnd)
		enumFound = append(enumFound, hwnd)
	}
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
