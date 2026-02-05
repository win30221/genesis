package web

import (
	"fmt"
	"genesis/pkg/channels"
	"genesis/pkg/config"
	"genesis/pkg/gateway"
	"genesis/pkg/llm"

	jsoniter "github.com/json-iterator/go"
)

// WebFactory 負責建立 Web Channels
type WebFactory struct{}

// Create 實作 ChannelFactory
func (f *WebFactory) Create(gw *gateway.GatewayManager, rawConfig jsoniter.RawMessage, history *llm.ChatHistory, system *config.SystemConfig) (gateway.Channel, error) {
	var pCfg WebConfig
	// 設定預設 Port
	pCfg.Port = 8080

	if err := json.Unmarshal(rawConfig, &pCfg); err != nil {
		return nil, fmt.Errorf("failed to parse web config: %w", err)
	}

	return NewWebChannel(pCfg, gw, history), nil
}

func init() {
	channels.RegisterChannel("web", &WebFactory{})
}
