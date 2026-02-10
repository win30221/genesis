package openailm

import (
	"genesis/pkg/config"
	"genesis/pkg/llm"
	"log/slog"
)

// OpenAIFactory handles creation of OpenAI Clients
type OpenAIFactory struct{}

// Create implements ProviderFactory
func (f *OpenAIFactory) Create(cfg llm.ProviderGroupConfig, sys *config.SystemConfig) ([]llm.LLMClient, error) {
	var clients []llm.LLMClient

	// Retrieve API Key
	apiKey := ""
	if len(cfg.APIKeys) > 0 {
		apiKey = cfg.APIKeys[0]
	}

	for _, model := range cfg.Models {
		baseURL := cfg.BaseURL

		client, err := NewClient("openai", apiKey, model, baseURL, cfg.Options)
		if err != nil {
			slog.Error("Failed to create OpenAI client", "model", model, "error", err)
			continue
		}
		clients = append(clients, client)
	}
	return clients, nil
}

func init() {
	llm.RegisterProvider("openai", &OpenAIFactory{})
}
