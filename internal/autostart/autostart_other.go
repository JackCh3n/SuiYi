//go:build !windows

// Package autostart 开机自启管理（非 Windows 平台暂不支持）
package autostart

import "fmt"

// Enable 注册开机自启（其他平台暂不支持）
func Enable(exePath string) error {
	return fmt.Errorf("当前平台暂不支持开机自启")
}

// Disable 取消开机自启
func Disable() error {
	return nil
}

// IsEnabled 查询是否已设置开机自启
func IsEnabled() (bool, error) {
	return false, nil
}
