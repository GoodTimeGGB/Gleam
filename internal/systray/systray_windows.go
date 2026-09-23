//go:build windows

package systray

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// 本文件实现 Windows 系统托盘：
// - Shell_NotifyIcon 注册/删除托盘图标
// - 自定义隐藏窗口接收回调消息
// - TrackPopupMenu 显示右键菜单
// - 左键单击/双击触发 OnActivate
// - 支持运行时更新 tooltip、图标、菜单

var (
	shell32              = syscall.NewLazyDLL("shell32.dll")
	user32               = syscall.NewLazyDLL("user32.dll")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	gdi32                = syscall.NewLazyDLL("gdi32.dll")
	comctl32             = syscall.NewLazyDLL("comctl32.dll")
	pShell_NotifyIconW   = shell32.NewProc("Shell_NotifyIconW")
	pCreateWindowExW     = user32.NewProc("CreateWindowExW")
	pDefWindowProcW      = user32.NewProc("DefWindowProcW")
	pDispatchMessageW    = user32.NewProc("DispatchMessageW")
	pGetMessageW         = user32.NewProc("GetMessageW")
	pPostQuitMessage     = user32.NewProc("PostQuitMessage")
	pPostMessageW        = user32.NewProc("PostMessageW")
	pRegisterClassExW    = user32.NewProc("RegisterClassExW")
	pTranslateMessage    = user32.NewProc("TranslateMessage")
	pLoadIconMetric      = user32.NewProc("LoadIconMetric") // Win7+，兜底失败时用默认
	pLoadCursorW         = user32.NewProc("LoadCursorW")
	pCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	pAppendMenuW         = user32.NewProc("AppendMenuW")
	pTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	pDestroyMenu         = user32.NewProc("DestroyMenu")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pGetCursorPos        = user32.NewProc("GetCursorPos")
	pDestroyWindow       = user32.NewProc("DestroyWindow")
	pGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	pLoadIconW           = user32.NewProc("LoadIconW")
	pLoadImageW          = user32.NewProc("LoadImageW")
)

const (
	imageIcon     = 1 // LoadImage 类型：ICON
	lrDefaultSize = 0x00000040
	lrShared      = 0x00008000
)

const (
	wmApp          = 0x8000
	wmTrayCallback = wmApp + 1
	wmQuit         = wmApp + 2
	wmUpdateMenu   = wmApp + 3
	wmUpdateTip    = wmApp + 4
	wmUpdateIcon   = wmApp + 5

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifGuid    = 0x00000020

	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmCommand       = 0x0111

	mfString       = 0x00000000
	mfGrayed       = 0x00000001
	mfSep          = 0x00000800
	tpmBottomalign = 0x0020
	tpmRightalign  = 0x0008
	tpmLeftbutton  = 0x0000
	tpmReturncmd   = 0x0100

	csVRedraw     = 0x0001
	csHRedraw     = 0x0002
	csGlobalclass = 0x4000

	idiApplication = 32512
	idcArrow       = 32512
	colorWindow    = 5

	pmemMoveable   = 0x0002
	lrDefaultColor = 0x00000000
)

type point struct{ X, Y int32 }
type msg struct {
	Hwnd   uintptr
	Msg    uint32
	Wparam uintptr
	Lparam uintptr
	Time   int32
	Pt     point
}
type wndclassex struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	Hinstance     uintptr
	Hicon         uintptr
	Hcursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HiconSm       uintptr
}
type notifyicondata struct {
	CbSize           uint32
	Hwnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	Hicon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UTimeoutVersion  uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         guid
	HballoonIcon     uintptr
}
type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	theRunner   *Runner
	runnerMu    sync.Mutex
	menuCmds    = map[uint32]func(){}
	menuCounter = uint32(1000)
	hwnd        uintptr
	nidHicon    uintptr
	currentIcon []byte
)

