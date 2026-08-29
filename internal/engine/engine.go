// Package engine 管理 llama-server 子进程生命周期与推理调用
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"suiyi/internal/config"
)

// Engine llama-server 子进程管理器
type Engine struct {
	cfg        *Config
	cmd        *exec.Cmd
	mu         sync.Mutex
	started    bool
	logFile    *os.File
	restarting bool
	switching  bool // 正在切换模型：期间禁止 waitHealthy 自动重启

	client      *http.Client // 健康检查客户端（短超时）
	inferClient *http.Client // 推理请求客户端（无固定超时，靠 context 控制）
	ready       chan struct{}
	readyOnce   sync.Once // 每次启动重建，保证就绪通道只被关闭一次
}

// Config 引擎启动参数
type Config struct {
	EnginePort int    // llama-server 端口
	ModelPath  string // 模型文件绝对/相对路径
	EnginePath string // llama-server 可执行文件绝对/相对路径
	SaveMemory bool   // 省内存：模型按需加载（llama.cpp mmap + 低优先级加载）
	NGL        int    // GPU 层数（-ngl），0=纯 CPU；Vulkan 版可调大
	NCTX       int    // 上下文长度（长文本需加大，0 用默认 8192）
}

func New(cfg *Config) *Engine {
	return &Engine{
		cfg:         cfg,
		client:      &http.Client{Timeout: 5 * time.Second},
		inferClient: &http.Client{}, // 超时由 request 的 context 控制
		ready:       make(chan struct{}),
	}
}

func (e *Engine) baseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", e.cfg.EnginePort)
}

// Start 启动 llama-server 子进程并等待就绪
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return nil
	}

	if err := e.locate(); err != nil {
		return err
	}

	nCtx := e.cfg.NCTX
	if nCtx <= 0 {
		nCtx = 8192
	}
	args := []string{
		"--model", e.cfg.ModelPath,
		"--host", "127.0.0.1",
		"--port", fmt.Sprint(e.cfg.EnginePort),
		"-ngl", fmt.Sprint(e.cfg.NGL),
		"-c", fmt.Sprint(nCtx), // 上下文长度：控制 KV cache 占用，长文本需加大
		"--parallel", "1",      // 单槽位：单个请求可用满 n_ctx
		"--jinja",              // 使用模型自带 chat template
	}
	if e.cfg.SaveMemory {
		// 省内存：减小 mmap 预取，加载更慢但更省
		args = append(args, "--mlock", "0")
	}
	e.cmd = exec.Command(e.cfg.EnginePath, args...)

	// 日志落盘 data/engine.log，避免控制台刷屏
	logDir := filepath.Dir(e.logPath())
	_ = os.MkdirAll(logDir, 0o755)
	f, err := os.OpenFile(e.logPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		e.logFile = f
		e.cmd.Stdout = f
		e.cmd.Stderr = f
	}

	if err := e.cmd.Start(); err != nil {
		return fmt.Errorf("启动 llama-server 失败: %w", err)
	}
	e.started = true
	e.readyOnce = sync.Once{}              // 每次启动重置，允许重建就绪通道
	e.ready = make(chan struct{})          // 每次启动重建就绪通道
	go e.reap()                            // 收割进程：进程退出后填充 ProcessState，供退出检测
	go e.waitHealthy(context.Background()) // 后台等待就绪
	return nil
}

// reap 等待子进程退出并填充 ProcessState（供 waitHealthy/SetModel 检测进程退出）
func (e *Engine) reap() {
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Wait()
	}
}

