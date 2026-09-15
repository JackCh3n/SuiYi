//go:build windows

// Package tray 系统托盘（Windows，纯 Win32：Shell_NotifyIcon + CreatePopupMenu/TrackPopupMenu）
//
// 交互（与用户使用习惯对齐）：
//   - 左键单击/双击 → 显示（并前置）主窗口
//   - 右键 → 弹出菜单：显示主窗口 / 打开浏览器 / 分隔 / 退出
//
// 说明：这里不用 getlantern/systray，因为它把左键和右键都送进同一个 showMenu 回调，
// 无法区分「左键显示窗口、右键出菜单」。自绘 Win32 实现还顺带修正了
// NOTIFYICON_VERSION_4 的事件解析（事件在 LOWORD(lParam)，wParam 是光标坐标）。
package tray

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCreateWindowEx   = user32.NewProc("CreateWindowExW")
	procDefWindowProc    = user32.NewProc("DefWindowProcW")
	procRegisterClassEx  = user32.NewProc("RegisterClassExW")
	procGetMessage       = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessage  = user32.NewProc("DispatchMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procPostMessage      = user32.NewProc("PostMessageW")
	procRegisterWinMsg   = user32.NewProc("RegisterWindowMessageW")
	procCreatePopupMenu  = user32.NewProc("CreatePopupMenu")
	procAppendMenuW      = user32.NewProc("AppendMenuW")
	procDestroyMenu      = user32.NewProc("DestroyMenu")
	procTrackPopupMenu   = user32.NewProc("TrackPopupMenu")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procSetForegroundWin = user32.NewProc("SetForegroundWindow")
	procLoadImageW       = user32.NewProc("LoadImageW")
	procLoadIconW        = user32.NewProc("LoadIconW")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")

	procShellNotifyIcon = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
)

const (
	nimAdd     = 0x00000000
	nimDelete  = 0x00000002
	nimSetVer  = 0x00000004
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	notifyVer  = 4 // NOTIFYICON_VERSION_4：wParam=光标坐标，lParam=MAKELONG(事件, 图标ID)

	wmDestroy     = 0x0002
	wmClose       = 0x0010
	wmCommand     = 0x0111
	wmLbuttonDbl  = 0x0203
	wmLbuttonUp   = 0x0202
	wmRbuttonUp   = 0x0205
	wmMouseMove   = 0x0200
	wmContextMenu = 0x007B // v4 下右键发的就是它

	// 托盘回调消息号取 WM_APP 段：0x0400~0x7FFF 是「窗口类私有」消息，
	// Explorer 跨进程投递不在保证范围内（实测用 0x0401 收不到点击）
	wmApp     = 0x8000
	trayMsgID = wmApp + 1

	// v4 下左键单击/键盘选中
	ninSelect    = 0x0400
	ninKeySelect = 0x0401

	mfString    = 0x00000000
	mfSeparator = 0x00000800

	tpmLeftAlign   = 0x0000
	tpmRightAlign  = 0x0008
	tpmBottomAlign = 0x0020
	tpmRightButton = 0x0002

	idmShow = 1001
	idmOpen = 1002
	idmExit = 1003

	imageIcon  = 1
	lrLoadFile = 0x0010

	smCxScreen = 0
	smCxSmIcon = 49
	smCySmIcon = 50
)

// Options 托盘参数
type Options struct {
	Icon    []byte // .ico 图标字节（空则用系统默认图标）
	Tooltip string // 悬停提示
	OnReady func() // 图标与菜单就绪（用于判断托盘是否可用）
	OnShow  func() // 左键单击 / 菜单「显示主窗口」
	OnOpen  func() // 菜单「打开浏览器」
	OnQuit  func() // 菜单「退出」
}

type notifyIconData struct {
	cbSize           uint32
	hWnd             syscall.Handle
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            syscall.Handle
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     syscall.Handle
}

type point struct {
	X, Y int32
}

type msg struct {
	HWnd    syscall.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

// WNDCLASSEXW：与 C 结构严格一致（x64 下 sizeof=80），不要自行加填充字段
type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         syscall.Handle
	hCursor       syscall.Handle
	hbrBackground syscall.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       syscall.Handle
}

const trayClassName = "SuiYiTrayWnd"

