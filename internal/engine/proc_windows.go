//go:build windows

package engine

import (
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW 子进程不分配控制台窗口。
// 主程序以 GUI 子系统构建（-H=windowsgui）时自身没有控制台，Windows 会给
// 控制台子进程（llama-server / hy-mt）新建一个黑窗口；一旦用户关闭该窗口，
// 子进程随即被杀，引擎就"启动失败"了，因此这里必须显式禁止。
const createNoWindow = 0x08000000

// hideConsole 让推理子进程在后台静默运行
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
