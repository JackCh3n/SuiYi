//go:build !windows

// Package gui 桌面窗口：仅 Windows 提供（Wails v2.12 + WebView2）。
// 其他平台编译期占位，运行时由 main 走无窗口的服务模式。
package gui

import "errors"

// Options 窗口启动参数（非 Windows 仅占位，字段与 Windows 版保持一致）
type Options struct {
	APIURL      string
	Version     string
	HideOnClose bool
	Debug       bool
	QuitCh      <-chan struct{}
	ShowCh      <-chan struct{}
}

// Run 非 Windows 无桌面窗口
func Run(Options) error {
	return errors.New("当前平台不支持桌面窗口，请使用 serve 模式（浏览器访问）")
}
