package config

import (
	"fmt"
	"os"

	jsoniter "github.com/json-iterator/go"
)

// Config 定義全域應用設定結構
type Config struct {
	Channels     map[string]jsoniter.RawMessage `json:"channels"`
	LLM          jsoniter.RawMessage            `json:"llm"`
	SystemPrompt string                         `json:"system_prompt"`

	// System 底層引擎設定 (從 system.json 載入)
	System *SystemConfig `json:"-"`
}

// SystemConfig 定義引擎層級的技術參數
type SystemConfig struct {
	MaxRetries            int    `json:"max_retries"`
	RetryDelayMs          int    `json:"retry_delay_ms"`
	LLMTimeoutMin         int    `json:"llm_timeout_min"`
	OllamaDefaultURL      string `json:"ollama_default_url"`
	InternalChannelBuffer int    `json:"internal_channel_buffer"`
	ThinkingInitDelayMs   int    `json:"thinking_init_delay_ms"`
	ThinkingTokenDelayMs  int    `json:"thinking_token_delay_ms"`
	TelegramMessageLimit  int    `json:"telegram_message_limit"`
	DownloadTimeoutSec    int    `json:"download_timeout_sec"`
	ShowThinking          bool   `json:"show_thinking"` // 是否顯示思考過程
}

// DefaultSystemConfig 返回硬編碼的預設值
func DefaultSystemConfig() *SystemConfig {
	return &SystemConfig{
		MaxRetries:            3,
		RetryDelayMs:          500,
		LLMTimeoutMin:         10,
		OllamaDefaultURL:      "http://localhost:11434",
		InternalChannelBuffer: 100,
		ThinkingInitDelayMs:   500,
		ThinkingTokenDelayMs:  200,
		TelegramMessageLimit:  4000,
		DownloadTimeoutSec:    10,
		ShowThinking:          true, // 預設顯示思考過程
	}
}

// Load 讀取並解析 JSON 設定檔
func Load(path string) (*Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("config file '%s' not found. please create one", path)
	}

	file, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := jsoniter.ConfigCompatibleWithStandardLibrary.Unmarshal(file, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// 載入系統設定
	cfg.System = LoadSystemConfig("system.json")

	return &cfg, nil
}

// LoadSystemConfig 嘗試載入系統設定，失敗則返回預設值
func LoadSystemConfig(path string) *SystemConfig {
	cfg := DefaultSystemConfig()

	file, err := os.ReadFile(path)
	if err != nil {
		return cfg // 檔案不存在，使用預設值
	}

	if err := jsoniter.ConfigCompatibleWithStandardLibrary.Unmarshal(file, cfg); err != nil {
		return cfg // 解析失敗，使用預設值
	}

	return cfg
}
