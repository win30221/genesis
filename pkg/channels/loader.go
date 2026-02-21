package channels

import (
	"genesis/pkg/api"
	"genesis/pkg/config"
	"genesis/pkg/llm"
	"log/slog"

	jsoniter "github.com/json-iterator/go"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

// Source encapsulates the configuration and dependencies required
// to dynamically create communication channels from configuration.
type Source struct {
	configs  map[string]jsoniter.RawMessage
	sessions *llm.SessionManager
	system   *config.SystemConfig
}

// NewSource creates a new Source instance.
func NewSource(configs map[string]jsoniter.RawMessage, sessions *llm.SessionManager, system *config.SystemConfig) *Source {
	return &Source{
		configs:  configs,
		sessions: sessions,
		system:   system,
	}
}

// Load creates channel instances from configuration and returns them.
func (s *Source) Load() []api.Channel {
	var result []api.Channel
	for name, rawConfig := range s.configs {
		factory, ok := GetChannelFactory(name)
		if !ok {
			slog.Warn("Unknown channel type", "name", name)
			continue
		}

		channel, err := factory.Create(rawConfig, s.sessions, s.system)
		if err != nil {
			slog.Error("Failed to create channel", "name", name, "error", err)
			continue
		}

		if channel == nil {
			continue
		}

		result = append(result, channel)
		slog.Info("Channel created", "name", name)
	}
	return result
}