// SetModel 切换本地引擎模型：停旧引擎 → 用新模型启动 → 等待就绪；失败自动回退旧模型
func (e *Engine) SetModel(modelPath string) error {
	newAbs := config.Resolve(modelPath)
	if _, err := os.Stat(newAbs); err != nil {
		return fmt.Errorf("模型文件不存在: %s", modelPath)
	}
	old := e.cfg.ModelPath
	if config.Resolve(old) == newAbs {
		return nil // 模型未变化
	}
	log.Printf("[engine] 切换模型: %s → %s", old, newAbs)
	e.mu.Lock()
	e.switching = true
	e.cfg.ModelPath = modelPath
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.switching = false
		e.mu.Unlock()
	}()

	if err := e.Stop(); err != nil {
		return err
	}
	if err := e.Start(context.Background()); err != nil {
		e.revertModel(old)
		return fmt.Errorf("切换模型失败，已回退: %w", err)
	}
	// 等待就绪：健康 OK 立即返回；进程快速退出（模型加载失败）立即回退
	deadline := time.Now().Add(60 * time.Second)
	for {
		if e.Healthy() {
			log.Printf("[engine] 模型已切换: %s", newAbs)
			return nil
		}
		e.mu.Lock()
		exited := e.started && e.cmd != nil && e.cmd.ProcessState != nil && e.cmd.ProcessState.Exited()
		e.mu.Unlock()
		if exited {
			e.revertModel(old)
			return fmt.Errorf("模型加载失败，已回退旧模型")
		}
		if time.Now().After(deadline) {
			e.revertModel(old)
			return fmt.Errorf("模型加载超时，已回退旧模型")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// revertModel 回退到旧模型并重启引擎
func (e *Engine) revertModel(old string) {
	log.Printf("[engine] 回退模型: %s", old)
	e.cfg.ModelPath = old
	_ = e.Stop()
	_ = e.Start(context.Background())
}

func (e *Engine) logPath() string {
	return filepath.Join(config.ExeDir(), "data", "engine.log")
}

// locate 解析可执行文件与模型绝对路径
func (e *Engine) locate() error {
	eng := config.Resolve(e.cfg.EnginePath)
	if _, err := os.Stat(eng); err != nil {
		return fmt.Errorf("找不到引擎: %s（请配置 internal.config 或放入 third_party/）", eng)
	}
	e.cfg.EnginePath = eng

	model := config.Resolve(e.cfg.ModelPath)
	if _, err := os.Stat(model); err != nil {
		return fmt.Errorf("找不到模型: %s（请下载 gguf 放入 models/）", model)
	}
	e.cfg.ModelPath = model
	return nil
}

// waitHealthy 轮询 /health 直至就绪或失败
func (e *Engine) waitHealthy(ctx context.Context) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	deadline := time.Now().Add(120 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			e.mu.Lock()
			switching := e.switching
			e.mu.Unlock()
			if time.Now().After(deadline) {
				if switching {
					return // 切换中由 SetModel 负责处理
				}
				e.restart("引擎启动超时")
				return
			}
			ok, _ := e.checkHealth()
			if ok {
				e.readyOnce.Do(func() { close(e.ready) }) // 并发 waitHealthy 也只关闭一次
				return
			}
			// 进程意外退出则重启（切换中除外）
			exit := false
			e.mu.Lock()
			if e.started && e.cmd != nil && e.cmd.ProcessState != nil && e.cmd.ProcessState.Exited() {
				exit = true
			}
			e.mu.Unlock()
			if exit {
				if switching {
					return // 切换中由 SetModel 负责处理
				}
				e.restart("引擎进程退出")
				return
			}
		}
	}
}

// Ready 返回就绪通知
func (e *Engine) Ready() <-chan struct{} { return e.ready }

// Healthy 检查引擎是否健康
func (e *Engine) Healthy() bool {
	ok, _ := e.checkHealth()
	return ok
}

func (e *Engine) checkHealth() (bool, error) {
	cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, e.baseURL()+"/health", nil)
	resp, err := e.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200, nil
}

// Complete 通过 OpenAI 兼容接口执行一次生成
func (e *Engine) Complete(ctx context.Context, r ChatRequest) (string, error) {
	body, err := e.request(ctx, "v1/chat/completions", r)
	if err != nil {
		return "", err
	}
	type choice struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	var out struct {
		Choices []choice `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("引擎未返回结果")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

func (e *Engine) request(ctx context.Context, path string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 300*time.Second) // 长文本 CPU 推理可能较久
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodPost, e.baseURL()+"/"+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.inferClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("推理请求失败: %w", err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("推理引擎错误 %d: %s", resp.StatusCode, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Stop 优雅停止引擎
func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return nil
	}
	// window 下先发终止信号
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
		_ = e.cmd.Wait()
	}
	if e.logFile != nil {
		_ = e.logFile.Close()
		e.logFile = nil
	}
	e.started = false
	return nil
}

// restart 重启引擎（崩溃恢复）
func (e *Engine) restart(reason string) {
	log.Printf("[engine] %s，尝试重启", reason)
	e.mu.Lock()
	if e.restarting {
		e.mu.Unlock()
		return
	}
	e.restarting = true
	e.mu.Unlock()

	_ = e.Stop()
	err := e.Start(context.Background())
	e.mu.Lock()
	e.restarting = false
	e.mu.Unlock()
	if err != nil {
		log.Printf("[engine] 重启失败: %v", err)
	}
}

// ChatRequest OpenAI 兼容请求体
type ChatRequest struct {
	Model       string `json:"model"`
	Messages    []Msg  `json:"messages"`
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"top_p,omitempty"`
	TopK        int     `json:"top_k,omitempty"`
	MaxTokens   int     `json:"max_tokens,omitempty"`
	Stream      bool    `json:"stream,omitempty"`
}

// Msg 消息
type Msg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}