package llm

import (
	"genesis/pkg/config"
)

// ProviderGroupConfig 定義一組模型的配置
// 這從 loader.go 移動過來，作為 Factory 的輸入標準
type ProviderGroupConfig struct {
	Type                string         `json:"type"`
	APIKeys             []string       `json:"api_keys,omitempty"`
	Models              []string       `json:"models"`
	BaseURL             string         `json:"base_url,omitempty"`
	UseThoughtSignature bool           `json:"use_thought_signature,omitempty"`
	Options             map[string]any `json:"options,omitempty"`
}

// ProviderFactory 定義建立 LLM Client 的工廠介面
type ProviderFactory interface {
	// Create 根據配置建立一組 atomic clients
	Create(groupConfig ProviderGroupConfig, systemConfig *config.SystemConfig) ([]LLMClient, error)
}

// 全域 Provider 註冊表
var providerRegistry = make(map[string]ProviderFactory)

// RegisterProvider 註冊一個 Provider Factory
func RegisterProvider(name string, factory ProviderFactory) {
	providerRegistry[name] = factory
}

// GetProviderFactory 取得指定名稱的 Provider Factory
func GetProviderFactory(name string) (ProviderFactory, bool) {
	f, ok := providerRegistry[name]
	return f, ok
}
