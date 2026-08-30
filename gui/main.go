// 随译 SuiYi 桌面 GUI：Wails v2.12 + WebView2
//
// 启动本地服务核心（推理引擎 + 翻译队列 + API/Web），
// 在原生窗口中加载本地 Web 管理界面（Google 式左右对照翻译）。
package main

import (
	"embed"
	"fmt"
	"log"
	"net/http"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"suiyi/internal/appcore"
	"suiyi/internal/config"
)

//go:embed all:frontend
var assets embed.FS

// version 构建版本号，由 CI / wails build 注入
var version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("读取配置失败: %v", err)
	}

	// 启动服务核心：本地 llama 引擎 + 翻译队列 + HTTP API/Web
	core, err := appcore.Start(cfg)
	if err != nil {
		log.Fatalf("启动服务核心失败: %v", err)
	}
	defer core.Stop()

	go func() {
		if err := core.Srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP 服务错误: %v", err)
		}
	}()

	apiURL := fmt.Sprintf("http://127.0.0.1:%d/", cfg.APIPort)
	app := &App{apiURL: apiURL, version: version}

	err = wails.Run(&options.App{
		Title:     "随译 SuiYi · 本地翻译",
		Width:     1180,
		Height:    800,
		MinWidth:  900,
		MinHeight: 640,
		AssetServer: &assetserver.Options{
			// 首页（frontend/index.html）通过绑定 GetAPIURL 获得地址后重定向到本地 Web 界面
			Assets: assets,
		},
		Menu:             buildMenu(app),
		BackgroundColour: options.NewRGB(245, 246, 250),
		OnStartup:        app.startup,
		OnBeforeClose:    app.beforeClose,
		Bind:             []interface{}{app},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			Theme:                windows.SystemDefault,
		},
	})
	if err != nil {
		log.Fatalf("GUI 启动失败: %v", err)
	}
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
