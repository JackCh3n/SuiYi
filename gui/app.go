package main

import (
	"context"
	"os/exec"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"suiyi/internal/config"
)

// App Wails 绑定对象（前端可通过 window.go.main.App.* 调用）
type App struct {
	ctx     context.Context
	apiURL  string
	version string

	mu      sync.Mutex
	visible bool // 窗口是否可见（用于菜单「显示/隐藏」）
}

// startup 窗口就绪回调：保存上下文供菜单/快捷键调用 runtime
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.visible = true
}

// beforeClose 关闭回调：返回 false 允许关闭（服务核心在 main 的 defer 中停止）
func (a *App) beforeClose(ctx context.Context) bool {
	return false
}

// GetVersion 返回版本号
func (a *App) GetVersion() string { return a.version }

// GetAPIURL 返回本地 API/Web 地址
func (a *App) GetAPIURL() string { return a.apiURL }

// OpenWeb 在默认浏览器打开 Web 界面（菜单/快捷键）
func (a *App) OpenWeb() error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", a.apiURL).Start()
}

// OpenDataDir 打开数据目录（data/）
func (a *App) OpenDataDir() error {
	return exec.Command("explorer", config.DataDir()).Start()
}

// OpenModelsDir 打开模型目录（工作目录/models）
func (a *App) OpenModelsDir() error {
	return exec.Command("explorer", config.Resolve("models")).Start()
}

// ToggleWindow 显示/隐藏窗口（菜单「显示/隐藏窗口」）
func (a *App) ToggleWindow() {
	a.mu.Lock()
	a.visible = !a.visible
	v := a.visible
	a.mu.Unlock()
	if v {
		runtime.WindowShow(a.ctx)
	} else {
		runtime.WindowHide(a.ctx)
	}
}

// Quit 退出应用（菜单「退出」）
func (a *App) Quit() {
	runtime.Quit(a.ctx)
}

// About 显示关于对话框（菜单「帮助→关于」）
func (a *App) About() {
	_, _ = runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Title:   "关于 随译 SuiYi",
		Message: "随译 SuiYi · 本地翻译服务\n\n" +
			"版本: " + a.version + "\n" +
			"GUI: Wails v2.12 + WebView2\n" +
			"推理: llama.cpp（本地离线 / OpenAI 兼容 API）\n\n" +
			"Web 界面: " + a.apiURL,
		Buttons: []string{"确定"},
	})
}
