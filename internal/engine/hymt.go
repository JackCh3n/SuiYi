package engine

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// HyMT hy-mt-rs CLI 后端：支持 AngelSlim 官方 1.25bit（STQ1_0，type 40/42）GGUF。
//
// hy-mt 为无状态 CLI，每次请求启动子进程加载模型并推理（GGUF mmap 加载约 0.3s），
// 使用 --instruction "{text}" 直接复用本应用的完整翻译提示词（术语/风格/分段前文）。
type HyMT struct {
	ModelPath string // 1.25bit GGUF 绝对路径
	BinPath   string // hy-mt.exe 绝对路径

	mu sync.Mutex // 串行化（单模型，避免并发进程争用）
}

// NewHyMT 创建 hy-mt 后端
func NewHyMT(modelPath, binPath string) *HyMT {
	return &HyMT{ModelPath: modelPath, BinPath: binPath}
}

// lastUserContent 取最后一条 user 消息内容（即完整翻译提示词）
func lastUserContent(r ChatRequest) string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == "user" {
			return r.Messages[i].Content
		}
	}
	return ""
}

// Complete 调用 hy-mt CLI 完成一次翻译（串行）
func (h *HyMT) Complete(ctx context.Context, r ChatRequest) (string, error) {
	text := lastUserContent(r)
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("无用户消息")
	}
	if r.MaxTokens <= 0 {
		r.MaxTokens = 1024
	}
	args := []string{
		"translate",
		"--model", h.ModelPath,
		"--tgt", "Chinese", // --instruction 已含目标语言说明，此处仅作占位
		"--instruction", "{text}",
		"--prompt", text,
		"--device", "cpu",
		"--temperature", fmt.Sprint(r.Temperature),
		"--max-new-tokens", fmt.Sprint(r.MaxTokens),
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	var out, errBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, h.BinPath, args...)
	hideConsole(cmd) // 后台运行：每次推理都不弹控制台窗口
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("hy-mt 推理失败: %v: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// CompleteStream 伪流式：hy-mt CLI 一次性输出，把完整译文作为单个 delta 发出
// （token 数按输出字符估算，CJK≈1 token/字）
func (h *HyMT) CompleteStream(ctx context.Context, r ChatRequest, emit func(string) error) (int, error) {
	text, err := h.Complete(ctx, r)
	if err != nil {
		return 0, err
	}
	if err := emit(text); err != nil {
		return 0, err
	}
	return len([]rune(text)), nil
}

var _ Completer = (*HyMT)(nil)
var _ StreamCompleter = (*HyMT)(nil)
