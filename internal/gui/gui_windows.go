//go:build windows

// Package gui 桌面窗口（Wails v2.12 + WebView2，仅 Windows）。
//
// 与服务端合并：同一可执行文件默认启动本窗口，窗口内通过 GetAPIURL 拿到
// 本地 Web 管理界面地址并重定向（同源加载，无 CORS / 鉴权问题），
// 同时 127.0.0.1:<port> 仍可被任意浏览器直接访问。
//
// 构建必须带 -tags production，否则 Wails 走 dev 模式（找不到开发服务器，只显示占位框）。
package gui

import (
	"bytes"
	"embed"
	"fmt"
	"net/http"
	"os/exec"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend
var assets embed.FS

// apiURLPlaceholder 窗口首页里的占位符：启动时替换成真实的本地地址。
// 页面原先靠 Wails 绑定（window.go）取地址，绑定注入存在时序竞争、且兜底值写死 8848，
// 用户把端口改成 9848 后首页就变成"127.0.0.1 拒绝连接"——改为 Go 端注入，彻底去掉竞态。
const apiURLPlaceholder = "__SUIYI_API_URL__"

// Options 窗口启动参数
type Options struct {
	APIURL  string // 本地 Web/API 地址（窗口重定向目标）
	Version string // 版本号（关于对话框 / 绑定）

	// HideOnClose 关闭窗口时隐藏到托盘而非退出（托盘可用时为 true）
	HideOnClose bool
	// Debug 开发调试：打开浏览器右键菜单
	Debug bool

	// QuitCh 收到关闭信号（托盘退出 / Ctrl+C）时退出窗口
	QuitCh <-chan struct{}
	// ShowCh 收到信号时显示并前置窗口（托盘「显示主窗口」）
	ShowCh <-chan struct{}
}

// Run 启动窗口（阻塞，须在主 goroutine 调用；窗口退出后返回）
func Run(o Options) error {
	app := &App{apiURL: o.APIURL, version: o.Version, ready: make(chan struct{})}

	// 把真实地址注入首页（端口可配置，页面上不再有写死的 8848）
	indexPage := bytes.ReplaceAll(mustReadAsset("frontend/index.html"), []byte(apiURLPlaceholder), []byte(o.APIURL))

	if o.QuitCh != nil {
		go func() {
			<-o.QuitCh
			<-app.ready // 等窗口就绪，避免 ctx 未初始化
			app.Quit()
		}()
	}
	if o.ShowCh != nil {
		go func() {
			for range o.ShowCh {
				<-app.ready
				app.ShowWindow()
			}
		}()
	}

	err := wails.Run(&options.App{
		Title:     "随译 SuiYi · 本地翻译",
		Width:     1180,
		Height:    800,
		MinWidth:  900,
		MinHeight: 640,
		AssetServer: &assetserver.Options{
			Assets: assets,
			// 首页（frontend/index.html）由这里注入真实地址后加载并跳转本地 Web 界面；
			// Middleware 在静态资源之前执行，才抢得到 "/"（Assets 会先命中 index.html）
			Middleware: assetserver.ChainMiddleware(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/" || r.URL.Path == "/index.html" {
						w.Header().Set("Content-Type", "text/html; charset=utf-8")
						_, _ = w.Write(indexPage)
						return
					}
					next.ServeHTTP(w, r)
				})
			}),
		},
		Menu:                     buildMenu(app),
		BackgroundColour:         options.NewRGB(245, 246, 250),
		OnStartup:                app.startup,
		HideWindowOnClose:        o.HideOnClose,
		EnableDefaultContextMenu: o.Debug,
		Bind:                     []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "suiyi-desktop",
			OnSecondInstanceLaunch: func(_ options.SecondInstanceData) { app.ShowWindow() },
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			Theme:                windows.SystemDefault,
		},
	})
	if err != nil {
		return fmt.Errorf("GUI 启动失败: %w", err)
	}
	return nil
}

// buildMenu 原生应用菜单 + 快捷键（Wails keys）
func buildMenu(app *App) *menu.Menu {
	m := menu.NewMenu()

	file := m.AddSubmenu("文件")
	file.AddText("打开 Web 界面（浏览器）", keys.CmdOrCtrl("o"), func(_ *menu.CallbackData) {
		_ = app.OpenWeb()
	})
	file.AddText("显示/隐藏窗口", keys.CmdOrCtrl("h"), func(_ *menu.CallbackData) {
		app.ToggleWindow()
	})
	file.AddSeparator()
	file.AddText("打开数据目录", keys.CmdOrCtrl("d"), func(_ *menu.CallbackData) {
		_ = app.OpenDataDir()
	})
	file.AddText("打开模型目录", keys.CmdOrCtrl("m"), func(_ *menu.CallbackData) {
		_ = app.OpenModelsDir()
	})
	file.AddSeparator()
	file.AddText("退出", keys.CmdOrCtrl("q"), func(_ *menu.CallbackData) {
		app.Quit()
	})

	help := m.AddSubmenu("帮助")
	help.AddText("关于", nil, func(_ *menu.CallbackData) {
		app.About()
	})
	return m
}

// mustReadAsset 读内嵌资源（编译期就存在，失败即 panic，避免带着坏页面启动）
func mustReadAsset(name string) []byte {
	b, err := assets.ReadFile(name)
	if err != nil {
		panic("内嵌资源缺失: " + name + ": " + err.Error())
	}
	return b
}

// openURL 在默认浏览器打开地址（Windows：rundll32，不弹命令行窗口）
func openURL(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
