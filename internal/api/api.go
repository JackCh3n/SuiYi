// Package api REST API + Web 管理界面
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"suiyi/internal/config"
	"suiyi/internal/engine"
	"suiyi/internal/queue"
	"suiyi/web"
)

// Server HTTP 服务
type Server struct {
	cfg       *config.Config
	completer engine.Completer // 当前推理后端
	eng       *engine.Engine   // 本地 llama 引擎（仅 local 后端非空）
	q         *queue.Queue
	mu        sync.Mutex
	logs      []string // 环形日志
	logMax    int

	switchFn func(modelPath string) (*BackendSwitch, error) // 运行时切换后端（appcore 注入）
}

// BackendSwitch 描述一次运行时后端切换的结果
type BackendSwitch struct {
	Backend   string           // 新后端：local / hymt / openai
	Completer engine.Completer // 新推理后端（供翻译队列使用）
	Eng       *engine.Engine   // 本地 llama 引擎（local 后端非空）
}

// SetSwitchFn 注入模型切换回调（由 appcore 提供，实现按模型类型自动路由后端）
func (s *Server) SetSwitchFn(fn func(modelPath string) (*BackendSwitch, error)) {
	s.switchFn = fn
}

// New 创建 API 服务
func New(cfg *config.Config, completer engine.Completer, q *queue.Queue) *Server {
	eng, _ := completer.(*engine.Engine)
	return &Server{cfg: cfg, completer: completer, eng: eng, q: q, logMax: 500}
}

