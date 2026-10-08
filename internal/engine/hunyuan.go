package engine

// 腾讯混元翻译（translate.hunyuan.tencent.com）App 私有接口后端。
//
// 来源：对「腾讯混元翻译」App（Hy/1.2.2）抓包得到的接口，请求只需两个请求头：
//
//	X-ID:    userId（/api/v1/user/info 里的 userId）
//	X-Token: 登录令牌（64 字符）
//
// 响应是 OpenAI 风格 SSE（choices[0].delta.content），因此复用 parseSSE。
//
// ⚠️ 非官方接口，请注意：
//   - 令牌会过期（过期后重新抓包替换即可），且请勿外传；
//   - 接口随时可能变更或加限流，不适合作为唯一依赖；
//   - 不支持术语表/风格（style 只接受固定枚举，传错会 400，故不转发用户的自由文本风格）。
//
// 与本地/OpenAI 后端不同，这是「专用翻译接口」：直接吃原文 + 源/目标语言码，
// 因此 appcore 会优先走 TextCompleter 分支（避免把提示词当正文翻译）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// TextCompleter 直接接收「原文 + 源/目标语言码」的翻译后端。
// appcore 在调用前判断 Completer 是否实现本接口，实现则走原生字段。
type TextCompleter interface {
	TranslateText(ctx context.Context, text, source, target string) (string, error)
	TranslateStream(ctx context.Context, text, source, target string, emit func(string) error) error
}

const hunyuanEndpoint = "https://translate.hunyuan.tencent.com/api/v1/translate"

// Hunyuan 混元翻译 App 接口客户端
type Hunyuan struct {
	userID string // X-ID
	token  string // X-Token
	client *http.Client
}

// NewHunyuan 创建混元翻译后端（userID/token 为空则调用时直接报错）
func NewHunyuan(userID, token string) *Hunyuan {
	return &Hunyuan{
		userID: strings.TrimSpace(userID),
		token:  strings.TrimSpace(token),
		client: &http.Client{},
	}
}

// requestBody 混元翻译请求体
type hunyuanReq struct {
	SourceText     string `json:"source_text"`
	SourceLanguage string `json:"source_language"`
	TargetLanguage string `json:"target_language"`
	InferenceMode  string `json:"inference_mode"`
	Stream         bool   `json:"stream"`
	RecordID       string `json:"record_id"`
}

// call 发起一次翻译请求并消费 SSE；emit 非空时逐块回调，否则累积返回
func (h *Hunyuan) call(ctx context.Context, text, source, target string, emit func(string) error) (string, error) {
	if h.token == "" || h.userID == "" {
		return "", fmt.Errorf("未配置混元翻译凭证：请在「设置 → 推理后端」填写 X-ID 与 X-Token（App 抓包获取）")
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("文本为空")
	}
	if source == "" {
		source = "auto" // 接口支持自动识别
	}
	if target == "" {
		target = "zh"
	}

	body, err := json.Marshal(hunyuanReq{
		SourceText:     text,
		SourceLanguage: source,
		TargetLanguage: target,
		InferenceMode:  "online",
		Stream:         true, // 统一走流式：非流式路径消费完即可
		RecordID:       recordID(),
	})
	if err != nil {
		return "", err
	}

	cctx, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, hunyuanEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-OS", "ios")
	req.Header.Set("X-Source", "app")
	req.Header.Set("X-App-Version", "1.2.2")
	req.Header.Set("X-Device-Model", "iPhone13,2")
	req.Header.Set("User-Agent", "Hy/14 CFNetwork/1331.0.7 Darwin/21.4.0")
	req.Header.Set("X-ID", h.userID)
	req.Header.Set("X-Token", h.token)

	resp, err := h.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("混元翻译请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("混元翻译错误 %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	if emit != nil {
		_, err := parseSSE(cctx, resp.Body, emit)
		return "", err
	}
	var sb strings.Builder
	if _, err := parseSSE(cctx, resp.Body, func(chunk string) error {
		sb.WriteString(chunk)
		return nil
	}); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// TranslateText 一次性翻译（TextCompleter）
func (h *Hunyuan) TranslateText(ctx context.Context, text, source, target string) (string, error) {
	return h.call(ctx, text, source, target, nil)
}

// TranslateStream 流式翻译（TextCompleter）
func (h *Hunyuan) TranslateStream(ctx context.Context, text, source, target string, emit func(string) error) error {
	_, err := h.call(ctx, text, source, target, emit)
	return err
}

// Complete 满足通用 Completer 接口：把提示词里 "\n\n" 之后的原文取出来（appcore 正常走 TextCompleter，
// 这里是兜底路径，仅当调用方自己拼了提示词时使用；分段提示词带前文时不要走这里）。
func (h *Hunyuan) Complete(ctx context.Context, r ChatRequest) (string, error) {
	text := lastUserContent(r)
	if i := strings.Index(text, "\n\n"); i >= 0 {
		text = text[i+2:]
	}
	target := r.TargetLang
	if target == "" {
		target = "zh"
	}
	return h.TranslateText(ctx, text, r.SourceLang, target)
}

// CompleteStream 满足 StreamCompleter 接口（同上，兜底路径）
func (h *Hunyuan) CompleteStream(ctx context.Context, r ChatRequest, emit func(string) error) (int, error) {
	out, err := h.Complete(ctx, r)
	if err != nil {
		return 0, err
	}
	if err := emit(out); err != nil {
		return 0, err
	}
	return len([]rune(out)), nil
}

// recordID 生成 32 位十六进制请求 ID（App 里是 UUID 去掉连字符）
func recordID() string {
	const hex = "0123456789abcdef"
	b := make([]byte, 32)
	for i := range b {
		b[i] = hex[rand.Intn(len(hex))]
	}
	return string(b)
}
