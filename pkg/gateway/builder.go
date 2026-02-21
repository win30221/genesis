package gateway

import (
	"fmt"
	"genesis/pkg/api"
	"genesis/pkg/config"
	"genesis/pkg/monitor"
)

// GatewayBuilder provides a fluent builder pattern interface for constructing
// and initializing a GatewayManager with all its necessary dependencies.
//
// All components (channels, handler, engine, tools) are pre-built and injected
// as instances — the Builder simply assembles and starts them.
type GatewayBuilder struct {
	gw             *GatewayManager                                 // The GatewayManager instance being constructed
	monitor        monitor.Monitor                                 // Monitoring implementation to be injected
	systemConfig   *config.SystemConfig                            // Technical parameters for the gateway
	handlerBuilder func(api.MessageResponder) api.MessageProcessor // Unified strategy to construct and wire the message handler
	channels       []api.Channel                                   // Pre-built channel instances to register
	agentEngine    api.AgentEngine                                 // Agent Engine (using strong type from api)
}

// NewGatewayBuilder creates a fresh GatewayBuilder instance and allocates
// an internal GatewayManager to be configured.
func NewGatewayBuilder() *GatewayBuilder {
	return &GatewayBuilder{
		gw: NewGatewayManager(),
	}
}

// WithMonitor injects a monitoring implementation into the builder.
// This monitor will be started automatically during the Build() process.
func (b *GatewayBuilder) WithMonitor(m monitor.Monitor) *GatewayBuilder {
	b.monitor = m
	return b
}

// WithSystemConfig provides engine-level technical parameters to the builder,
// which are used to set up internal buffers and other system behaviors.
func (b *GatewayBuilder) WithSystemConfig(cfg *config.SystemConfig) *GatewayBuilder {
	b.systemConfig = cfg
	return b
}

// WithChannel adds pre-built channel instances to the gateway.
func (b *GatewayBuilder) WithChannel(channels ...api.Channel) *GatewayBuilder {
	b.channels = append(b.channels, channels...)
	return b
}

// WithAgentEngine injects an agent engine into the gateway.
func (b *GatewayBuilder) WithAgentEngine(engine api.AgentEngine) *GatewayBuilder {
	b.agentEngine = engine
	return b
}

// WithHandler injects a message handler instance into the gateway.
// If the handler implements api.ResponderAware, it will be automatically initialized.
func (b *GatewayBuilder) WithHandler(h api.MessageProcessor) *GatewayBuilder {
	b.handlerBuilder = func(responder api.MessageResponder) api.MessageProcessor {
		if setter, ok := h.(api.ResponderAware); ok {
			setter.SetResponder(responder)
		}
		return h
	}
	return b
}

// Build finalizes the configuration, injects all dependencies into the
// GatewayManager, registers all channels, and starts everything.
// Returns the fully operational GatewayManager or an error if any stage fails.
func (b *GatewayBuilder) Build() (*GatewayManager, error) {
	// 0. Extract and apply system-level parameters
	if b.systemConfig != nil {
		b.gw.WithSystemConfig(b.systemConfig)
	}

	// 1. Initialize and start the monitoring service
	if b.monitor != nil {
		b.gw.SetMonitor(b.monitor)
		if err := b.monitor.Start(); err != nil {
			return nil, fmt.Errorf("failed to start monitor: %w", err)
		}
	}

	// 2. Register all pre-built channels
	for _, c := range b.channels {
		b.gw.Register(c)
	}

	// 3. Establish the core message handler using the registered strategy
	if b.handlerBuilder != nil {
		handler := b.handlerBuilder(b.gw)
		if handler != nil {
			b.gw.SetMessageHandler(handler.OnMessage)
		}
	}

	// 4. Inject responder into engine if provided
	if b.agentEngine != nil {
		b.agentEngine.SetResponder(b.gw)
	}

	// 5. Start all registered channels
	if err := b.gw.StartAll(); err != nil {
		return nil, fmt.Errorf("failed to start channels: %w", err)
	}

	return b.gw, nil
}