func runPlatform(cfg Config) error {
	runnerMu.Lock()
	if theRunner != nil {
		runnerMu.Unlock()
		return nil
	}
	theRunner = &Runner{cfg: cfg, quit: make(chan struct{})}
	runnerMu.Unlock()

	// 关键：Win32 消息按「创建窗口的线程」分发，GetMessage 只取当前线程队列。
	// 因此窗口注册、托盘图标添加、消息循环必须在同一个线程上执行。
	// 用 runtime.LockOSThread 把这条 goroutine 固定在同一个 OS 线程。
	startErr := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		instance, _, _ := pGetModuleHandleW.Call(0)
		if err := registerSystrayWindow(instance); err != nil {
			startErr <- err
			return
		}
		addNotifyIcon(cfg)
		startErr <- nil

		// 在同一线程内跑消息循环（阻塞直到 WM_QUIT）
		messageLoop()

		// 线程退出前移除图标
		removeNotifyIcon()
	}()
	if err := <-startErr; err != nil {
		return err
	}

	// 阻塞直到 Quit
	<-theRunner.quit
	destroyMenuCmds()
	return nil
}

func registerSystrayWindow(instance uintptr) error {
	className, _ := syscall.UTF16PtrFromString("GleamSystrayCls")
	windowName, _ := syscall.UTF16PtrFromString("GleamSystray")
	hc, _, _ := pLoadCursorW.Call(0, uintptr(idcArrow))
	wcx := wndclassex{
		CbSize:        uint32(unsafe.Sizeof(wndclassex{})),
		Style:         csHRedraw | csVRedraw,
		LpfnWndProc:   syscall.NewCallback(wndProc),
		Hinstance:     instance,
		Hcursor:       hc,
		HbrBackground: uintptr(colorWindow + 1),
		LpszClassName: className,
	}
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wcx)))
	ret, _, _ := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
		0,
		0, 0, 0, 0,
		0, 0, instance, 0,
	)
	if ret == 0 {
		return fmt.Errorf("systray: CreateWindowEx failed")
	}
	hwnd = ret
	return nil
}

