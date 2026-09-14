//go:build windows

// Package tray 系统托盘（Windows，getlantern/systray，无需 CGO）
package tray

import (
	"runtime"

	"github.com/getlantern/systray"
)

// Options 托盘参数
type Options struct {
	Icon    []byte // .ico 图标字节（空则用系统默认图标）
	Tooltip string // 悬停提示
	OnReady func() // 图标就绪回调（用于判断托盘是否可用）
	OnShow  func() // 「显示主窗口」
	OnOpen  func() // 「打开浏览器」
	OnQuit  func() // 「退出」
}

// Run 启动系统托盘（阻塞），需在独立 goroutine 中调用。
// 菜单：显示主窗口 / 打开浏览器 / 退出。
func Run(o Options) {
	defer func() { _ = recover() }() // 托盘异常不影响服务
	// systray 在 Windows 上创建隐藏窗口并跑自己的消息循环，
	// 必须与创建窗口的线程同属一个 OS 线程，否则收不到点击消息。
	runtime.LockOSThread()

	systray.Run(func() {
		if len(o.Icon) > 0 {
			systray.SetIcon(o.Icon)
		}
		systray.SetTitle("随译 SuiYi")
		if o.Tooltip != "" {
			systray.SetTooltip(o.Tooltip)
		}

		mShow := systray.AddMenuItem("显示主窗口", "显示/前置桌面窗口")
		mOpen := systray.AddMenuItem("打开浏览器", "在默认浏览器打开管理界面")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出", "退出随译")

		if o.OnReady != nil {
			o.OnReady()
		}

		go func() {
			for {
				select {
				case <-mShow.ClickedCh:
					if o.OnShow != nil {
						o.OnShow()
					}
				case <-mOpen.ClickedCh:
					if o.OnOpen != nil {
						o.OnOpen()
					}
				case <-mQuit.ClickedCh:
					if o.OnQuit != nil {
						o.OnQuit()
					}
					systray.Quit()
					return
				}
			}
		}()
	}, func() {})
}