// addLog 记一条日志（供 /logs 与控制台）
func (s *Server) addLog(format string, args ...any) {
	line := fmt.Sprintf("%s %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	log.Println(line)
	s.mu.Lock()
	s.logs = append(s.logs, line)
	if len(s.logs) > s.logMax {
		s.logs = s.logs[len(s.logs)-s.logMax:]
	}
	s.mu.Unlock()
}

// Log 记录一条应用日志（供外部模块调用）
func (s *Server) Log(format string, args ...any) { s.addLog(format, args...) }

// Handler 返回路由
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/translate", s.handleTranslate)
	mux.HandleFunc("/translate/stream", s.handleTranslateStream)
	mux.HandleFunc("/languages", s.handleLanguages)
	mux.HandleFunc("/models", s.handleModels)
	mux.HandleFunc("/config", s.handleConfig)
	mux.HandleFunc("/logs", s.handleLogs)
	mux.HandleFunc("/", s.handleWeb)
	// 配置了 token 时：除 Web 界面外的 API 需要鉴权（同源放行 / Bearer / ?token=）
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authOK(r) {
			writeJSON(w, 401, map[string]string{"error": "未授权：需要 token"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// authOK 鉴权：未配置 token 直接放行；配置后允许同源（Web 管理界面）、Bearer token 或 ?token=
func (s *Server) authOK(r *http.Request) bool {
	if s.cfg.Token == "" {
		return true
	}
	if r.URL.Path == "/" {
		return true // Web 界面本身
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		if strings.HasPrefix(origin, "http://127.0.0.1:") || strings.HasPrefix(origin, "http://localhost:") {
			return true
		}
	}
	if r.Header.Get("Authorization") == "Bearer "+s.cfg.Token {
		return true
	}
	return r.URL.Query().Get("token") == s.cfg.Token
}

// Listen 监听并服务
func (s *Server) ListenAndServe() error {
	addr := fmt.Sprintf("127.0.0.1:%d", s.cfg.APIPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.addLog("管理界面: http://%s", addr)
	return http.Serve(ln, s.Handler())
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// ---- 各接口 ----

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"status":  "ok",
		"backend": s.cfg.Backend,
		"pending": s.q.Pending(),
	}
	if s.cfg.Backend == "openai" {
		// OpenAI 兼容后端：有配置即视为可用
		resp["engine"] = s.cfg.OpenAIBaseURL != ""
	} else if s.cfg.Backend == "hymt" {
		// hy-mt CLI 后端：可执行文件与模型存在即视为可用（无常驻进程）
		_, statBin := os.Stat(config.Resolve(s.cfg.EnginePath))
		_, statModel := os.Stat(config.Resolve(s.cfg.ModelPath))
		resp["engine"] = statBin == nil && statModel == nil
	} else {
		engineOK := false
		// 就绪通道一旦关闭不会重开，进程被杀后仍是关闭状态：必须先确认进程还活着
		if s.eng != nil && s.eng.Running() {
			select {
			case <-s.eng.Ready():
				engineOK = true
			default:
				engineOK = s.eng.Healthy()
			}
		}
		resp["engine"] = engineOK
	}
	writeJSON(w, 200, resp)
}

// TranslateRequest 单句翻译请求
type TranslateRequest struct {
	Text         string `json:"text"`
	Source       string `json:"source,omitempty"`   // 语言代码，空=自动
	Target       string `json:"target,omitempty"`   // 目标语言代码，空=配置默认
	TermGlossary string `json:"glossary,omitempty"` // 可选：术语约定
	Style        string `json:"style,omitempty"`    // 可选：风格
}

// TranslateResult 结果
type TranslateResult struct {
	Ok     bool   `json:"ok"`
	Text   string `json:"text"`   // 译文
	Source string `json:"source"` // 实际使用源语言
	Target string `json:"target"` // 实际使用目标语言
	Error  string `json:"error,omitempty"`
}

func (s *Server) handleTranslate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "仅支持 POST"})
		return
	}
	var req TranslateRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "请求体无效: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeJSON(w, 400, map[string]string{"error": "text 不能为空"})
		return
	}
	target := req.Target
	if target == "" {
		target = s.cfg.TargetLang
	}

	done := s.q.Submit(&queue.Job{
		Type: "translate",
		Args: &TranslateRequest{Text: req.Text, Source: req.Source, Target: target, TermGlossary: req.TermGlossary, Style: req.Style},
	})

	select {
	case res := <-done:
		if err, ok := res.(error); ok {
			s.addLog("翻译失败: %v", err)
			writeJSON(w, 500, TranslateResult{Ok: false, Text: "", Target: target, Error: err.Error()})
			return
		}
		// 结果由 worker 写入
		if tr, ok := res.(*TranslateResult); ok {
			s.addLog("翻译: %q → %q (%s→%s)", truncate(req.Text, 30), truncate(tr.Text, 30), tr.Source, tr.Target)
			writeJSON(w, 200, tr)
			return
		}
		writeJSON(w, 200, res)
	case <-r.Context().Done():
		writeJSON(w, 408, map[string]string{"error": "请求超时"})
	}
}

// StreamJob 流式翻译任务（由队列 worker 调用后端流式生成，逐块写回）
type StreamJob struct {
	Ctx      context.Context                                       // 可取消上下文：客户端断开/点击停止时取消，从而中止生成
	Req      *TranslateRequest                                     // 请求
	Emit     func(string) error                                    // 每段增量内容回调；返回错误则中止
	Progress func(pct float64, stage string, eta int, tps float64) // 进度回调：pct 0~1，eta 预计剩余秒（-1=未知），tps 生成速度
}

// writeSSE 输出一条 SSE 事件
func writeSSE(w http.ResponseWriter, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", raw)
	return err
}

