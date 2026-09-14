//go:build !windows

package main

// attachConsole 非 Windows 平台由终端提供控制台（noop）
func attachConsole() {}

// outputVisible 非 Windows 平台默认终端可见
func outputVisible() bool { return true }

// reportFatal 非 Windows 平台只写 stderr（noop）
func reportFatal(string) {}
