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
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"
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

// wailsLogger 把 Wails 内部日志转发到标准错误（排查拖拽/消息问题用）
type wailsLogger struct{}

func (wailsLogger) Print(m string)   { fmt.Fprintln(os.Stderr, "[wails]", m) }
func (wailsLogger) Trace(m string)   {}
func (wailsLogger) Debug(m string)   {}
func (wailsLogger) Info(m string)    { fmt.Fprintln(os.Stderr, "[wails]", m) }
func (wailsLogger) Warning(m string) { fmt.Fprintln(os.Stderr, "[wails]", m) }
func (wailsLogger) Error(m string)   { fmt.Fprintln(os.Stderr, "[wails:ERR]", m) }
func (wailsLogger) Fatal(m string)   { fmt.Fprintln(os.Stderr, "[wails:FATAL]", m) }

// currentApp 当前窗口实例（Run 时记录，供全局快捷键等外部入口注入页面 JS）
var currentApp *App

// Run 启动窗口（阻塞，须在主 goroutine 调用；窗口退出后返回）
func Run(o Options) error {
	app := &App{apiURL: o.APIURL, version: o.Version, startedAt: time.Now(), ready: make(chan struct{})}
	currentApp = app

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

	// 提取 Web 界面的源（如 http://127.0.0.1:9848）：Wails 的消息（拖拽/窗口控制）
	// 按源校验，重定向后的页面必须加入白名单才能用 runtime
	apiOrigin := o.APIURL // 形如 http://127.0.0.1:9848/
	if i := strings.Index(o.APIURL[7:], "/"); i >= 0 {
		apiOrigin = o.APIURL[:7+i] // 去掉路径，只留 scheme://host:port
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
		// 无边框窗口 + 自绘标题栏（页面 header 即拖拽区，右侧窗口按钮）。
		// 原生菜单与无边框不兼容，其功能已迁入页面：Ctrl+O/H/D/M/Q（页面分发器）、
		// 关于（帮助面板）、退出（托盘/Ctrl+Q）。
		Frameless:                true,
		Logger:                   wailsLogger{},
		BindingsAllowedOrigins:   apiOrigin,
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

// PushHotkeyText 把剪贴板文本注入当前页面并触发翻译。
// 页面（Web 管理界面）需定义 window.suiyiHotkeyTranslate(text)；
// 若当前页还没定义（如仍在启动页/别的页面），静默跳过。
// WindowExecJS 会派发到 UI 线程执行，可在任意 goroutine 调用。
func PushHotkeyText(text string) {
	app := currentApp
	if app == nil {
		return
	}
	select {
	case <-app.ready: // 等窗口就绪，避免 ctx 未初始化
	case <-time.After(3 * time.Second):
		return
	}
	raw, err := json.Marshal(text)
	if err != nil {
		return
	}
	runtime.WindowExecJS(app.ctx, "window.suiyiHotkeyTranslate && window.suiyiHotkeyTranslate("+string(raw)+")")
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
