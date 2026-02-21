package ollama

import (
	"genesis/pkg/config"
	"genesis/pkg/llm"
	"log/slog"
)

// OllamaFactory handles creation of Ollama Clients
type OllamaFactory struct{}

// Create implements ProviderFactory
func (f *OllamaFactory) Create(cfg llm.ProviderGroupConfig, sys *config.SystemConfig) ([]llm.LLMClient, error) {
	var clients []llm.LLMClient

	for _, model := range cfg.Models {
		baseURL := cfg.BaseURL
		// Factory guarantees a valid URL (if not set in config, it remains empty or client uses default)
		client, err := NewOllamaClient(model, baseURL, cfg.Options, sys)
		if err != nil {
			slog.Error("Failed to create Ollama client", "model", model, "error", err)
			continue
		}
		clients = append(clients, client)
	}
	return clients, nil
}

func init() {
	llm.RegisterProvider("ollama", &OllamaFactory{})
}
