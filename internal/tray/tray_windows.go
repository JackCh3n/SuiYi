//go:build windows

// Package tray 系统托盘（Windows，getlantern/systray，无需 CGO）
package tray

import "github.com/getlantern/systray"

// Run 启动系统托盘（阻塞），需在 goroutine 中调用。
// onOpen 打开管理界面；onQuit 请求退出应用。
func Run(onOpen func(), onQuit func()) {
	defer func() { _ = recover() }() // 托盘异常不影响服务
	systray.Run(func() {
		systray.SetTitle("随译 SuiYi")
		systray.SetTooltip("随译 SuiYi · 本地翻译服务")
		mOpen := systray.AddMenuItem("打开管理界面", "打开 Web 管理界面")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出", "退出随译")
		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					onOpen()
				case <-mQuit.ClickedCh:
					onQuit()
					systray.Quit()
					return
				}
			}
		}()
	}, func() {})
}
