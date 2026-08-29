//go:build windows

// Package autostart 开机自启管理（Windows：HKCU\...\Run 注册表键）
package autostart

import (
	"fmt"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const name = "SuiYi"

// Enable 注册开机自启（exe 路径 + serve 参数）
func Enable(exePath string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开自启注册表失败: %w", err)
	}
	defer k.Close()
	return k.SetStringValue(name, `"`+exePath+`" serve`)
}

// Disable 取消开机自启
func Disable() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	defer k.Close()
	return k.DeleteValue(name)
}

// IsEnabled 查询是否已设置开机自启
func IsEnabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return false, nil
		}
		return false, err
	}
	defer k.Close()
	_, _, err = k.GetStringValue(name)
	if err != nil {
		if err == registry.ErrNotExist {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
