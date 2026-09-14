//go:build !windows

// Package tray 系统托盘（其他平台暂不支持，noop）
package tray

// Options 托盘参数（非 Windows 仅占位，字段与 Windows 版一致）
type Options struct {
	Icon    []byte
	Tooltip string
	OnReady func()
	OnShow  func()
	OnOpen  func()
	OnQuit  func()
}

// Run 其他平台暂不支持托盘（noop）
func Run(Options) {}
