package channels

import (
	"genesis/pkg/config"
	"genesis/pkg/gateway"
	"genesis/pkg/llm"

	jsoniter "github.com/json-iterator/go"
)

// ChannelFactory 定義建立 Channel 的介面
type ChannelFactory interface {
	// Create 解析 rawConfig 並建立對應的 Channel
	Create(gw *gateway.GatewayManager, rawConfig jsoniter.RawMessage, history *llm.ChatHistory, system *config.SystemConfig) (gateway.Channel, error)
}

// 全域 Channel 註冊表
var channelRegistry = make(map[string]ChannelFactory)

// RegisterChannel 註冊一個 Channel Factory
func RegisterChannel(name string, factory ChannelFactory) {
	channelRegistry[name] = factory
}

// GetChannelFactory 取得指定名稱的 Channel Factory
func GetChannelFactory(name string) (ChannelFactory, bool) {
	f, ok := channelRegistry[name]
	return f, ok
}
