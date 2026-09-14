//go:build !windows

package engine

import "os/exec"

// hideConsole 非 Windows 平台子进程本就没有独立窗口（noop）
func hideConsole(*exec.Cmd) {}
