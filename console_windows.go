//go:build windows

package main

import (
	"log"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procAllocConsole     = kernel32.NewProc("AllocConsole")
	procGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	procGetStdHandle     = kernel32.NewProc("GetStdHandle")
	procSetConsoleTitleW = kernel32.NewProc("SetConsoleTitleW")
)

const stdOutputHandle = ^uintptr(11) // STD_OUTPUT_HANDLE = -11

// attachConsole 传 -debug 时显示控制台黑窗口：
//   - 已有控制台（命令行启动）→ 直接用，输出已在终端可见
//   - 无控制台（双击启动，-H=windowsgui）→ AllocConsole 新建控制台并接管 stdout/stderr
func attachConsole() {
	if h, _, _ := procGetConsoleWindow.Call(); h != 0 {
		consoleVisible = true
		return
	}
	// 继承了父进程（cmd / CI）控制台句柄：输出本就可见，不再另开窗口
	if h, _, _ := procGetStdHandle.Call(stdOutputHandle); h != 0 {
		consoleVisible = true
		return
	}
	if r, _, _ := procAllocConsole.Call(); r == 0 {
		return // 分配失败（极少数策略限制）→ 保持无控制台，错误走弹窗
	}
	consoleVisible = true

	// 重新打开标准流：控制台子系统里 os.Stdout 仍是无效句柄，
	// 必须换成新控制台的设备文件，fmt / log 才会输出到黑窗口。
	if out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		os.Stdout, os.Stderr = out, out
		log.SetOutput(out)
	}
	if in, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
		os.Stdin = in
	}
	if title, err := syscall.UTF16PtrFromString("随译 SuiYi · 调试控制台（-debug）"); err == nil {
		procSetConsoleTitleW.Call(uintptr(unsafe.Pointer(title)))
	}
}

// outputVisible 标准输出是否已经可见（有控制台窗口或继承了控制台句柄）
func outputVisible() bool {
	if consoleVisible {
		return true
	}
	if h, _, _ := procGetConsoleWindow.Call(); h != 0 {
		return true
	}
	h, _, _ := procGetStdHandle.Call(stdOutputHandle)
	return h != 0
}

// reportFatal 无可见输出时用弹窗提示错误（GUI 双击启动否则毫无反馈）
func reportFatal(msg string) {
	if outputVisible() {
		return
	}
	text, err := windows.UTF16PtrFromString(msg)
	if err != nil {
		return
	}
	caption, _ := windows.UTF16PtrFromString("随译 SuiYi · 启动失败")
	_, _ = windows.MessageBox(0, text, caption, windows.MB_OK|windows.MB_ICONERROR)
}
