package channels

import (
	"genesis/pkg/config"
	"genesis/pkg/gateway"
	"genesis/pkg/llm"
	"log"

	jsoniter "github.com/json-iterator/go"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

// LoadFromConfig 根據設定檔 map 動態初始化並註冊 Channels
func LoadFromConfig(gw *gateway.GatewayManager, configs map[string]jsoniter.RawMessage, history *llm.ChatHistory, system *config.SystemConfig) {
	for name, rawConfig := range configs {
		factory, ok := GetChannelFactory(name)
		if !ok {
			log.Printf("⚠️ Unknown Channel type: %s", name)
			continue
		}

		channel, err := factory.Create(gw, rawConfig, history, system)
		if err != nil {
			log.Printf("❌ Failed to create channel '%s': %v", name, err)
			continue
		}

		// 如果 Create 返回 nil (例如某些條件不滿足但不算錯誤), 則跳過
		if channel == nil {
			continue
		}

		gw.Register(channel)
		log.Printf("✅ Channel '%s' registered", name)
	}
}