var (
	opts              Options
	trayHIcon         syscall.Handle
	taskbarCreatedMsg uint32
	menuOpen          bool // TrackPopupMenu 是模态的，防止叠出第二层菜单
	lastActionMu      sync.Mutex
	lastShowAt        time.Time
	lastMenuAt        time.Time
)

// debounce 同类动作在 window 时间内只放行一次
// （v4 下同一次左键会先后发 NIN_SELECT 与 WM_LBUTTONUP）
func debounce(last *time.Time, window time.Duration) bool {
	lastActionMu.Lock()
	defer lastActionMu.Unlock()
	if time.Since(*last) < window {
		return false
	}
	*last = time.Now()
	return true
}

// notifyEvent 取托盘通知事件：v4 与旧协议的事件都在 LOWORD(lParam)
// （v4 的 wParam 是光标坐标，早期误读 HIWORD(wParam) 会把 y 坐标当事件）
func notifyEvent(lParam uintptr) uint32 { return uint32(lParam) & 0xFFFF }

var trayWndProc = syscall.NewCallback(func(hWnd syscall.Handle, m uint32, wParam, lParam uintptr) uintptr {
	switch {
	case m == trayMsgID:
		switch ev := notifyEvent(lParam); ev {
		case wmContextMenu, wmRbuttonUp:
			if debounce(&lastMenuAt, 400*time.Millisecond) {
				showMenu(hWnd)
			}
		case wmLbuttonUp, wmLbuttonDbl, ninSelect, ninKeySelect:
			if debounce(&lastShowAt, 600*time.Millisecond) && opts.OnShow != nil {
				opts.OnShow()
			}
		case wmMouseMove:
			// 光标划过图标时 Explorer 会高频发送，忽略
		}
		return 0

	case m == wmCommand:
		switch uint16(wParam & 0xFFFF) {
		case idmShow:
			if opts.OnShow != nil {
				opts.OnShow()
			}
		case idmOpen:
			if opts.OnOpen != nil {
				opts.OnOpen()
			}
		case idmExit:
			if opts.OnQuit != nil {
				opts.OnQuit()
			}
			postQuit(hWnd)
		}
		return 0

	case m == taskbarCreatedMsg && taskbarCreatedMsg != 0:
		// 资源管理器重启后把图标加回去
		_ = addIcon(hWnd, trayHIcon)
		return 0

	case m == wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProc.Call(uintptr(hWnd), uintptr(m), wParam, lParam)
	return ret
})

// Run 启动托盘（阻塞），需在独立 goroutine 中调用
func Run(o Options) {
	defer func() { _ = recover() }() // 托盘异常不影响服务
	opts = o

	// 窗口创建、图标注册与消息循环必须在同一个 OS 线程上（Win32 消息派发要求线程亲和性）
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hWnd, err := createTrayWindow()
	if err != nil {
		fmt.Fprintf(os.Stderr, "托盘初始化失败: %v\n", err)
		return
	}
	if opts.OnReady != nil {
		opts.OnReady()
	}

	var m msg
	for {
		ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if ret == 0 || ret == ^uintptr(0) { // WM_QUIT / 出错
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	removeIcon(hWnd)
}

// createTrayWindow 注册窗口类、创建消息窗口并注册图标
func createTrayWindow() (syscall.Handle, error) {
	hIcon := loadIcon()
	if hIcon == 0 {
		return 0, fmt.Errorf("图标加载失败")
	}
	trayHIcon = hIcon

	hInst, _, _ := procGetModuleHandle.Call(0)
	cls := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   trayWndProc,
		hInstance:     syscall.Handle(hInst),
		hIcon:         hIcon,
		hIconSm:       hIcon,
		lpszClassName: syscall.StringToUTF16Ptr(trayClassName),
	}
	if atom, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&cls))); atom == 0 {
		return 0, fmt.Errorf("RegisterClassExW: %v", err)
	}

	hWnd, _, err := procCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(trayClassName))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("随译 SuiYi"))),
		0, 0, 0, 0, 0,
		0, // hWndParent：普通顶层窗口，可收到 Explorer 广播的 TaskbarCreated
		0,
		uintptr(hInst), // 必须与注册窗口类时的 hInstance 一致
		0,
	)
	if hWnd == 0 {
		return 0, fmt.Errorf("CreateWindowExW: %v", err)
	}

	if err := addIcon(syscall.Handle(hWnd), hIcon); err != nil {
		return 0, err
	}

	namePtr, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	id, _, _ := procRegisterWinMsg.Call(uintptr(unsafe.Pointer(namePtr)))
	taskbarCreatedMsg = uint32(id)

	return syscall.Handle(hWnd), nil
}

