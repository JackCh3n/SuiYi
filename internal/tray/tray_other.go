//go:build !windows

// Package tray 系统托盘（其他平台暂不支持，noop）
package tray

// Run 其他平台暂不支持托盘（noop）
func Run(onOpen func(), onQuit func()) {}
