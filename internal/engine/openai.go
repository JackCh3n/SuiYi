package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Completer 推理后端统一接口：本地 llama.cpp 与 OpenAI 兼容 API 均实现
type Completer interface {
	Complete(ctx context.Context, r ChatRequest) (string, error)
}

// OpenAI OpenAI 兼容 API 客户端（可对接任意 OpenAI 格式服务）
type OpenAI struct {
	baseURL string // 基地址，如 https://api.openai.com/v1
	key     string // Bearer Key（空=不鉴权，适合本地自建服务）
	model   string // 模型名
	client  *http.Client
}

// NewOpenAI 创建 OpenAI 兼容客户端
func NewOpenAI(baseURL, key, model string) *OpenAI {
	return &OpenAI{
		baseURL: strings.TrimRight(baseURL, "/"),
		key:     key,
		model:   model,
		client:  &http.Client{}, // 超时由 Complete 的 context 控制
	}
}

// withModel 远程后端一律用设置里的「模型名 / 服务 ID」。
// 调用方（appcore.ChatReq）填的 "suiyi" 只是本地 llama-server 的别名，
// 直接发给第三方 API 会报「模型或服务 ID suiyi 不存在」（400004）。
func (o *OpenAI) withModel(r ChatRequest) (ChatRequest, error) {
	if strings.TrimSpace(o.model) == "" {
		return r, fmt.Errorf("未配置模型名/服务 ID：请在「设置 → 推理后端 → 模型名」填写（如 hy-mt2-pro）")
	}
	r.Model = o.model
	return r, nil
}

// Complete 通过 OpenAI 兼容 /chat/completions 执行一次生成
func (o *OpenAI) Complete(ctx context.Context, r ChatRequest) (string, error) {
	r, err := o.withModel(r)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, 300*time.Second) // 长文本翻译可能较久
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.key != "" {
		req.Header.Set("Authorization", "Bearer "+o.key)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("OpenAI 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", openaiHTTPError(resp.StatusCode, body)
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
		return "", fmt.Errorf("OpenAI 未返回结果")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// openaiHTTPError 把第三方 API 的错误体整理成人能读的信息：
// 优先取 message_zh / message，取不到就原样返回。
func openaiHTTPError(status int, body []byte) error {
	var e struct {
		Error struct {
			Message   string `json:"message"`
			MessageZh string `json:"message_zh"`
			Code      any    `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err == nil && (e.Error.MessageZh != "" || e.Error.Message != "") {
		msg := e.Error.MessageZh
		if msg == "" {
			msg = e.Error.Message
		}
		if e.Error.Code != nil {
			return fmt.Errorf("OpenAI 错误 %d（%v）: %s", status, e.Error.Code, msg)
		}
		return fmt.Errorf("OpenAI 错误 %d: %s", status, msg)
	}
	return fmt.Errorf("OpenAI 错误 %d: %s", status, strings.TrimSpace(string(body)))
}
