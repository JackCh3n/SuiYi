package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config 应用配置（内存缓存 + data/config.json 持久化）
type Config struct {
	APIPort       int    `json:"api_port"`        // Web/API 端口
	EnginePort    int    `json:"engine_port"`     // llama-server 推理端口
	ModelPath     string `json:"model_path"`      // 模型文件路径
	EnginePath    string `json:"engine_path"`     // llama-server 可执行文件路径
	TargetLang    string `json:"target_lang"`     // 默认目标语言代码
	SaveMemory    bool   `json:"save_memory"`     // 省内存模式（按需加载）
	Autostart     bool   `json:"autostart"`       // 开机自启（Windows 注册表 Run 键）
	NCTX          int    `json:"n_ctx"`           // 推理上下文长度（长文本需加大）
	NGL           int    `json:"ngl"`             // GPU 层数（0=纯 CPU；Vulkan 版可设 99 全量 GPU）
	Token         string `json:"token"`           // 可选鉴权 token（空=不鉴权）
	Backend       string `json:"backend"`         // "local"（本地 llama）或 "openai"（OpenAI 兼容 API）
	OpenAIBaseURL string `json:"openai_base_url"` // OpenAI 兼容 API 基地址（含 /v1）
	OpenAIKey     string `json:"openai_key"`      // OpenAI 兼容 API Key
	OpenAIModel   string `json:"openai_model"`    // OpenAI 兼容 API 模型名
}

// Defaults 返回带默认值的配置
func Defaults() *Config {
	return &Config{
		APIPort:       8848,
		EnginePort:    8849,
		ModelPath:     "models/Hy-MT2-1.8B-Q4_K_M.gguf",
		EnginePath:    "third_party/windows/amd64/llama-server.exe",
		TargetLang:    "zh",
		SaveMemory:    false,
		Autostart:     false,
		NCTX:          8192,
		Backend:       "local",
		OpenAIBaseURL: "https://api.openai.com/v1",
		OpenAIModel:   "gpt-4o-mini",
	}
}

// ExeDir 返回可执行文件所在目录
func ExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// Resolve 相对路径按「工作目录 → 可执行文件目录 → 可执行文件上级目录」查找，
// 返回首个存在的绝对路径（未找到也返回最可能的绝对路径，便于后续报错）。
// 兼容两种部署形态：exe 位于项目根（third_party/ 同级），或位于 build/ 子目录
// （如 build\suiyi.exe，第三方引擎/模型在项目根）。
func Resolve(rel string) string {
	if rel == "" {
		return ""
	}
	// 1) 工作目录
	if abs, err := filepath.Abs(rel); err == nil {
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	// 2) 可执行文件所在目录（exe 与 third_party 同级）
	exeDir := ExeDir()
	if cand := filepath.Join(exeDir, rel); statOK(cand) {
		return cand
	}
	// 3) 可执行文件的上级目录（exe 在 build/ 等子目录时，项目根在此）
	if parent := filepath.Dir(exeDir); parent != exeDir {
		if cand := filepath.Join(parent, rel); statOK(cand) {
			return cand
		}
	}
	// 4) 兜底：工作目录下的绝对路径（便于后续创建/清晰报错）
	if abs, err := filepath.Abs(rel); err == nil {
		return abs
	}
	return filepath.Join(exeDir, rel)
}

func statOK(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// DataDir 数据目录（模型/引擎不存在时的可写位置）
func DataDir() string {
	dir := filepath.Join(ExeDir(), "data")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func configPath() string {
	return filepath.Join(DataDir(), "config.json")
}

// Load 读取配置；不存在则写默认值
func Load() (*Config, error) {
	cfg := Defaults()
	raw, err := os.ReadFile(configPath())
	if err != nil {
		if os.IsNotExist(err) {
			_ = cfg.Save()
			return cfg, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Save 持久化配置
func (c *Config) Save() error {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), raw, 0o644)
}
