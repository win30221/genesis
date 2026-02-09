package channels

import (
	"genesis/pkg/config"
	"genesis/pkg/gateway"
	"genesis/pkg/llm"
	"log"

	jsoniter "github.com/json-iterator/go"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

// LoadFromConfig acts as the central orchestration point for dynamic
// channel initialization. It iterates through the provided configuration
// map, resolves factories, and registers the resulting channels with
// the GatewayManager.
func LoadFromConfig(gw *gateway.GatewayManager, configs map[string]jsoniter.RawMessage, history *llm.ChatHistory, system *config.SystemConfig) {
	for name, rawConfig := range configs {
		factory, ok := GetChannelFactory(name)
		if !ok {
			log.Printf("Unknown Channel type: %s", name)
			continue
		}

		channel, err := factory.Create(rawConfig, history, system)
		if err != nil {
			log.Printf("Failed to create channel '%s': %v", name, err)
			continue
		}

		// If Create returns nil (e.g., certain conditions not met but not an error), skip
		if channel == nil {
			continue
		}

		gw.Register(channel)
		log.Printf("✅ Channel '%s' registered", name)
	}
}
