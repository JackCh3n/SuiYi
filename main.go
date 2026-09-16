// 随译 SuiYi：桌面 GUI + 本地服务（同一可执行文件）
//
// 用法：
//
//	suiyi                    桌面窗口（内嵌 Web 管理界面）+ API/Web 服务（默认 127.0.0.1:8848）
//	suiyi -debug             同上，并附带控制台窗口（查看日志）
//	suiyi serve              无窗口服务（保留托盘），适合脚本 / 开机自启
//	suiyi serve -headless    纯服务（无窗口、无托盘），适合服务器
//	suiyi translate "文本"    调用本地服务单次翻译
//
// 控制台窗口：默认不显示黑窗口（Windows 构建加 -H=windowsgui 隐藏子系统），
// 传 -debug 时用 AllocConsole 动态分配控制台并接管 stdout/stderr。
//
// 窗口与浏览器共用同一地址：窗口首页重定向到 http://127.0.0.1:<port>/，
// 任意浏览器也可直接打开该地址。
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"suiyi/internal/api"
	"suiyi/internal/appcore"
	"suiyi/internal/autostart"
	"suiyi/internal/clipboard"
	"suiyi/internal/config"
	"suiyi/internal/queue"
	"suiyi/internal/tray"
)

// trayIcon 托盘图标（应用图标 .ico，Windows 托盘用）
//
//go:embed assets/appicon.ico
var trayIcon []byte

// version 构建版本号，由 CI 通过 -ldflags "-X main.version=..." 注入
var version = "dev"

// consoleVisible -debug 是否已接管控制台（Windows，用于决定错误是否弹窗）
var consoleVisible bool

func main() {
	args := os.Args[1:]
	if hasFlag(args, "debug") {
		attachConsole()
	}
	if len(args) > 0 {
		switch args[0] {
		case "translate":
			os.Exit(runTranslate(args[1:]))
		case "serve":
			os.Exit(runServe(args[1:]))
		}
	}
	os.Exit(runDesktop(args))
}

// hasFlag 判断参数里是否出现 -xxx / --xxx（兼容 -xxx=true）
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		if key, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "="); key == name {
			return true
		}
	}
	return false
}

// appOptions 运行形态：窗口 / 托盘由各入口决定
type appOptions struct {
	port, ngl int
	window    bool // 显示桌面窗口
	tray      bool // 显示系统托盘
	debug     bool // -debug（控制台已接管）
}

// windowOptions 传给平台相关窗口实现的参数
type windowOptions struct {
	apiURL      string
	version     string
	hideOnClose bool
	debug       bool
	quit        <-chan struct{}
	show        <-chan struct{}
}

// runDesktop 默认入口：桌面窗口 + API/Web 服务（浏览器同样可访问）
func runDesktop(args []string) int {
	fs := flag.NewFlagSet("suiyi", flag.ExitOnError)
	port := fs.Int("port", 0, "Web/API 端口（覆盖配置）")
	ngl := fs.Int("ngl", 0, "GPU 层数（0=纯 CPU，Vulkan 版可调 99）")
	noWindow := fs.Bool("headless", false, "不显示桌面窗口（仅服务 + 托盘）")
	noTray := fs.Bool("notray", false, "不显示系统托盘")
	_ = fs.Bool("debug", false, "显示控制台窗口（查看日志）")
	_ = fs.Parse(args)

	return run(appOptions{
		port:   *port,
		ngl:    *ngl,
		window: uiSupported() && !*noWindow,
		tray:   !*noTray,
		debug:  hasFlag(args, "debug"),
	})
}

// runServe 无窗口服务（保留托盘，除非 -headless）
func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", 0, "Web/API 端口（覆盖配置）")
	ngl := fs.Int("ngl", 0, "GPU 层数（0=纯 CPU，Vulkan 版可调 99）")
	headless := fs.Bool("headless", false, "无托盘模式")
	_ = fs.Bool("debug", false, "显示控制台窗口（查看日志）")
	_ = fs.Parse(args)

	return run(appOptions{
		port:  *port,
		ngl:   *ngl,
		tray:  !*headless,
		debug: hasFlag(args, "debug"),
	})
}

