package config

import (
	"fmt"
	"os"

	jsoniter "github.com/json-iterator/go"
)

// Config defines the global application configuration structure.
// This structure maps directly to the config.json file and holds
// business-level settings like channel API keys and LLM provider choices.
type Config struct {
	// Channels contains a map of channel identifiers (e.g., "telegram", "web")
	// to their specific configuration payloads in raw JSON format.
	Channels map[string]jsoniter.RawMessage `json:"channels"`
	// LLM holds the configuration for the primary LLM provider in raw JSON.
	LLM jsoniter.RawMessage `json:"llm"`
	// SystemPrompt is the global persona/instruction string sent to the AI
	// as the initial system message in every conversation.
	SystemPrompt string `json:"system_prompt"`
}

// Validate ensures the configuration structure contains all mandatory fields.
// It acts as a primary guard before the system proceeds to initialization.
func (c *Config) Validate() error {
	if len(c.LLM) == 0 {
		return fmt.Errorf("mandatory 'llm' configuration is missing or empty")
	}
	return nil
}

// SystemConfig defines engine-level technical parameters.
// These settings are usually stored in system.json and control the
// performance, reliability, and technical behavior of the Genesis engine.
type SystemConfig struct {
	// MaxRetries is the number of times the system will attempt to
	// recover from a transient LLM or network error before giving up.
	MaxRetries int `json:"max_retries"`
	// MaxContinuations is the maximum number of automatic content
	// continuation requests when the LLM response is truncated due to length limits.
	MaxContinuations int `json:"max_continuations"`
	// RetryDelayMs is the duration to wait (in milliseconds) between
	// consecutive retry attempts.
	RetryDelayMs int `json:"retry_delay_ms"`
	// LLMTimeoutMs is the hard cutoff time (in milliseconds) for an
	// LLM request. The context will be cancelled if exceeded.
	LLMTimeoutMs int `json:"llm_timeout_ms"`
	// OllamaDefaultURL is the fallback endpoint used when connecting
	// to a local Ollama instance if no specific URL is provided.
	OllamaDefaultURL string `json:"ollama_default_url"`
	// InternalChannelBuffer defines the size of the internal Go channels
	// used for buffering stream chunks to prevent production blocking.
	InternalChannelBuffer int `json:"internal_channel_buffer"`
	// ThinkingInitDelayMs is the time to wait (in milliseconds) after a
	// user message before showing the "AI is thinking" status in the UI.
	ThinkingInitDelayMs int `json:"thinking_init_delay_ms"`
	// ThinkingTokenDelayMs is the threshold (in milliseconds) used to
	// detect if the AI has paused during streaming, triggering a thinking signal.
	ThinkingTokenDelayMs int `json:"thinking_token_delay_ms"`
	// TelegramMessageLimit is the maximum character count for a single
	// Telegram message. Longer responses will be split into multiple chunks.
	TelegramMessageLimit int `json:"telegram_message_limit"`
	// DownloadTimeoutMs is the timeout (in milliseconds) applied when
	// fetching external media or files (e.g., from Telegram servers).
	DownloadTimeoutMs int `json:"download_timeout_ms"`
	// ShowThinking determines whether the AI's internal reasoning process (thinking blocks)
	// should be streamed and displayed to the end user.
	ShowThinking bool `json:"show_thinking"`
	// DebugChunks enables saving every raw LLM response chunk to the /debug
	// folder for inspection and troubleshooting purposes.
	DebugChunks bool `json:"debug_chunks"`
	// LogLevel sets the minimum severity for log output.
	// Accepted values: "debug", "info", "warn", "error". Default: "info".
	LogLevel string `json:"log_level"`
	// EnableTools globally toggles the tool calling (agentic) functionality.
	// If false, the AI will not be provided with any external tools/capabilities.
	EnableTools bool `json:"enable_tools"`
}

// DefaultSystemConfig returns a SystemConfig pointer initialized with hardcoded
// safe default values. This is used as a fallback when the system.json file
// is missing or corrupt, ensuring the engine can always start.
func DefaultSystemConfig() *SystemConfig {
	return &SystemConfig{
		MaxRetries:            3,
		MaxContinuations:      5,
		RetryDelayMs:          500,
		LLMTimeoutMs:          600000,
		OllamaDefaultURL:      "http://localhost:11434",
		InternalChannelBuffer: 100,
		ThinkingInitDelayMs:   500,
		ThinkingTokenDelayMs:  200,
		TelegramMessageLimit:  4000,
		DownloadTimeoutMs:     10000,
		ShowThinking:          true,
		LogLevel:              "info",
		EnableTools:           true,
	}
}

// Load reads and parses the JSON configuration files from the current working directory.
// It first attempts to load 'config.json' (app config). If this file is missing, it returns an error.
// Then it calls LoadSystemConfig to load 'system.json'.
// Returns pointers to the loaded Config and SystemConfig, or an error if the mandatory app config fails.
func Load() (*Config, *SystemConfig, error) {
	// 1. Load Application Config
	appPath := "config.json"
	if _, err := os.Stat(appPath); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("config file '%s' not found. please create one", appPath)
	}

	appFile, err := os.ReadFile(appPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := jsoniter.ConfigCompatibleWithStandardLibrary.Unmarshal(appFile, &cfg); err != nil {
		return nil, nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// 1a. Validate structure integrity
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}

	// 2. Load System Config independently
	sysCfg := LoadSystemConfig("system.json")

	return &cfg, sysCfg, nil
}

// LoadSystemConfig attempts to load system settings, returns defaults if it fails
func LoadSystemConfig(path string) *SystemConfig {
	cfg := DefaultSystemConfig()

	file, err := os.ReadFile(path)
	if err != nil {
		return cfg // File not found, use defaults
	}

	if err := jsoniter.ConfigCompatibleWithStandardLibrary.Unmarshal(file, cfg); err != nil {
		return cfg // Parse failed, use defaults
	}

	return cfg
}