func messageLoop() {
	var m msg
	for {
		ret, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func wndProc(hwndArg uintptr, msgID uint32, wparam, lparam uintptr) uintptr {
	switch msgID {
	case wmTrayCallback:
		switch uint32(lparam) {
		case wmLButtonUp, wmLButtonDblClk:
			runnerMu.Lock()
			fn := theRunner.cfg.OnActivate
			runnerMu.Unlock()
			if fn != nil {
				go fn()
			}
		case wmRButtonUp:
			showMenu()
		}
	case wmCommand:
		if fn, ok := menuCmds[uint32(wparam)]; ok {
			go fn()
		}
	case wmUpdateMenu:
		runnerMu.Lock()
		items := theRunner.cfg.MenuItems
		runnerMu.Unlock()
		rebuildMenuItems(items)
	case wmUpdateTip:
		runnerMu.Lock()
		tip := theRunner.cfg.Tooltip
		runnerMu.Unlock()
		modifyTip(tip)
	case wmUpdateIcon:
		runnerMu.Lock()
		b := currentIcon
		runnerMu.Unlock()
		modifyIcon(b)
	case wmQuit:
		removeNotifyIcon()
		pPostQuitMessage.Call(0)
	}
	ret, _, _ := pDefWindowProcW.Call(hwndArg, uintptr(msgID), wparam, lparam)
	return ret
}

func addNotifyIcon(cfg Config) {
	icon := loadIconFromBytes(cfg.IconBytes)
	nidHicon = icon
	nid := notifyicondata{
		CbSize:           uint32(unsafe.Sizeof(notifyicondata{})),
		Hwnd:             hwnd,
		UID:              1,
		UFlags:           nifMessage | nifIcon | nifTip,
		UCallbackMessage: wmTrayCallback,
		Hicon:            icon,
	}
	copy(nid.SzTip[:], utf16Trunc(cfg.Tooltip, 127))
	pShell_NotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	rebuildMenuItems(cfg.MenuItems)
}

func removeNotifyIcon() {
	nid := notifyicondata{CbSize: uint32(unsafe.Sizeof(notifyicondata{})), Hwnd: hwnd, UID: 1}
	pShell_NotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func modifyTip(tip string) {
	nid := notifyicondata{
		CbSize: uint32(unsafe.Sizeof(notifyicondata{})),
		Hwnd:   hwnd, UID: 1, UFlags: nifTip,
	}
	copy(nid.SzTip[:], utf16Trunc(tip, 127))
	pShell_NotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func modifyIcon(b []byte) {
	icon := loadIconFromBytes(b)
	if icon == 0 {
		return
	}
	nidHicon = icon
	nid := notifyicondata{
		CbSize: uint32(unsafe.Sizeof(notifyicondata{})),
		Hwnd:   hwnd, UID: 1, UFlags: nifIcon, Hicon: icon,
	}
	pShell_NotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func loadIconFromBytes(_ []byte) uintptr {
	// 优先加载 exe 内嵌的自定义图标资源（rsrc 嵌入，ID=1）
	if h, _, _ := pGetModuleHandleW.Call(0); h != 0 {
		if icon, _, _ := pLoadImageW.Call(h, uintptr(1), imageIcon, 0, 0, lrDefaultSize|lrShared); icon != 0 {
			return icon
		}
	}
	// 回退：系统默认应用图标
	h, _, _ := pLoadIconW.Call(0, uintptr(32512))
	return h
}

func showMenu() {
	hMenu, _, _ := pCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	runnerMu.Lock()
	items := theRunner.cfg.MenuItems
	runnerMu.Unlock()
	buildMenu(hMenu, items)
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(hwnd)
	ret, _, _ := pTrackPopupMenu.Call(
		hMenu,
		uintptr(tpmBottomalign|tpmRightalign|tpmLeftbutton|tpmReturncmd),
		uintptr(pt.X), uintptr(pt.Y),
		0, hwnd, 0,
	)
	if ret != 0 {
		if fn, ok := menuCmds[uint32(ret)]; ok {
			go fn()
		}
	}
	pDestroyMenu.Call(hMenu)
}

func buildMenu(hMenu uintptr, items []MenuItem) {
	for _, it := range items {
		menuCounter++
		id := menuCounter
		menuCmds[id] = it.OnClick
		flags := uintptr(mfString)
		if it.Disabled {
			flags |= mfGrayed
		}
		textPtr, _ := syscall.UTF16PtrFromString(it.Title)
		pAppendMenuW.Call(hMenu, flags, uintptr(id), uintptr(unsafe.Pointer(textPtr)))
		if it.SepAfter {
			pAppendMenuW.Call(hMenu, mfSep, 0, 0)
		}
	}
}

func rebuildMenuItems(items []MenuItem) {
	// TrackPopupMenu 是临时菜单，无需重建；菜单由 showMenu 动态构造。
	// 这里仅存储到配置。
	runnerMu.Lock()
	theRunner.cfg.MenuItems = items
	runnerMu.Unlock()
}

func destroyMenuCmds() {
	menuCmds = map[uint32]func(){}
}

func quitPlatform() {
	if hwnd != 0 {
		pPostMessageW.Call(hwnd, wmQuit, 0, 0)
	}
	runnerMu.Lock()
	if theRunner != nil && !theRunner.closed {
		theRunner.closed = true
		close(theRunner.quit)
	}
	runnerMu.Unlock()
}

func setTooltipPlatform(tip string) {
	runnerMu.Lock()
	if theRunner == nil {
		runnerMu.Unlock()
		return
	}
	theRunner.cfg.Tooltip = tip
	runnerMu.Unlock()
	if hwnd != 0 {
		pPostMessageW.Call(hwnd, wmUpdateTip, 0, 0)
	}
}

func setIconPlatform(ico []byte) {
	runnerMu.Lock()
	if theRunner == nil {
		runnerMu.Unlock()
		return
	}
	currentIcon = ico
	runnerMu.Unlock()
	if hwnd != 0 {
		pPostMessageW.Call(hwnd, wmUpdateIcon, 0, 0)
	}
}

func updateMenuPlatform(items []MenuItem) {
	runnerMu.Lock()
	if theRunner == nil {
		runnerMu.Unlock()
		return
	}
	theRunner.cfg.MenuItems = items
	runnerMu.Unlock()
}

func utf16Trunc(s string, n int) []uint16 {
	u, _ := syscall.UTF16FromString(s)
	if len(u) > n {
		u = u[:n]
		u[len(u)-1] = 0
	}
	return u
}
