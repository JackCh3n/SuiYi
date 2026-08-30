package main

import (
	"context"
	"os/exec"

	"suiyi/internal/config"
)

// App Wails 绑定对象（前端可通过 window.go.main.App.* 调用）
type App struct {
	apiURL  string
	version string
}

// startup 窗口就绪回调
func (a *App) startup(ctx context.Context) {}

// beforeClose 关闭回调：返回 false 允许关闭（服务核心在 main 的 defer 中停止）
func (a *App) beforeClose(ctx context.Context) bool {
	return false
}

// GetVersion 返回版本号
func (a *App) GetVersion() string { return a.version }

// GetAPIURL 返回本地 API/Web 地址
func (a *App) GetAPIURL() string { return a.apiURL }

// OpenDataDir 打开数据目录（data/）
func (a *App) OpenDataDir() error {
	return exec.Command("explorer", config.DataDir()).Start()
}

// OpenModelsDir 打开模型目录（工作目录/models）
func (a *App) OpenModelsDir() error {
	return exec.Command("explorer", config.Resolve("models")).Start()
}
