package channels

import (
	"genesis/pkg/config"
	"genesis/pkg/gateway"
	"genesis/pkg/llm"

	jsoniter "github.com/json-iterator/go"
)

// ChannelFactory defines the abstract interface for platform-specific
// channel creators. This allows the system to support new platforms
// (e.g., Line, Discord) without modifying the core gateway logic.
type ChannelFactory interface {
	// Create instantiates a concrete Channel implementation using the
	// provided configuration and shared system resources.
	Create(rawConfig jsoniter.RawMessage, history *llm.ChatHistory, system *config.SystemConfig) (gateway.Channel, error)
}

// channelRegistry is an internal global map stores the mapping between
// platform names (e.g., "telegram") and their factory implementations.
var channelRegistry = make(map[string]ChannelFactory)

// RegisterChannel adds a new ChannelFactory to the global internal registry.
// This is typically called during the package's init() phase.
func RegisterChannel(name string, factory ChannelFactory) {
	channelRegistry[name] = factory
}

// GetChannelFactory retrieves a registered ChannelFactory by platform name.
func GetChannelFactory(name string) (ChannelFactory, bool) {
	f, ok := channelRegistry[name]
	return f, ok
}
