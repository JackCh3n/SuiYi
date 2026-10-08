package engine

// 腾讯混元翻译（translate.hunyuan.tencent.com）App 接口后端。
//
// 来源：对「腾讯混元翻译」App（Hy/1.2.2）抓包还原，见项目 README 与
// 「混元翻译接口文档.md」。关键结论（2026-10-08 实测）：
//
//   - 完全不带凭证 → 401 {"error":{"code":"999","message":"X-ID is empty"}}
//   - POST /api/login/anon {"deviceId":"<uuid>"} → {"userId":"a_…","token":"…","isAnon":true}
//     **匿名账号**，同一 deviceId 重复登录得到同一身份；因此本后端可以零配置直接使用
//   - 翻译接口 POST /api/v1/translate：stream=false 返回 JSON（translated_text），
//     stream=true 返回 OpenAI 风格 SSE（choices[0].delta.content）
//   - 上限 5000 字符/次（本后端按 3000 字自动分段，天然满足）
//   - style 是 9 项严格白名单（传错 400），自由文本风格/术语表改走 user_prompts
//
// 凭证优先级：填了 X-ID + X-Token 就用账号身份（译文进 App 历史）；留空则自动匿名登录。

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// TextCompleter 直接接收「原文 + 语言码 + 术语表/风格」的翻译后端。
// appcore 在调用前判断 Completer 是否实现本接口，实现则走原生字段（不拼提示词）。
type TextCompleter interface {
	TranslateText(ctx context.Context, text, source, target, glossary, style string) (string, error)
	TranslateStream(ctx context.Context, text, source, target, glossary, style string, emit func(string) error) error
}

const (
	hunyuanBase      = "https://translate.hunyuan.tencent.com"
	hunyuanTranslate = hunyuanBase + "/api/v1/translate"
	hunyuanAnonLogin = hunyuanBase + "/api/login/anon"
)

// hunyuanStyles 官方风格白名单（/api/v1/config 的 translation_styles）
var hunyuanStyles = map[string]bool{
	"default": true, "daily_spoken": true, "academic_paper": true, "popular_science": true,
	"business_formal": true, "news_report": true, "promotion_copy": true, "novel": true,
	"legal_contract": true,
}

// hunyuanLangFix 我们的语言码 → 接口语言码（接口用 zh-TW，不认 zh-Hant）
var hunyuanLangFix = map[string]string{"zh-Hant": "zh-TW"}

// Hunyuan 混元翻译客户端
type Hunyuan struct {
	anonMode bool   // 匿名模式：构造时未配置账号凭证（此模式下 401 会自动重登）
	userID   string // 当前生效的 X-ID（账号配置的，或匿名登录得到的 a_…）
	token    string // 当前生效的 X-Token
	deviceID string // 匿名登录用设备号（同一设备固定，保证身份稳定）

	mu      sync.Mutex // 保护凭证（匿名登录/401 重登时更新）
	client  *http.Client
	anonTry int // 匿名重登计数（避免 401 死循环）
}

// NewHunyuan 创建混元后端；userID/token 留空则首次调用时自动匿名登录
func NewHunyuan(userID, token, deviceID string) *Hunyuan {
	if strings.TrimSpace(deviceID) == "" {
		deviceID = newUUID()
	}
	uid, tok := strings.TrimSpace(userID), strings.TrimSpace(token)
	return &Hunyuan{
		anonMode: uid == "" || tok == "",
		userID:   uid,
		token:    tok,
		deviceID: strings.TrimSpace(deviceID),
		client:   &http.Client{},
	}
}

// DeviceID 返回本实例使用的设备号（供上层持久化，保证匿名身份稳定）
func (h *Hunyuan) DeviceID() string { return h.deviceID }

// Anonymous 是否为匿名身份。
// 注意：必须看构造时定下的 anonMode，不能看"当前凭证是否为空"——
// 匿名登录成功后就持有了 a_… 凭证，那样判断会变成"非匿名"，401 时就不会自动重登了。
func (h *Hunyuan) Anonymous() bool { return h.anonMode }