// handleTranslateStream 流式单句翻译（text/event-stream，逐段增量返回）
// 安全设计：worker 只向事件通道发送事件，仅本 handler goroutine 写 ResponseWriter；
// 客户端断开/刷新/点击停止时取消流式上下文，worker 随即停止，彻底避免进程崩溃。
func (s *Server) handleTranslateStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "仅支持 POST"})
		return
	}
	var req TranslateRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "请求体无效: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeJSON(w, 400, map[string]string{"error": "text 不能为空"})
		return
	}
	target := req.Target
	if target == "" {
		target = s.cfg.TargetLang
	}
	req.Target = target

	// 流式上下文随客户端请求取消（断开/刷新/停止）而取消
	streamCtx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// 事件通道：worker 生产，本 goroutine 消费并写出（唯一写 w 的 goroutine）
	events := make(chan map[string]any, 32)
	emit := func(chunk string) error {
		select {
		case events <- map[string]any{"delta": chunk}:
			return nil
		case <-streamCtx.Done():
			return streamCtx.Err()
		}
	}
	progress := func(pct float64, stage string, eta int, tps float64) {
		select {
		case events <- map[string]any{"progress": pct, "stage": stage, "eta": eta, "tps": tps}:
		case <-streamCtx.Done():
		}
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	_ = writeSSE(w, map[string]any{"start": true, "source": req.Source, "target": target})
	if flusher != nil {
		flusher.Flush()
	}

	done := s.q.Submit(&queue.Job{Type: "translate-stream", Args: &StreamJob{Ctx: streamCtx, Req: &req, Emit: emit, Progress: progress}})

	// 事件泵：仅在此处写 ResponseWriter
	for {
		select {
		case ev := <-events:
			if err := writeSSE(w, ev); err != nil {
				cancel()
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		case res := <-done:
			if r.Context().Err() != nil {
				return // 客户端已断开，不回写
			}
			// 先清空尚未写出的缓冲事件，再写结束事件
			for {
				select {
				case ev := <-events:
					_ = writeSSE(w, ev)
					if flusher != nil {
						flusher.Flush()
					}
				default:
					goto finish
				}
			}
		finish:
			if err, ok := res.(error); ok {
				s.addLog("流式翻译失败: %v", err)
				_ = writeSSE(w, map[string]any{"error": err.Error()})
			} else {
				s.addLog("流式翻译: %q (%s→%s)", truncate(req.Text, 30), req.Source, target)
				_ = writeSSE(w, map[string]any{"done": true, "source": req.Source, "target": target})
			}
			if flusher != nil {
				flusher.Flush()
			}
			return
		case <-r.Context().Done():
			cancel() // 通知 worker 停止生成
			return
		}
	}
}

func (s *Server) handleLanguages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, Languages)
}

// handleModels 列出 models 目录下的 GGUF 模型文件（供 Web 下拉选择/刷新）
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"models": listModels()})
}

