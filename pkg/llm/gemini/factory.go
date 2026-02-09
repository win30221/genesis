package gemini

import (
	"genesis/pkg/config"
	"genesis/pkg/llm"
)

// GeminiFactory handles creation of Gemini Clients
type GeminiFactory struct{}

// Create implements ProviderFactory
func (f *GeminiFactory) Create(cfg llm.ProviderGroupConfig, sys *config.SystemConfig) ([]llm.LLMClient, error) {
	var clients []llm.LLMClient

	// Cartesian Product: Models x Keys (prioritize models)
	for _, model := range cfg.Models {
		for _, key := range cfg.APIKeys {
			client := NewGeminiClient(key, model, cfg.UseThoughtSignature)
			clients = append(clients, client)
		}
	}
	return clients, nil
}

func init() {
	llm.RegisterProvider("gemini", &GeminiFactory{})
}
