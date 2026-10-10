//go:build !windows

// Package hotkey 系统级全局快捷键：其他平台暂不支持（noop）
package hotkey

import "errors"

// Parse 非 Windows 平台不支持
func Parse(combo string) (mods uint32, vk uint32, name string, err error) {
	return 0, 0, "", errors.New("当前平台不支持全局快捷键")
}

// Start 非 Windows 平台不支持
func Start(combo string, onHotkey func()) (func(), error) {
	return nil, errors.New("当前平台不支持全局快捷键")
}
