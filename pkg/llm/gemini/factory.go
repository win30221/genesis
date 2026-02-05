package gemini

import (
	"genesis/pkg/config"
	"genesis/pkg/llm"
)

// GeminiFactory 負責建立 Gemini Clients
type GeminiFactory struct{}

// Create 實作 ProviderFactory
func (f *GeminiFactory) Create(cfg llm.ProviderGroupConfig, sys *config.SystemConfig) ([]llm.LLMClient, error) {
	var clients []llm.LLMClient

	// Cartesian Product: Keys x Models
	for _, key := range cfg.APIKeys {
		for _, model := range cfg.Models {
			client := NewGeminiClient(key, model, cfg.UseThoughtSignature)
			clients = append(clients, client)
		}
	}
	return clients, nil
}

func init() {
	llm.RegisterProvider("gemini", &GeminiFactory{})
}
