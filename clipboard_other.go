//go:build !windows

package main

// readClipboardText 非 Windows 平台暂不支持（返回空）
func readClipboardText() string { return "" }
