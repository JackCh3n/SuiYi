//go:build !windows

package main

import (
	"os/exec"
	"runtime"
)

// openBrowser 在默认浏览器打开地址（macOS 用 open，其他平台用 xdg-open）
func openBrowser(url string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", url).Start()
	}
	return exec.Command("xdg-open", url).Start()
}
