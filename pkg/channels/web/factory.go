package web

import (
	"fmt"
	"genesis/pkg/api"
	"genesis/pkg/channels"
	"genesis/pkg/config"
	"genesis/pkg/llm"

	jsoniter "github.com/json-iterator/go"
)

// WebFactory implements the channels.ChannelFactory interface to
// instantiate WebSocket-based communication adapters.
type WebFactory struct{}

// Create parses the web-specific configuration and initializes a
// WebChannel instance.
func (f *WebFactory) Create(rawConfig jsoniter.RawMessage, sessions *llm.SessionManager, system *config.SystemConfig) (api.Channel, error) {
	var pCfg WebConfig
	// Set default port
	pCfg.Port = 9453

	if err := json.Unmarshal(rawConfig, &pCfg); err != nil {
		return nil, fmt.Errorf("failed to parse web config: %w", err)
	}

	return NewWebChannel(pCfg, sessions), nil
}

func init() {
	channels.RegisterChannel("web", &WebFactory{})
}
