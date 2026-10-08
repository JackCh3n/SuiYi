// Package appcore 聚合「推理后端 + 翻译队列 + API 服务」，供 CLI（main.go）与桌面 GUI 复用
package appcore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"suiyi/internal/api"
	"suiyi/internal/config"
	"suiyi/internal/engine"
	"suiyi/internal/queue"
)

// Core 应用核心：推理后端 + 串行翻译队列 + HTTP API 服务
type Core struct {
	cfg       *config.Config
	Completer engine.Completer // 当前推理后端
	Eng       *engine.Engine   // 本地 llama 引擎（仅 local 后端非空）
	Q         *queue.Queue
	Srv       *api.Server
}

// Start 启动核心：按配置组装推理后端、翻译队列与 API 服务
func Start(cfg *config.Config) (*Core, error) {
	c := &Core{cfg: cfg}

	if cfg.Backend == "openai" {
		c.Completer = engine.NewOpenAI(cfg.OpenAIBaseURL, cfg.OpenAIKey, cfg.OpenAIModel)
	} else if cfg.Backend == "hunyuan" {
		// 腾讯混元翻译 App 私有接口（免费，需 X-ID / X-Token）
		c.Completer = engine.NewHunyuan(cfg.HunyuanUserID, cfg.HunyuanToken)
	} else if cfg.Backend == "hymt" {
		// hy-mt-rs CLI 后端：支持 AngelSlim 1.25bit（STQ1_0）/ 2bit（SEQ）GGUF
		c.Completer = engine.NewHyMT(config.Resolve(cfg.ModelPath), hyMTBinPath(cfg))
	} else {
		eng := engine.New(&engine.Config{
			EnginePort: cfg.EnginePort,
			ModelPath:  cfg.ModelPath,
			EnginePath: cfg.EnginePath,
			SaveMemory: cfg.SaveMemory,
			NGL:        cfg.NGL,
			NCTX:       cfg.NCTX,
		})
		if err := eng.Start(context.Background()); err != nil {
			return nil, fmt.Errorf("启动推理引擎失败: %w", err)
		}
		c.Eng = eng
		c.Completer = eng
	}

	// 翻译队列 worker：串行调用后端（超长文本自动分段 + 滚动前文）
	c.Q = queue.New(func(ctx context.Context, job *queue.Job) {
		switch job.Type {
		case "translate":
			req := job.Args.(*api.TranslateRequest)
			out, err := TranslateText(ctx, c.Completer, req)
			if err != nil {
				job.Done <- &api.TranslateResult{Ok: false, Text: "", Source: req.Source, Target: req.Target, Error: err.Error()}
				return
			}
			job.Done <- &api.TranslateResult{Ok: true, Text: out, Source: req.Source, Target: req.Target}
		case "translate-stream":
			sj := job.Args.(*api.StreamJob)
			streamer, ok := c.Completer.(engine.StreamCompleter)
			if !ok {
				job.Done <- fmt.Errorf("当前后端不支持流式输出")
				return
			}
			if err := TranslateStream(sj.Ctx, streamer, sj.Req, sj.Emit, sj.Progress); err != nil {
				job.Done <- err
				return
			}
			job.Done <- nil
		default:
			job.Done <- fmt.Errorf("未知任务类型: %s", job.Type)
		}
	})

	c.Srv = api.New(cfg, c.Completer, c.Q)
	c.Srv.SetSwitchFn(c.SwitchModel)
	return c, nil
}

// translateTextNative 结构化翻译后端的一次性翻译：超长文本按段切分逐段翻译（无提示词/前文机制）
func translateTextNative(ctx context.Context, tc engine.TextCompleter, req *api.TranslateRequest) (string, error) {
	segs := api.SplitSegments(req.Text, maxSegRunes)
	if len(segs) <= 1 {
		return tc.TranslateText(ctx, req.Text, req.Source, req.Target)
	}
	var full strings.Builder
	for _, seg := range segs {
		out, err := tc.TranslateText(ctx, seg, req.Source, req.Target)
		if err != nil {
			return "", err
		}
		full.WriteString(out)
	}
	return full.String(), nil
}

