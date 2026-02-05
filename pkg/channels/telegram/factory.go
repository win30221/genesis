package telegram

import (
	"fmt"
	"genesis/pkg/channels"
	"genesis/pkg/config"
	"genesis/pkg/gateway"
	"genesis/pkg/llm"

	jsoniter "github.com/json-iterator/go"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

// TelegramFactory 負責建立 Telegram Channels
type TelegramFactory struct{}

// Create 實作 ChannelFactory
func (f *TelegramFactory) Create(gw *gateway.GatewayManager, rawConfig jsoniter.RawMessage, history *llm.ChatHistory, system *config.SystemConfig) (gateway.Channel, error) {
	var tgCfg TelegramConfig
	if err := json.Unmarshal(rawConfig, &tgCfg); err != nil {
		return nil, fmt.Errorf("failed to parse telegram config: %w", err)
	}

	if tgCfg.Token == "" {
		return nil, fmt.Errorf("missing telegram token")
	}

	return NewTelegramChannel(tgCfg, system.TelegramMessageLimit, system.DownloadTimeoutSec)
}

func init() {
	channels.RegisterChannel("telegram", &TelegramFactory{})
}
