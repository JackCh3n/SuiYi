//go:build windows

package main

import "suiyi/internal/gui"

// uiSupported Windows 提供原生桌面窗口
func uiSupported() bool { return true }

// runWindow 启动桌面窗口（阻塞至窗口退出）
func runWindow(o windowOptions) error {
	return gui.Run(gui.Options{
		APIURL:      o.apiURL,
		Version:     o.version,
		HideOnClose: o.hideOnClose,
		Debug:       o.debug,
		QuitCh:      o.quit,
		ShowCh:      o.show,
	})
}
