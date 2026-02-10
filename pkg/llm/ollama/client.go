package ollama

import (
	"context"
	"genesis/pkg/llm"
	"genesis/pkg/llm/openailm"
)

// OllamaClient is now a wrapper around the generic OpenAI client
// satisfying the llm.LLMClient interface
type OllamaClient struct {
	client *openailm.Client
}

// NewOllamaClient creates an Ollama client using the OpenAI compatibility layer
func NewOllamaClient(model string, baseURL string, options map[string]any) (*OllamaClient, error) {
	// Ollama APIs are compatible with OpenAI.
	apiKey := "ollama"

	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	} else {
		if !containsV1(baseURL) {
			baseURL = baseURL + "/v1"
		}
	}

	client, err := openailm.NewClient("ollama", apiKey, model, baseURL, options)
	if err != nil {
		return nil, err
	}

	return &OllamaClient{
		client: client,
	}, nil
}

func containsV1(url string) bool {
	return len(url) >= 3 && url[len(url)-3:] == "/v1"
}

func (o *OllamaClient) Provider() string {
	return "ollama"
}

func (o *OllamaClient) SetDebug(enabled bool) {
	o.client.SetDebug(enabled)
}

func (o *OllamaClient) IsTransientError(err error) bool {
	return o.client.IsTransientError(err)
}

func (o *OllamaClient) StreamChat(ctx context.Context, messages []llm.Message, availableTools any) (<-chan llm.StreamChunk, error) {
	return o.client.StreamChat(ctx, messages, availableTools)
}