// translateReq 翻译请求体
type translateReq struct {
	SourceText     string `json:"source_text"`
	SourceLanguage string `json:"source_language,omitempty"`
	TargetLanguage string `json:"target_language"`
	InferenceMode  string `json:"inference_mode,omitempty"`
	Stream         bool   `json:"stream"`
	Style          string `json:"style,omitempty"`
	UserPrompts    string `json:"user_prompts,omitempty"`
}

// translateResp 非流式响应
type translateResp struct {
	TranslatedText   string `json:"translated_text"`
	DetectedLanguage struct {
		Code string `json:"code"`
	} `json:"detected_language"`
	IsSensitive bool `json:"is_sensitive"`
}

// hunyuanError 业务错误体 {"code":12000,"message":"…"}
type hunyuanError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// gatewayError 网关错误体 {"error":{"code":"999","message":"…"}}
type gatewayError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// creds 取当前凭证（必要时先匿名登录）
func (h *Hunyuan) creds(ctx context.Context) (string, string, error) {
	h.mu.Lock()
	uid, tok := h.userID, h.token
	h.mu.Unlock()
	if uid != "" && tok != "" {
		return uid, tok, nil
	}
	return h.anonLogin(ctx)
}

// anonLogin 匿名登录换取 userId/token（同一 deviceId 得到同一身份）
func (h *Hunyuan) anonLogin(ctx context.Context) (string, string, error) {
	h.mu.Lock()
	if h.userID != "" && h.token != "" { // 已被其它并发调用拿到
		uid, tok := h.userID, h.token
		h.mu.Unlock()
		return uid, tok, nil
	}
	h.mu.Unlock()

	body, _ := json.Marshal(map[string]string{"deviceId": h.deviceID})
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, hunyuanAnonLogin, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	h.setHeaders(req, "", "")
	resp, err := h.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("混元匿名登录失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("混元匿名登录失败 %d: %s", resp.StatusCode, readableErr(raw))
	}
	var out struct {
		UserID string `json:"userId"`
		Token  string `json:"token"`
		IsAnon bool   `json:"isAnon"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.UserID == "" || out.Token == "" {
		return "", "", fmt.Errorf("混元匿名登录响应异常: %s", strings.TrimSpace(string(raw)))
	}
	h.mu.Lock()
	h.userID, h.token = out.UserID, out.Token
	h.mu.Unlock()
	return out.UserID, out.Token, nil
}

// setHeaders 统一请求头（凭证为空表示匿名登录请求本身）
func (h *Hunyuan) setHeaders(req *http.Request, uid, tok string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OS", "ios")
	req.Header.Set("X-Source", "app")
	req.Header.Set("X-App-Version", "1.2.2")
	req.Header.Set("X-Device-Model", "iPhone13,2")
	req.Header.Set("User-Agent", "Hy/14 CFNetwork/1331.0.7 Darwin/21.4.0")
	if uid != "" {
		req.Header.Set("X-ID", uid)
	}
	if tok != "" {
		req.Header.Set("X-Token", tok)
	}
}

// buildBody 组装请求体：语言码修正、风格白名单、术语/自由风格转 user_prompts
func (h *Hunyuan) buildBody(text, source, target, glossary, style string, stream bool) translateReq {
	if source == "" {
		source = "auto" // 接口支持自动识别
	} else {
		source = fixLang(source)
	}
	if target == "" {
		target = "zh"
	}
	target = fixLang(target)

	r := translateReq{
		SourceText:     text,
		SourceLanguage: source,
		TargetLanguage: target,
		InferenceMode:  "online",
		Stream:         stream,
	}
	// style 只认 9 个白名单 key，传错直接 400：命中的原样转发，自由文本风格改走 user_prompts
	if hunyuanStyles[style] {
		r.Style = style
	} else if s := strings.TrimSpace(style); s != "" {
		r.UserPrompts = "翻译风格要求：" + s
	}
	// 术语表：接口没有术语入参（memory 只读），用 user_prompts 传递参考译法
	if g := strings.TrimSpace(glossary); g != "" {
		if r.UserPrompts != "" {
			r.UserPrompts += "\n"
		}
		r.UserPrompts += "术语对照（请遵循）：\n" + g
	}
	return r
}

// do 发起一次翻译请求；stream=true 时逐块回调 emit，否则返回完整译文
func (h *Hunyuan) do(ctx context.Context, text, source, target, glossary, style string, stream bool, emit func(string) error) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("文本为空")
	}
	uid, tok, err := h.creds(ctx)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(h.buildBody(text, source, target, glossary, style, stream))
	if err != nil {
		return "", err
	}

	cctx, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, hunyuanTranslate, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	h.setHeaders(req, uid, tok)
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("混元翻译请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 && h.anonMode {
		// 匿名凭证失效（服务端未声明匿名 token 有效期，实测会失效）：丢弃后重新匿名登录再试一次
		h.mu.Lock()
		retry := h.anonTry < 2
		h.anonTry++
		h.userID, h.token = "", ""
		h.mu.Unlock()
		if retry {
			log.Printf("[hunyuan] 匿名凭证失效(401)，已重新匿名登录")
			return h.do(ctx, text, source, target, glossary, style, stream, emit)
		}
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("混元翻译错误 %d: %s", resp.StatusCode, readableErr(b))
	}
	h.mu.Lock()
	h.anonTry = 0
	h.mu.Unlock()

	if stream {
		_, err := parseSSE(cctx, resp.Body, emit)
		return "", err
	}
	var out translateResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("混元翻译响应解析失败: %w", err)
	}
	return out.TranslatedText, nil
}

// TranslateText 一次性翻译（stream=false，返回 JSON 的 translated_text）
func (h *Hunyuan) TranslateText(ctx context.Context, text, source, target, glossary, style string) (string, error) {
	return h.do(ctx, text, source, target, glossary, style, false, nil)
}

// TranslateStream 流式翻译（stream=true，OpenAI 风格 SSE）
func (h *Hunyuan) TranslateStream(ctx context.Context, text, source, target, glossary, style string, emit func(string) error) error {
	_, err := h.do(ctx, text, source, target, glossary, style, true, emit)
	return err
}

// Complete 满足通用 Completer 接口（兜底路径；appcore 正常走 TextCompleter）
func (h *Hunyuan) Complete(ctx context.Context, r ChatRequest) (string, error) {
	text := lastUserContent(r)
	if i := strings.Index(text, "\n\n"); i >= 0 {
		text = text[i+2:]
	}
	target := r.TargetLang
	if target == "" {
		target = "zh"
	}
	return h.TranslateText(ctx, text, r.SourceLang, target, "", "")
}

// CompleteStream 满足 StreamCompleter 接口（兜底路径）
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

// readableErr 从两种错误体里取 message，取不到就原样返回
func readableErr(raw []byte) string {
	var g gatewayError
	if err := json.Unmarshal(raw, &g); err == nil && g.Error.Message != "" {
		return fmt.Sprintf("[%s] %s", g.Error.Code, g.Error.Message)
	}
	var b hunyuanError
	if err := json.Unmarshal(raw, &b); err == nil && b.Message != "" {
		return fmt.Sprintf("[%d] %s", b.Code, b.Message)
	}
	return strings.TrimSpace(string(raw))
}

// fixLang 语言码修正（接口用 zh-TW）
func fixLang(code string) string {
	if v, ok := hunyuanLangFix[code]; ok {
		return v
	}
	return code
}

// newUUID 生成标准 UUID v4（deviceId 用，免引入依赖）
func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// 极端情况下退化为时间戳派生的固定值，保证可用
		ts := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(ts >> (uint(i%8) * 8))
		}
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// NewDeviceID 生成一个新的设备号（appcore 用于首次持久化）
func NewDeviceID() string { return newUUID() }
