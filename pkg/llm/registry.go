package llm

import (
	"genesis/pkg/config"
)

// ProviderGroupConfig defines a schema for configuring a cluster of models
// from a specific LLM provider. This configuration allows for multi-model
// support and model-specific behavioral flags (like thought signatures).
type ProviderGroupConfig struct {
	Type                string         `json:"type"`                            // Provider type identifier (e.g., "gemini", "ollama")
	APIKeys             []string       `json:"api_keys,omitempty"`              // Optional pool of API keys for load balancing or rotation
	Models              []string       `json:"models"`                          // List of model names to initialize (e.g., ["gemini-1.5-flash"])
	BaseURL             string         `json:"base_url,omitempty"`              // Custom API endpoint (mostly used for local Ollama instances)
	UseThoughtSignature bool           `json:"use_thought_signature,omitempty"` // Whether to enable reasoning token tracking (Gemini specific)
	Options             map[string]any `json:"options,omitempty"`               // Arbitrary provider-specific parameters (temperature, topP, etc.)
}

// ProviderFactory is a structural interface for provider-specific loaders.
// Each provider (Gemini, Ollama, OpenAI) must implement this factory to
// allow the generic LLM loader to instantiate its clients.
type ProviderFactory interface {
	// Create instantiates one or more LLMClient objects based on the group
	// configuration and system-level technical parameters.
	Create(groupConfig ProviderGroupConfig, systemConfig *config.SystemConfig) ([]LLMClient, error)
}

// providerRegistry is an internal global map stores the mapping between provider
// names and their respective factory implementations.
var providerRegistry = make(map[string]ProviderFactory)

// RegisterProvider adds a new ProviderFactory to the global internal registry.
// This is typically called within the init() function of each provider package.
func RegisterProvider(name string, factory ProviderFactory) {
	providerRegistry[name] = factory
}

// GetProviderFactory returns a registered ProviderFactory by its provider name.
func GetProviderFactory(name string) (ProviderFactory, bool) {
	f, ok := providerRegistry[name]
	return f, ok
}
