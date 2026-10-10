//go:build windows

// Package hotkey 系统级全局快捷键（Win32 RegisterHotKey）。
//
// 实现要点：RegisterHotKey(NULL, …) 把 WM_HOTKEY 投递到「调用线程」的消息队列，
// 因此监听必须运行在一个 LockOSThread 的独立线程上（与托盘/窗口线程互不干扰）。
// 组合键必须至少含一个修饰键（alt/ctrl/shift/win），否则会把单个按键全局抢走。
package hotkey

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procRegisterHotKey     = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	procGetMessage         = user32.NewProc("GetMessageW")
	procPostThreadMessage  = user32.NewProc("PostThreadMessageW")
	procGetCurrentThreadID = kernel32.NewProc("GetCurrentThreadId")
)

const (
	modAlt     = 0x0001
	modControl = 0x0002
	modShift   = 0x0004
	modWin     = 0x0008

	wmHotkey  = 0x0312
	wmStopMsg = 0x8000 + 2 // 线程消息：通知监听线程退出
)

type msg struct {
	HWnd    syscall.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// Parse 解析组合键字符串 → (修饰键位掩码, 虚拟键码, 规范化名称)。
// 支持修饰键 alt / ctrl(control) / shift / win(windows)，主键 A-Z / 0-9 / F1-F12。
// 例："alt+f" → MOD_ALT, 'F'；"Ctrl+Alt+T" → MOD_CONTROL|MOD_ALT, 'T'。
func Parse(combo string) (mods uint32, vk uint32, name string, err error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(combo)), "+")
	var key string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		switch p {
		case "":
			continue
		case "alt":
			mods |= modAlt
		case "ctrl", "control":
			mods |= modControl
		case "shift":
			mods |= modShift
		case "win", "windows", "super", "cmd", "meta":
			mods |= modWin
		default:
			if key != "" {
				return 0, 0, "", fmt.Errorf("快捷键 %q 里出现了多个主键", combo)
			}
			key = p
		}
	}
	if key == "" {
		return 0, 0, "", fmt.Errorf("快捷键 %q 缺少主键（字母/数字/F1-F12）", combo)
	}
	if mods == 0 {
		return 0, 0, "", fmt.Errorf("快捷键 %q 至少需要一个修饰键（alt/ctrl/shift/win），否则会全局抢占单个按键", combo)
	}
	vk, err = vkOf(key)
	if err != nil {
		return 0, 0, "", fmt.Errorf("快捷键 %q：%w", combo, err)
	}
	return mods, vk, strings.ToLower(strings.TrimSpace(combo)), nil
}

// vkOf 主键 → Windows 虚拟键码
func vkOf(key string) (uint32, error) {
	if len(key) == 1 {
		c := key[0]
		if c >= 'a' && c <= 'z' {
			return uint32('A') + uint32(c-'a'), nil
		}
		if c >= '0' && c <= '9' {
			return uint32(c), nil
		}
		return 0, fmt.Errorf("不支持的主键 %q", key)
	}
	if strings.HasPrefix(key, "f") && len(key) <= 3 {
		var n int
		if _, err := fmt.Sscanf(key, "f%d", &n); err == nil && n >= 1 && n <= 12 {
			return 0x70 + uint32(n-1), nil // VK_F1 = 0x70
		}
	}
	return 0, fmt.Errorf("不支持的主键 %q（可用：a-z、0-9、F1-F12）", key)
}

// Start 注册全局快捷键并启动监听线程，返回停止函数。
// 注册在监听线程上同步完成（失败立即返回错误，例如组合键被其他程序占用）；
// 停止函数注销快捷键并结束线程，可安全调用多次。
func Start(combo string, onHotkey func()) (func(), error) {
	mods, vk, name, err := Parse(combo)
	if err != nil {
		return nil, err
	}
	stopCh := make(chan struct{})
	var once sync.Once
	errCh := make(chan error, 1)
	go func() { errCh <- listen(mods, vk, name, onHotkey, stopCh) }()
	select {
	case e := <-errCh: // 注册失败（listen 立即返回）
		return nil, e
	case <-time.After(400 * time.Millisecond): // 注册成功，已进入消息循环
		return func() { once.Do(func() { close(stopCh) }) }, nil
	}
}

// listen 在独立线程上注册并监听（阻塞）
func listen(mods, vk uint32, name string, onHotkey func(), stop <-chan struct{}) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if r, _, err := procRegisterHotKey.Call(0, 1, uintptr(mods), uintptr(vk)); r == 0 {
		return fmt.Errorf("注册全局快捷键 %q 失败（可能被其他程序占用）: %v", name, err)
	}
	defer procUnregisterHotKey.Call(0, 1)

	// 唤醒线程的看门狗：listen 退出时也要结束，否则它会永久阻塞在 <-stop 上（每换一次快捷键泄漏一个）
	done := make(chan struct{})
	defer close(done)
	tid, _, _ := procGetCurrentThreadID.Call()
	go func() {
		select {
		case <-stop:
			procPostThreadMessage.Call(tid, wmStopMsg, 0, 0) // 唤醒 GetMessage
		case <-done:
		}
	}()

	var m msg
	for {
		ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if ret == 0 || ret == ^uintptr(0) { // WM_QUIT / 出错
			return nil
		}
		switch m.Message {
		case wmHotkey:
			if onHotkey != nil {
				go onHotkey() // 回调里可能读剪贴板/注入 JS，别阻塞消息循环
			}
		case wmStopMsg:
			return nil
		}
	}
}
