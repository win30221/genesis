package telegram

import (
	"fmt"
	"genesis/pkg/api"
	"genesis/pkg/channels"
	"genesis/pkg/config"
	"genesis/pkg/llm"

	jsoniter "github.com/json-iterator/go"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

// TelegramFactory implements the channels.ChannelFactory interface to
// instantiate Telegram-specific communication adapters.
type TelegramFactory struct{}

// Create parses the channel-specific configuration and initializes a
// TelegramChannel instance with synchronized system-level timeouts.
func (f *TelegramFactory) Create(rawConfig jsoniter.RawMessage, sessions *llm.SessionManager, system *config.SystemConfig) (api.Channel, error) {
	var tgCfg TelegramConfig
	if err := json.Unmarshal(rawConfig, &tgCfg); err != nil {
		return nil, fmt.Errorf("failed to parse telegram config: %w", err)
	}

	if tgCfg.Token == "" {
		return nil, fmt.Errorf("missing telegram token")
	}

	return NewTelegramChannel(tgCfg, system.TelegramMessageLimit, system.DownloadTimeoutMs)
}

func init() {
	channels.RegisterChannel("telegram", &TelegramFactory{})
}
