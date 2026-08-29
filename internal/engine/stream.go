package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// StreamCompleter 流式推理后端接口：逐 token 回调增量内容，返回总 token 数
type StreamCompleter interface {
	CompleteStream(ctx context.Context, r ChatRequest, emit func(string) error) (tokens int, err error)
}

// _ 编译期断言：本地引擎与 OpenAI 客户端均支持流式
var (
	_ StreamCompleter = (*Engine)(nil)
	_ StreamCompleter = (*OpenAI)(nil)
)

// parseSSE 解析 text/event-stream，逐 data 回调；捕获 usage.total_tokens，遇 [DONE] 或流结束返回
func parseSSE(ctx context.Context, body io.Reader, emit func(string) error) (tokens int, err error) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			return tokens, nil
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // 跳过非内容事件
		}
		if chunk.Usage != nil {
			tokens = chunk.Usage.TotalTokens
		}
		if len(chunk.Choices) > 0 {
			if err := emit(chunk.Choices[0].Delta.Content); err != nil {
				return tokens, err
			}
		}
	}
	return tokens, sc.Err()
}

// CompleteStream 通过 OpenAI 兼容流式接口生成（本地 llama-server）
func (e *Engine) CompleteStream(ctx context.Context, r ChatRequest, emit func(string) error) (int, error) {
	r.Stream = true
	raw, err := json.Marshal(r)
	if err != nil {
		return 0, err
	}
	cctx, cancel := context.WithTimeout(ctx, 300*time.Second) // 长文本 CPU 推理可能较久
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, e.baseURL()+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := e.inferClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("推理请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("推理引擎错误 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseSSE(ctx, resp.Body, emit)
}

// CompleteStream 通过 OpenAI 兼容流式接口生成（远程 API）
func (o *OpenAI) CompleteStream(ctx context.Context, r ChatRequest, emit func(string) error) (int, error) {
	r.Stream = true
	if r.Model == "" {
		r.Model = o.model
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return 0, err
	}
	cctx, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if o.key != "" {
		req.Header.Set("Authorization", "Bearer "+o.key)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("OpenAI 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("OpenAI 错误 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseSSE(ctx, resp.Body, emit)
}
