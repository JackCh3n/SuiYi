//go:build windows

package gui

import (
	"context"
	"os/exec"
	"sync"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"suiyi/internal/config"
)

// App Wails 绑定对象（前端可通过 window.go.main.App.* 调用）
type App struct {
	ctx     context.Context
	apiURL  string
	version string
	ready   chan struct{} // startup 完成后关闭，供后台 goroutine 安全使用 ctx

	mu      sync.Mutex
	visible bool // 窗口是否可见（用于菜单「显示/隐藏」）
}

// startup 窗口就绪回调：保存上下文供菜单/快捷键/托盘调用 runtime
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.visible = true
	close(a.ready)
}

// GetVersion 返回版本号
func (a *App) GetVersion() string { return a.version }

// GetAPIURL 返回本地 API/Web 地址（窗口首页据此重定向）
func (a *App) GetAPIURL() string { return a.apiURL }

// OpenWeb 在默认浏览器打开 Web 界面（菜单/快捷键）
func (a *App) OpenWeb() error { return openURL(a.apiURL) }

// OpenDataDir 打开数据目录（data/）
func (a *App) OpenDataDir() error { return exec.Command("explorer", config.DataDir()).Start() }

// OpenModelsDir 打开模型目录（工作目录/models）
func (a *App) OpenModelsDir() error {
	return exec.Command("explorer", config.Resolve("models")).Start()
}

// ShowWindow 显示并前置窗口（托盘「显示主窗口」/ 二次启动）
func (a *App) ShowWindow() {
	a.mu.Lock()
	a.visible = true
	a.mu.Unlock()
	wruntime.WindowShow(a.ctx)
	wruntime.WindowUnminimise(a.ctx)
}

// HideWindow 隐藏窗口（保留服务与托盘）
func (a *App) HideWindow() {
	a.mu.Lock()
	a.visible = false
	a.mu.Unlock()
	wruntime.WindowHide(a.ctx)
}

// ToggleWindow 显示/隐藏窗口（菜单「显示/隐藏窗口」）
func (a *App) ToggleWindow() {
	a.mu.Lock()
	a.visible = !a.visible
	v := a.visible
	a.mu.Unlock()
	if v {
		wruntime.WindowShow(a.ctx)
	} else {
		wruntime.WindowHide(a.ctx)
	}
}

// Quit 退出应用（菜单「退出」/ 托盘「退出」）
func (a *App) Quit() { wruntime.Quit(a.ctx) }

// About 显示关于对话框（菜单「帮助→关于」）
func (a *App) About() {
	_, _ = wruntime.MessageDialog(a.ctx, wruntime.MessageDialogOptions{
		Title: "关于 随译 SuiYi",
		Message: "随译 SuiYi · 本地翻译服务\n\n" +
			"版本: " + a.version + "\n" +
			"GUI: Wails v2.12 + WebView2\n" +
			"推理: llama.cpp（本地离线 / OpenAI 兼容 API）\n\n" +
			"Web 界面: " + a.apiURL,
		Buttons: []string{"确定"},
	})
}
