//go:build !windows

package main

// uiSupported 非 Windows 无桌面窗口（服务模式，浏览器访问）
func uiSupported() bool { return false }

// runWindow 非 Windows 无窗口实现（main 中不会调用，占位保证编译通过）
func runWindow(windowOptions) error { return nil }
