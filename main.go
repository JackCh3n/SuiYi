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
	"os/signal"
	"strings"
	"syscall"

	"suiyi/internal/api"
	"suiyi/internal/config"
	"suiyi/internal/engine"
	"suiyi/internal/queue"
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
	cfg.Headless = *headless

	fmt.Printf("随译 SuiYi v%s · 推理后端 %s · 模型 %s\n", version, cfg.Backend, config.Resolve(cfg.ModelPath))

	// 按后端组装推理调用方：本地 llama.cpp 或 OpenAI 兼容 API
	var completer engine.Completer
	if cfg.Backend == "openai" {
		completer = engine.NewOpenAI(cfg.OpenAIBaseURL, cfg.OpenAIKey, cfg.OpenAIModel)
	} else {
		eng := engine.New(&engine.Config{
			EnginePort: cfg.EnginePort,
			ModelPath:  cfg.ModelPath,
			EnginePath: cfg.EnginePath,
			SaveMemory: cfg.SaveMemory,
			NGL:        *ngl,
		})
		if err := eng.Start(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "启动推理引擎失败:", err)
			return 1
		}
		completer = eng
		defer eng.Stop()
	}

	// 翻译队列 worker：串行调用后端
	q := queue.New(func(ctx context.Context, job *queue.Job) {
		switch job.Type {
		case "translate":
			req := job.Args.(*api.TranslateRequest)
			prompt := api.BuildPrompt(req.Text, req.Source, req.Target, req.TermGlossary, req.Style)
			out, err := completer.Complete(ctx, engine.ChatRequest{
				Model: "suiyi",
				Messages: []engine.Msg{
					{Role: "user", Content: prompt},
				},
				Temperature: 0.7, // README 推荐采样参数
				TopP:        0.6,
				TopK:        20,
				MaxTokens:   4096,
			})
			if err != nil {
				job.Done <- &api.TranslateResult{Ok: false, Text: "", Source: req.Source, Target: req.Target, Error: err.Error()}
				return
			}
			job.Done <- &api.TranslateResult{Ok: true, Text: out, Source: req.Source, Target: req.Target}
		default:
			job.Done <- fmt.Errorf("未知任务类型: %s", job.Type)
		}
	})
	defer q.Close()

	srv := api.New(cfg, completer, q)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "HTTP 服务错误:", err)
		}
	}()

	// 等待退出信号
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
	fmt.Println("\n正在退出…")
	return 0
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