// listModels 扫描「工作目录 / 可执行文件目录」下的 models/ 目录，返回 .gguf 列表
func listModels() []map[string]string {
	seen := map[string]bool{}
	var out []map[string]string
	add := func(dir string) {
		if dir == "" {
			return
		}
		matches, err := filepath.Glob(filepath.Join(dir, "*.gguf"))
		if err != nil {
			return
		}
		for _, m := range matches {
			rel := filepath.ToSlash(filepath.Join("models", filepath.Base(m)))
			if seen[rel] {
				continue
			}
			seen[rel] = true
			out = append(out, map[string]string{"name": filepath.Base(m), "path": rel})
		}
	}
	if wd, err := os.Getwd(); err == nil {
		add(filepath.Join(wd, "models"))
	}
	add(filepath.Join(config.ExeDir(), "models"))
	return out
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, s.cfg)
	case http.MethodPut:
		var patch struct {
			APIPort       *int    `json:"api_port"`
			EnginePort    *int    `json:"engine_port"`
			ModelPath     *string `json:"model_path"`
			EnginePath    *string `json:"engine_path"`
			TargetLang    *string `json:"target_lang"`
			SaveMemory    *bool   `json:"save_memory"`
			Autostart     *bool   `json:"autostart"`
			NCTX          *int    `json:"n_ctx"`
			NGL           *int    `json:"ngl"`
			Token         *string `json:"token"`
			Backend       *string `json:"backend"`
			OpenAIBaseURL *string `json:"openai_base_url"`
			OpenAIKey     *string `json:"openai_key"`
			OpenAIModel   *string `json:"openai_model"`
		}
		if err := readJSON(r, &patch); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		oldModel := s.cfg.ModelPath
		modelChanged := patch.ModelPath != nil && *patch.ModelPath != oldModel
		if patch.APIPort != nil {
			s.cfg.APIPort = *patch.APIPort
		}
		if patch.EnginePort != nil {
			s.cfg.EnginePort = *patch.EnginePort
		}
		if patch.ModelPath != nil {
			s.cfg.ModelPath = *patch.ModelPath
		}
		if patch.EnginePath != nil {
			s.cfg.EnginePath = *patch.EnginePath
		}
		if patch.TargetLang != nil {
			s.cfg.TargetLang = *patch.TargetLang
		}
		if patch.SaveMemory != nil {
			s.cfg.SaveMemory = *patch.SaveMemory
		}
		if patch.Autostart != nil {
			s.cfg.Autostart = *patch.Autostart
		}
		if patch.NCTX != nil {
			s.cfg.NCTX = *patch.NCTX
		}
		if patch.NGL != nil {
			s.cfg.NGL = *patch.NGL
		}
		if patch.Token != nil {
			s.cfg.Token = *patch.Token
		}
		if patch.Backend != nil {
			s.cfg.Backend = *patch.Backend
		}
		if patch.OpenAIBaseURL != nil {
			s.cfg.OpenAIBaseURL = *patch.OpenAIBaseURL
		}
		if patch.OpenAIKey != nil {
			s.cfg.OpenAIKey = *patch.OpenAIKey
		}
		if patch.OpenAIModel != nil {
			s.cfg.OpenAIModel = *patch.OpenAIModel
		}
		if err := s.cfg.Save(); err != nil {
			writeJSON(w, 500, map[string]string{"error": "保存配置失败: " + err.Error()})
			return
		}
		s.addLog("配置已更新")
		// 本地/hymt 后端：模型路径变化时按模型类型自动切换后端（失败自动回退）
		if modelChanged && (s.cfg.Backend == "local" || s.cfg.Backend == "hymt") && s.switchFn != nil {
			s.addLog("正在切换模型: %s", s.cfg.ModelPath)
			bs, err := s.switchFn(s.cfg.ModelPath)
			if err != nil {
				s.cfg.ModelPath = oldModel
				_ = s.cfg.Save()
				s.addLog("模型切换失败，已回退: %v", err)
				writeJSON(w, 200, map[string]any{"ok": false, "error": "模型切换失败，已回退旧模型", "config": s.cfg})
				return
			}
			s.eng = bs.Eng
			s.completer = bs.Completer
			s.cfg.Backend = bs.Backend
			_ = s.cfg.Save()
			s.addLog("模型已切换(%s): %s", bs.Backend, s.cfg.ModelPath)
		}
		writeJSON(w, 200, s.cfg)
	default:
		writeJSON(w, 405, map[string]string{"error": "仅支持 GET/PUT"})
	}
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	logs := append([]string{}, s.logs...)
	s.mu.Unlock()
	// 追加本地 llama-server 引擎日志尾部（OpenAI 后端无此文件则忽略）
	engineLog := tailFile(filepath.Join(config.ExeDir(), "data", "engine.log"), 200)
	writeJSON(w, 200, map[string]any{"logs": logs, "engine": engineLog})
}

// tailFile 读取文件末尾最多 n 行
func tailFile(path string, n int) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// handleWeb 内嵌管理界面
func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/":
		content, _ := web.IndexHTML()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(content)
		return
	case "/favicon-16.png", "/favicon-32.png":
		if b, ok := web.Favicon(strings.TrimPrefix(r.URL.Path, "/")); ok {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(b)
			return
		}
	}
	http.NotFound(w, r)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}
