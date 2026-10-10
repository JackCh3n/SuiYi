//go:build windows

package main

// 剪贴板文本读取（Win32，全局快捷键取剪贴板内容用）。
// 剪贴板可能被其他程序短暂占用，OpenClipboard 失败时重试。
//
// 指针处理：GlobalLock 返回 uintptr，直接转 unsafe.Pointer 会被 go vet 拦下
// （且 reflect.Convert 运行时会 panic）。这里改用 lstrcpynW 让系统把内容拷进
// Go 分配的缓冲——全程只有 uintptr(unsafe.Pointer(&buf[0]))（合法方向），
// 不需要任何 uintptr → unsafe.Pointer 转换。

import (
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	clipKernel32 = syscall.NewLazyDLL("kernel32.dll")
	clipUser32   = syscall.NewLazyDLL("user32.dll")

	procOpenClip     = clipUser32.NewProc("OpenClipboard")
	procCloseClip    = clipUser32.NewProc("CloseClipboard")
	procGetClipData  = clipUser32.NewProc("GetClipboardData")
	procGlobalLock   = clipKernel32.NewProc("GlobalLock")
	procGlobalUnlock = clipKernel32.NewProc("GlobalUnlock")
	procLstrlenW     = clipKernel32.NewProc("lstrlenW")
	procLstrcpynW    = clipKernel32.NewProc("lstrcpynW")
)

const cfUnicodeText = 13

// readClipboardText 读取剪贴板文本（无文本/读取失败返回空串）。
// 只取前 100 万字符，避免误复制超大内容把翻译请求撑爆。
func readClipboardText() string {
	for i := 0; i < 10; i++ { // 剪贴板被占用时稍等重试
		if r, _, _ := procOpenClip.Call(0); r != 0 {
			text := readClipboardLocked()
			procCloseClip.Call()
			return text
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ""
}

func readClipboardLocked() string {
	h, _, _ := procGetClipData.Call(cfUnicodeText)
	if h == 0 {
		return "" // 剪贴板里没有文本（可能是图片等）
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return ""
	}
	defer procGlobalUnlock.Call(h)

	n, _, _ := procLstrlenW.Call(ptr) // 宽字符数（不含结尾 NUL）
	if n == 0 {
		return ""
	}
	if n > 1_000_000 {
		n = 1_000_000
	}
	buf := make([]uint16, n+1)
	// lstrcpynW(dst, src, max) 最多拷 max-1 个字符并保证 NUL 结尾
	procLstrcpynW.Call(uintptr(unsafe.Pointer(&buf[0])), ptr, uintptr(len(buf)))
	return strings.TrimSpace(syscall.UTF16ToString(buf))
}