// run 启动核心（推理后端 + 翻译队列 + API/Web）+ 托盘 + 窗口，阻塞至退出
func run(o appOptions) int {
	cfg, err := config.Load()
	if err != nil {
		return fail(o, "读取配置失败: %v", err)
	}
	if o.port != 0 {
		cfg.APIPort = o.port
	}
	if o.ngl != 0 {
		cfg.NGL = o.ngl
	}
	cfg.Headless = !o.tray

	webURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.APIPort)
	fmt.Printf("随译 SuiYi v%s · 推理后端 %s · 模型 %s\n", version, cfg.Backend, config.Resolve(cfg.ModelPath))

	// 启动前清理：结束已在运行的旧实例与推理进程（llama-server / hy-mt），释放 API / 引擎端口
	if killed := killExisting(cfg); len(killed) > 0 {
		fmt.Printf("已结束既有进程: %s\n", strings.Join(killed, "、"))
		time.Sleep(700 * time.Millisecond) // 等端口释放
	}

	fmt.Printf("管理界面: %s/  窗口: %v  托盘: %v\n", webURL, o.window, o.tray)

	core, err := appcore.Start(cfg)
	if err != nil {
		return fail(o, "%v", err)
	}
	defer core.Stop()

	// HTTP 服务：窗口与浏览器共用同一地址
	serveErr := make(chan error, 1)
	go func() {
		if err := core.Srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serveErr <- err
		}
	}()

	// 端口被占用（非本程序占用时无法清理）：打开既有界面，避免多开抢占端口
	select {
	case err := <-serveErr:
		if isAddrInUse(err) {
			fmt.Printf("端口 %d 已被占用（PID %v），直接打开 %s\n",
				cfg.APIPort, listeningPIDs(cfg.APIPort), webURL)
			_ = openBrowser(webURL)
			return 0
		}
		return fail(o, "HTTP 服务启动失败: %v", err)
	case <-time.After(800 * time.Millisecond):
	}

	// 开机自启：按配置应用（Windows 注册表 Run 键）
	applyAutostart(core, cfg)

	// 剪贴板自动翻译：复制即译（去抖/过滤由 clipboard 包处理）
	if cfg.ClipboardEnabled && o.tray {
		clipCtx, clipCancel := context.WithCancel(context.Background())
		defer clipCancel()
		startClipboard(clipCtx, core, cfg)
	}

	// 退出协调：托盘「退出」/ 窗口关闭 / Ctrl+C
	var quitOnce sync.Once
	quit := make(chan struct{})
	requestQuit := func() { quitOnce.Do(func() { close(quit) }) }

	// 系统托盘：左键显示主窗口；右键菜单 显示主窗口 / 打开浏览器 / 退出
	show := make(chan struct{}, 1)
	trayReady := make(chan struct{})
	if o.tray {
		go tray.Run(tray.Options{
			Icon:    trayIcon,
			Tooltip: "随译 SuiYi · 本地翻译服务",
			OnReady: func() { close(trayReady) },
			OnShow: func() {
				if o.window {
					select {
					case show <- struct{}{}:
					default:
					}
					return
				}
				_ = openBrowser(webURL) // 无窗口模式（serve）：左键退化为打开浏览器
			},
			OnOpen: func() { _ = openBrowser(webURL) },
			OnQuit: requestQuit,
		})
	}

	// 托盘就绪才启用「关闭窗口 → 隐藏到托盘」，否则关闭窗口直接退出（避免无处可点）
	hideOnClose := false
	if o.window && o.tray {
		select {
		case <-trayReady:
			hideOnClose = true
			fmt.Println("关闭窗口将隐藏到托盘，右键托盘图标可退出")
		case <-time.After(2 * time.Second):
			fmt.Println("托盘初始化超时，关闭窗口将直接退出")
		}
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; requestQuit() }()

	if o.window {
		if err := runWindow(windowOptions{
			apiURL:      webURL + "/",
			version:     version,
			hideOnClose: hideOnClose,
			debug:       o.debug,
			quit:        quit,
			show:        show,
		}); err != nil {
			return fail(o, "%v", err)
		}
	} else {
		<-quit
	}

	fmt.Println("正在退出…")
	return 0
}

// applyAutostart 按配置应用开机自启（Windows 注册表 Run 键）
func applyAutostart(core *appcore.Core, cfg *config.Config) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if !cfg.Autostart {
		_ = autostart.Disable()
		return
	}
	if err := autostart.Enable(exe); err != nil {
		core.Srv.Log("设置开机自启失败: %v", err)
		return
	}
	core.Srv.Log("开机自启已启用")
}

// startClipboard 剪贴板自动翻译：复制即译
func startClipboard(ctx context.Context, core *appcore.Core, cfg *config.Config) {
	core.Srv.Log("剪贴板自动翻译已启用（复制即译）")
	go func() {
		clipboard.Watch(ctx, func(text string) {
			core.Srv.Log("剪贴板捕获: %s", truncateRunes(text, 40))
			done := core.Q.Submit(&queue.Job{Type: "translate", Args: &api.TranslateRequest{Text: text, Target: cfg.TargetLang}})
			item := api.ClipItem{Time: time.Now().Format("15:04:05"), Text: truncateRunes(text, 60)}
			switch res := (<-done).(type) {
			case *api.TranslateResult:
				if res.Ok {
					item.Result = res.Text
				} else {
					item.Error = res.Error
				}
			case error:
				item.Error = res.Error()
			}
			core.Srv.PushClipItem(item)
		})
	}()
}

// isAddrInUse 判断监听失败是否因端口被占用（Windows 的报错文案与 Unix 不同）
func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "only one usage of each socket address")
}

// fail 输出错误；无可见控制台时弹窗提示（否则 GUI 用户看不到任何信息）
func fail(o appOptions, format string, args ...any) int {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, msg)
	log.Println(msg)
	reportFatal(msg)
	return 1
}

// truncateRunes 按字符截断（避免截断多字节字符）
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// runTranslate 走本地 API 单次翻译（要求服务已运行）
func runTranslate(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "用法: suiyi translate \"要翻译的文本\" [-t zh|en|...] [-p 端口]")
		return 1
	}
	text := args[len(args)-1]
	target := "en"
	port := 0 // 0 = 未指定，用配置里的端口（端口可配置，不能写死 8848）
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "-t":
			target = args[i+1]
		case "-p":
			fmt.Sscanf(args[i+1], "%d", &port)
		}
	}
	if port == 0 {
		port = config.Defaults().APIPort
		if cfg, err := config.Load(); err == nil && cfg.APIPort > 0 {
			port = cfg.APIPort
		}
	}
	body, _ := json.Marshal(map[string]string{"text": text, "target": target})
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/translate", port), "application/json", strings.NewReader(string(body)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "服务未运行，请先启动 suiyi：", err)
		return 1
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out api.TranslateResult
	if err := json.Unmarshal(raw, &out); err != nil {
		fmt.Println(string(raw))
		return 1
	}
	if !out.Ok {
		fmt.Fprintln(os.Stderr, "翻译失败:", out.Error)
		return 1
	}
	fmt.Println(out.Text)
	return 0
}
