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
	"time"

	"suiyi/internal/api"
	"suiyi/internal/autostart"
	"suiyi/internal/clipboard"
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

	// 翻译队列 worker：串行调用后端（超长文本自动分段 + 滚动前文）
	q := queue.New(func(ctx context.Context, job *queue.Job) {
		switch job.Type {
		case "translate":
			req := job.Args.(*api.TranslateRequest)
			out, err := translateText(ctx, completer, req)
			if err != nil {
				job.Done <- &api.TranslateResult{Ok: false, Text: "", Source: req.Source, Target: req.Target, Error: err.Error()}
				return
			}
			job.Done <- &api.TranslateResult{Ok: true, Text: out, Source: req.Source, Target: req.Target}
		case "translate-stream":
			sj := job.Args.(*api.StreamJob)
			streamer, ok := completer.(engine.StreamCompleter)
			if !ok {
				job.Done <- fmt.Errorf("当前后端不支持流式输出")
				return
			}
			if err := translateStream(sj.Ctx, streamer, sj.Req, sj.Emit, sj.Progress); err != nil {
				job.Done <- err
				return
			}
			job.Done <- nil
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

	// 开机自启：按配置应用（Windows 注册表 Run 键）
	if exe, err := os.Executable(); err == nil {
		if cfg.Autostart {
			if err := autostart.Enable(exe); err != nil {
				srv.Log("设置开机自启失败: %v", err)
			} else {
				srv.Log("开机自启已启用")
			}
		} else {
			_ = autostart.Disable()
		}
	}

	// 剪贴板自动翻译：复制即译（去抖/过滤由 clipboard 包处理）
	if cfg.ClipboardEnabled && !cfg.Headless {
		clipCtx, clipCancel := context.WithCancel(context.Background())
		defer clipCancel()
		srv.Log("剪贴板自动翻译已启用（复制即译）")
		go func() {
			clipboard.Watch(clipCtx, func(text string) {
				srv.Log("剪贴板捕获: %s", truncateRunes(text, 40))
				done := q.Submit(&queue.Job{Type: "translate", Args: &api.TranslateRequest{Text: text, Target: cfg.TargetLang}})
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
				srv.PushClipItem(item)
			})
		}()
	}

	// 等待退出信号
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
	fmt.Println("\n正在退出…")
	return 0
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

// maxSegRunes 单段最大字符数（约等于 token 数），超长文本按此切分
const maxSegRunes = 3000

// chatReq 构造统一翻译请求（README 推荐采样参数）
func chatReq(prompt string) engine.ChatRequest {
	return engine.ChatRequest{
		Model:       "suiyi",
		Messages:    []engine.Msg{{Role: "user", Content: prompt}},
		Temperature: 0.7,
		TopP:        0.6,
		TopK:        20,
		MaxTokens:   4096,
	}
}

// buildSegPrompt 构造分段提示词；携带前文参考以保持术语/语气一致
func buildSegPrompt(seg, source, target, glossary, style, prev string) string {
	prompt := api.BuildPrompt(seg, source, target, glossary, style)
	if prev != "" {
		prompt = "以下是前文的原文与译文，用于保持术语与语气一致，请勿重复输出它们：\n" + prev + "\n\n" + prompt
	}
	return prompt
}

// rollingPrev 生成滚动前文：仅保留最近一段原文与译文的尾部，避免无限增长
func rollingPrev(seg, out string) string {
	return trimTail(seg, 600) + "\n" + trimTail(out, 600) + "\n"
}

func trimTail(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[len(r)-n:])
	}
	return s
}

// translateText 非流式翻译：超长文本自动分段，逐段翻译并拼接（携带滚动前文）
func translateText(ctx context.Context, completer engine.Completer, req *api.TranslateRequest) (string, error) {
	segs := api.SplitSegments(req.Text, maxSegRunes)
	if len(segs) <= 1 {
		return completer.Complete(ctx, chatReq(api.BuildPrompt(req.Text, req.Source, req.Target, req.TermGlossary, req.Style)))
	}
	var full strings.Builder
	var prev string
	for _, seg := range segs {
		out, err := completer.Complete(ctx, chatReq(buildSegPrompt(seg, req.Source, req.Target, req.TermGlossary, req.Style, prev)))
		if err != nil {
			return "", err
		}
		full.WriteString(out)
		prev = rollingPrev(seg, out)
	}
	return full.String(), nil
}

// translateStream 流式翻译：超长文本自动分段，逐段流式输出（携带滚动前文）
// progress 回调上报：pct 0~1、段数、预计剩余秒数、生成速度 token/s
func translateStream(ctx context.Context, streamer engine.StreamCompleter, req *api.TranslateRequest, emit func(string) error, progress func(pct float64, stage string, eta int, tps float64)) error {
	segs := api.SplitSegments(req.Text, maxSegRunes)
	total := 0
	for _, s := range segs {
		total += len([]rune(s))
	}
	if total <= 0 {
		total = 1
	}
	t0 := time.Now()
	processed := 0 // 已完成的输入字符数
	totalTokens := 0
	var prev string
	for i, seg := range segs {
		segRunes := len([]rune(seg))
		stage := fmt.Sprintf("第 %d/%d 段", i+1, len(segs))
		if progress != nil {
			progress(float64(processed)/float64(total), stage, etaSeconds(t0, processed, total), 0)
		}
		segT0 := time.Now()
		var out strings.Builder
		emitted := 0
		var lastProg time.Time
		tokens, err := streamer.CompleteStream(ctx, chatReq(buildSegPrompt(seg, req.Source, req.Target, req.TermGlossary, req.Style, prev)), func(chunk string) error {
			out.WriteString(chunk)
			emitted += len([]rune(chunk))
			// 段内进度：估算本段译文长度 ≈ 原文长度；200ms 节流推送
			if progress != nil && time.Since(lastProg) > 200*time.Millisecond {
				local := float64(emitted) / float64(maxInt(segRunes, 1))
				if local > 0.95 {
					local = 0.95
				}
				doneInput := processed + int(local*float64(segRunes))
				progress((float64(processed)+local*float64(segRunes))/float64(total), stage,
					etaSeconds(t0, doneInput, total), tpsOf(segT0, 0, emitted))
				lastProg = time.Now()
			}
			return emit(chunk)
		})
		if err != nil {
			return err
		}
		totalTokens += tokens
		processed += segRunes
		if progress != nil {
			progress(float64(processed)/float64(total), stage, etaSeconds(t0, processed, total), tpsOf(t0, totalTokens, processed))
		}
		prev = rollingPrev(seg, out.String())
	}
	if progress != nil {
		progress(1, "完成", 0, tpsOf(t0, totalTokens, processed))
	}
	return nil
}

// tpsOf 计算生成速度 token/s：优先用 usage 的 token 数，否则按输出字符估算（CJK≈1 token/字）
func tpsOf(start time.Time, tokens, outRunes int) float64 {
	n := tokens
	if n <= 0 {
		n = outRunes
	}
	secs := time.Since(start).Seconds()
	if secs <= 0 {
		return 0
	}
	return float64(n) / secs
}

// etaSeconds 按已处理输入字符的吞吐估算剩余秒数；未知返回 -1
func etaSeconds(t0 time.Time, processed, total int) int {
	elapsed := time.Since(t0).Seconds()
	if processed <= 0 || elapsed <= 0 {
		return -1
	}
	rate := float64(processed) / elapsed
	remain := float64(total - processed)
	if rate <= 0 {
		return -1
	}
	return int(remain / rate)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}