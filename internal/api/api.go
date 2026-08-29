// Package api REST API + Web 管理界面
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
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

// Handler 返回路由
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/translate", s.handleTranslate)
	mux.HandleFunc("/batch", s.handleBatch)
	mux.HandleFunc("/languages", s.handleLanguages)
	mux.HandleFunc("/config", s.handleConfig)
	mux.HandleFunc("/logs", s.handleLogs)
	mux.HandleFunc("/", s.handleWeb)
	return mux
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
	} else {
		engineOK := false
		if s.eng != nil {
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
	Text     string `json:"text"`
	Source   string `json:"source,omitempty"` // 语言代码，空=自动
	Target   string `json:"target,omitempty"` // 目标语言代码，空=配置默认
	TermGlossary string `json:"glossary,omitempty"` // 可选：术语约定
	Style    string `json:"style,omitempty"`  // 可选：风格
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

// BatchRequest 批量翻译请求
type BatchRequest struct {
	Items  []TranslateRequest `json:"items"`
	Source string             `json:"source,omitempty"`
	Target string             `json:"target,omitempty"`
}

func (s *Server) handleBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "仅支持 POST"})
		return
	}
	var req BatchRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "请求体无效: " + err.Error()})
		return
	}
	if len(req.Items) == 0 {
		writeJSON(w, 400, map[string]string{"error": "items 不能为空"})
		return
	}

	results := make([]TranslateResult, len(req.Items))
	for i := range req.Items {
		if req.Items[i].Target == "" {
			req.Items[i].Target = req.Target
		}
		if req.Items[i].Source == "" {
			req.Items[i].Source = req.Source
		}
		if req.Items[i].Target == "" {
			req.Items[i].Target = s.cfg.TargetLang
		}
		// 逐条入队，保持顺序
		id := fmt.Sprintf("batch-%d-%d", time.Now().UnixNano(), i)
		done := s.q.Submit(&queue.Job{ID: id, Type: "translate", Args: &req.Items[i]})
		select {
		case res := <-done:
			switch v := res.(type) {
			case *TranslateResult:
				results[i] = *v
			case error:
				results[i] = TranslateResult{Ok: false, Text: "", Target: req.Items[i].Target, Error: v.Error()}
			}
		case <-r.Context().Done():
			results[i] = TranslateResult{Ok: false, Text: "", Target: req.Items[i].Target, Error: "超时"}
		}
	}
	writeJSON(w, 200, map[string]any{"items": results})
}

func (s *Server) handleLanguages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, Languages)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, s.cfg)
	case http.MethodPut:
		var patch struct {
			APIPort          *int    `json:"api_port"`
			EnginePort       *int    `json:"engine_port"`
			ModelPath        *string `json:"model_path"`
			EnginePath       *string `json:"engine_path"`
			TargetLang       *string `json:"target_lang"`
			SaveMemory       *bool   `json:"save_memory"`
			ClipboardEnabled *bool   `json:"clipboard_enabled"`
			Token            *string `json:"token"`
			Backend          *string `json:"backend"`
			OpenAIBaseURL    *string `json:"openai_base_url"`
			OpenAIKey        *string `json:"openai_key"`
			OpenAIModel      *string `json:"openai_model"`
		}
		if err := readJSON(r, &patch); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
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
		if patch.ClipboardEnabled != nil {
			s.cfg.ClipboardEnabled = *patch.ClipboardEnabled
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
		writeJSON(w, 200, s.cfg)
	default:
		writeJSON(w, 405, map[string]string{"error": "仅支持 GET/PUT"})
	}
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"logs": s.logs})
}

// handleWeb 内嵌管理界面
func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		content, _ := web.IndexHTML()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(content)
		return
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