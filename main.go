// 随译 SuiYi：本地翻译服务入口
//
// 用法：
//
//	suiyi serve               启动 Web 管理界面 + API（默认 127.0.0.1:8848）
//	suiyi translate "文本"     单次翻译，需服务已在运行（走本地 API）
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
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

// version 构建版本号，由 CI 通过 -ldflags "-X main.version=..." 注入
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "translate" {
		os.Exit(runTranslate(os.Args[2:]))
	}
	os.Exit(runServe(os.Args[1:]))
}

// runServe 常驻服务
func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", 0, "Web/API 端口（覆盖配置）")
	ngl := fs.Int("ngl", 0, "GPU 层数（0=纯 CPU，Vulkan 版可调 99）")
	headless := fs.Bool("headless", false, "无托盘模式")
	_ = fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取配置失败:", err)
		return 1
	}
	if *port != 0 {
		cfg.APIPort = *port
	}
	if *ngl != 0 {
		cfg.NGL = *ngl
	}
	cfg.Headless = *headless

	fmt.Printf("随译 SuiYi v%s · 推理后端 %s · 模型 %s\n", version, cfg.Backend, config.Resolve(cfg.ModelPath))

	// 启动核心：推理后端 + 翻译队列 + API 服务
	core, err := appcore.Start(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer core.Stop()

	go func() {
		if err := core.Srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "HTTP 服务错误:", err)
		}
	}()

	// 开机自启：按配置应用（Windows 注册表 Run 键）
	if exe, err := os.Executable(); err == nil {
		if cfg.Autostart {
			if err := autostart.Enable(exe); err != nil {
				core.Srv.Log("设置开机自启失败: %v", err)
			} else {
				core.Srv.Log("开机自启已启用")
			}
		} else {
			_ = autostart.Disable()
		}
	}

	// 剪贴板自动翻译：复制即译（去抖/过滤由 clipboard 包处理）
	if cfg.ClipboardEnabled && !cfg.Headless {
		clipCtx, clipCancel := context.WithCancel(context.Background())
		defer clipCancel()
		core.Srv.Log("剪贴板自动翻译已启用（复制即译）")
		go func() {
			clipboard.Watch(clipCtx, func(text string) {
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

	// 系统托盘（非 headless 时）：打开界面 / 退出
	quitCh := make(chan struct{})
	if !cfg.Headless {
		webURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.APIPort)
		go tray.Run(func() { _ = openBrowser(webURL) }, func() { close(quitCh) })
	}

	// 等待退出信号（Ctrl+C / 托盘退出）
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	select {
	case <-ch:
	case <-quitCh:
	}
	fmt.Println("\n正在退出…")
	return 0
}

// openBrowser 打开默认浏览器（Windows）
func openBrowser(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
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
	port := 8848
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "-t":
			target = args[i+1]
		case "-p":
			fmt.Sscanf(args[i+1], "%d", &port)
		}
	}
	body, _ := json.Marshal(map[string]string{"text": text, "target": target})
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/translate", port), "application/json", strings.NewReader(string(body)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "服务未运行，请先启动 suiyi serve：", err)
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