// loadIcon 从内嵌 ICO 创建 HICON：按 SM_CXSMICON 取小图标尺寸（16/24…），
// 比固定 32px 再让系统缩小更清晰
func loadIcon() syscall.Handle {
	if len(opts.Icon) > 0 {
		if f, err := os.CreateTemp("", "suiyi-tray-*.ico"); err == nil {
			path := f.Name()
			_, _ = f.Write(opts.Icon)
			_ = f.Close()
			defer os.Remove(path)

			pathPtr, _ := syscall.UTF16PtrFromString(path)
			cx, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
			cy, _, _ := procGetSystemMetrics.Call(smCySmIcon)
			if cx == 0 || cy == 0 {
				cx, cy = 32, 32
			}
			if h, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(pathPtr)),
				imageIcon, cx, cy, lrLoadFile); h != 0 {
				return syscall.Handle(h)
			}
			if h, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(pathPtr)),
				imageIcon, 32, 32, lrLoadFile); h != 0 {
				return syscall.Handle(h)
			}
		}
	}
	h, _, _ := procLoadIconW.Call(0, 32512) // IDI_APPLICATION 兜底
	return syscall.Handle(h)
}

func addIcon(hWnd, hIcon syscall.Handle) error {
	nid := notifyIconData{
		cbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		hWnd:             hWnd,
		uID:              1,
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: trayMsgID,
		hIcon:            hIcon,
	}
	tip := opts.Tooltip
	if tip == "" {
		tip = "随译 SuiYi · 本地翻译服务"
	}
	copy(nid.szTip[:], syscall.StringToUTF16(tip))

	if ret, _, err := procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid))); ret == 0 {
		return fmt.Errorf("Shell_NotifyIcon(NIM_ADD): %v", err)
	}
	nid.uFlags = 0
	nid.uVersion = notifyVer
	procShellNotifyIcon.Call(nimSetVer, uintptr(unsafe.Pointer(&nid)))
	return nil
}

func removeIcon(hWnd syscall.Handle) {
	nid := notifyIconData{
		cbSize: uint32(unsafe.Sizeof(notifyIconData{})),
		hWnd:   hWnd,
		uID:    1,
	}
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

// postQuit 结束消息循环（用 PostMessage(WM_QUIT) 而非 PostQuitMessage，
// 避免依赖调用方所在线程）
func postQuit(hWnd syscall.Handle) {
	procPostMessage.Call(uintptr(hWnd), 0x0012, 0, 0)
}

func showMenu(hWnd syscall.Handle) {
	if menuOpen {
		return
	}
	menuOpen = true
	defer func() { menuOpen = false }()

	procSetForegroundWin.Call(uintptr(hWnd)) // 不抢前台则点别处菜单不消失（KB135788）

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	// 光标在屏幕右半区时向左展开，避免菜单被挤出屏幕
	align := uintptr(tpmLeftAlign)
	if w, _, _ := procGetSystemMetrics.Call(smCxScreen); w > 0 && pt.X > int32(w)/2 {
		align = uintptr(tpmRightAlign)
	}

	menu, _, _ := procCreatePopupMenu.Call()
	appendMenu := func(id uintptr, text string) {
		procAppendMenuW.Call(menu, mfString, id,
			uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(text))))
	}
	appendMenu(idmShow, "显示主窗口")
	appendMenu(idmOpen, "打开浏览器")
	procAppendMenuW.Call(menu, mfSeparator, 0, 0)
	appendMenu(idmExit, "退出")

	// 不带 TPM_RETURNCMD：选中项会以 WM_COMMAND 送到窗口过程（标准做法）
	procTrackPopupMenu.Call(menu, align|tpmBottomAlign|tpmRightButton,
		uintptr(pt.X), uintptr(pt.Y), 0, uintptr(hWnd), 0)
	procPostMessage.Call(uintptr(hWnd), 0, 0, 0) // 修复菜单不消失（KB Q135788）
	procDestroyMenu.Call(menu)
}