// translateStreamNative 结构化翻译后端的流式翻译（分段 + 进度上报，与提示词路径一致）
func translateStreamNative(ctx context.Context, tc engine.TextCompleter, req *api.TranslateRequest, emit func(string) error, progress func(pct float64, stage string, eta int, tps float64)) error {
	segs := api.SplitSegments(req.Text, maxSegRunes)
	total := 0
	for _, s := range segs {
		total += len([]rune(s))
	}
	if total <= 0 {
		total = 1
	}
	t0 := time.Now()
	processed := 0
	for i, seg := range segs {
		segRunes := len([]rune(seg))
		stage := fmt.Sprintf("第 %d/%d 段", i+1, len(segs))
		if progress != nil {
			progress(float64(processed)/float64(total), stage, etaSeconds(t0, processed, total), 0)
		}
		emitted := 0
		var lastProg time.Time
		if err := tc.TranslateStream(ctx, seg, req.Source, req.Target, func(chunk string) error {
			emitted += len([]rune(chunk))
			if progress != nil && time.Since(lastProg) > 200*time.Millisecond {
				local := float64(emitted) / float64(maxInt(segRunes, 1))
				if local > 0.95 {
					local = 0.95
				}
				progress((float64(processed)+local*float64(segRunes))/float64(total), stage,
					etaSeconds(t0, processed+int(local*float64(segRunes)), total), 0)
				lastProg = time.Now()
			}
			return emit(chunk)
		}); err != nil {
			return err
		}
		processed += segRunes
		if progress != nil {
			progress(float64(processed)/float64(total), stage, etaSeconds(t0, processed, total), 0)
		}
	}
	if progress != nil {
		progress(1, "完成", 0, 0)
	}
	return nil
}

// needsHyMT 判断模型是否必须走 hy-mt 后端（AngelSlim 私有量化：2bit SEQ / 1.25bit STQ，
// llama.cpp 无法加载 type 41/43）
func needsHyMT(modelAbs string) bool {
	b := strings.ToLower(filepath.Base(modelAbs))
	return strings.Contains(b, "2bit") || strings.Contains(b, "1.25bit")
}

// hyMTBinPath 定位 hy-mt.exe：优先 third_party/windows/amd64/hymt/，其次用户配置的 engine_path
func hyMTBinPath(cfg *config.Config) string {
	p := config.Resolve("third_party/windows/amd64/hymt/hy-mt.exe")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return config.Resolve(cfg.EnginePath)
}

// llamaBinPath 定位 llama-server.exe：优先 third_party/windows/amd64/llama-server.exe
func llamaBinPath(cfg *config.Config) string {
	p := config.Resolve("third_party/windows/amd64/llama-server.exe")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return config.Resolve(cfg.EnginePath)
}

// SwitchModel 按模型类型自动切换推理后端：
//   - 2bit / 1.25bit（AngelSlim 私有量化）→ hy-mt 后端（子进程 CLI）
//   - 其余（Q4_K_M 等标准量化）→ 本地 llama-server 后端
//
// 成功返回新后端信息（供 API 层同步状态）；失败时保持原后端不变。
func (c *Core) SwitchModel(modelPath string) (*api.BackendSwitch, error) {
	newAbs := config.Resolve(modelPath)
	if _, err := os.Stat(newAbs); err != nil {
		return nil, fmt.Errorf("模型文件不存在: %s", modelPath)
	}
	if needsHyMT(newAbs) {
		bin := hyMTBinPath(c.cfg)
		if _, err := os.Stat(bin); err != nil {
			return nil, fmt.Errorf("找不到 hy-mt 引擎: %s（请放入 third_party/windows/amd64/hymt/ 或配置 engine_path）", bin)
		}
		if c.Eng != nil {
			_ = c.Eng.Stop()
		}
		h := engine.NewHyMT(newAbs, bin)
		c.Eng = nil
		c.Completer = h
		return &api.BackendSwitch{Backend: "hymt", Completer: h}, nil
	}
	// 先停旧引擎再启新：新旧 llama-server 用同一个 EnginePort，
	// 先启后停会让新进程绑定失败（旧进程还占着端口）
	if c.Eng != nil {
		_ = c.Eng.Stop()
	}
	eng := engine.New(&engine.Config{
		EnginePort: c.cfg.EnginePort,
		ModelPath:  modelPath,
		EnginePath: llamaBinPath(c.cfg),
		SaveMemory: c.cfg.SaveMemory,
		NGL:        c.cfg.NGL,
		NCTX:       c.cfg.NCTX,
	})
	if err := eng.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("启动引擎失败: %w", err)
	}
	c.Eng = eng
	c.Completer = eng
	return &api.BackendSwitch{Backend: "local", Completer: eng, Eng: eng}, nil
}

// Stop 停止核心：先关队列，再停本地引擎
func (c *Core) Stop() {
	if c.Q != nil {
		c.Q.Close()
	}
	if c.Eng != nil {
		c.Eng.Stop()
	}
}

// maxSegRunes 单段最大字符数（约等于 token 数），超长文本按此切分
const maxSegRunes = 3000

// ChatReq 构造统一翻译请求（README 推荐采样参数）
func ChatReq(prompt, source, target string) engine.ChatRequest {
	return engine.ChatRequest{
		Model:       "suiyi",
		Messages:    []engine.Msg{{Role: "user", Content: prompt}},
		SourceLang:  source,
		TargetLang:  target,
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

// TranslateText 非流式翻译：超长文本自动分段，逐段翻译并拼接（携带滚动前文）
func TranslateText(ctx context.Context, completer engine.Completer, req *api.TranslateRequest) (string, error) {
	// 结构化翻译后端（如混元 App 接口）：直接给原文与语言码，不走提示词
	if tc, ok := completer.(engine.TextCompleter); ok {
		return translateTextNative(ctx, tc, req)
	}
	segs := api.SplitSegments(req.Text, maxSegRunes)
	if len(segs) <= 1 {
		return completer.Complete(ctx, ChatReq(api.BuildPrompt(req.Text, req.Source, req.Target, req.TermGlossary, req.Style), req.Source, req.Target))
	}
	var full strings.Builder
	var prev string
	for _, seg := range segs {
		out, err := completer.Complete(ctx, ChatReq(buildSegPrompt(seg, req.Source, req.Target, req.TermGlossary, req.Style, prev), req.Source, req.Target))
		if err != nil {
			return "", err
		}
		full.WriteString(out)
		prev = rollingPrev(seg, out)
	}
	return full.String(), nil
}

// TranslateStream 流式翻译：超长文本自动分段，逐段流式输出（携带滚动前文）
// progress 回调上报：pct 0~1、段数、预计剩余秒数、生成速度 token/s
func TranslateStream(ctx context.Context, streamer engine.StreamCompleter, req *api.TranslateRequest, emit func(string) error, progress func(pct float64, stage string, eta int, tps float64)) error {
	// 结构化翻译后端（如混元 App 接口）：直接给原文与语言码，不走提示词
	if tc, ok := streamer.(engine.TextCompleter); ok {
		return translateStreamNative(ctx, tc, req, emit, progress)
	}
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
		tokens, err := streamer.CompleteStream(ctx, ChatReq(buildSegPrompt(seg, req.Source, req.Target, req.TermGlossary, req.Style, prev), req.Source, req.Target), func(chunk string) error {
			out.WriteString(chunk)
			emitted += len([]rune(chunk))
			// 段内进度：估算本段译文长度 ≈ 原文长度；200ms 节流推送
			if progress != nil && time.Since(lastProg) > 200*time.Millisecond {
				local := float64(emitted) / float64(maxInt(segRunes, 1))
				if local > 0.95 {
					local = 0.95
				}
				progress((float64(processed)+local*float64(segRunes))/float64(total), stage,
					etaSeconds(t0, processed+int(local*float64(segRunes)), total), tpsOf(segT0, 0, emitted))
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
