package gateway

import (
	"fmt"
	"genesis/pkg/config"
	"genesis/pkg/monitor"
)

// GatewayBuilder provides a fluent builder pattern interface for constructing
// and initializing a GatewayManager with all its necessary dependencies like
// monitors, configurations, and handler factories.
type GatewayBuilder struct {
	gw             *GatewayManager                      // The GatewayManager instance being constructed
	monitor        monitor.Monitor                      // Monitoring implementation to be injected
	systemConfig   *config.SystemConfig                 // Technical parameters for the gateway
	channelLoader  func(*GatewayManager)                // Function responsible for loading and registering channels
	handlerFactory func(*GatewayManager) MessageHandler // Factory function to create the core message handler
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

// WithChannelLoader injects a loader function that knows how to instantiate
// and register specific Channel implementations. The loader captures all
// required dependencies (configs, history, etc.) via closure.
func (b *GatewayBuilder) WithChannelLoader(loader func(*GatewayManager)) *GatewayBuilder {
	b.channelLoader = loader
	return b
}

// WithHandlerFactory provides a factory function that creates the core
// MessageHandler (tipically ChatHandler.OnMessage) by passing the fully
// configured GatewayManager reference.
func (b *GatewayBuilder) WithHandlerFactory(factory func(*GatewayManager) MessageHandler) *GatewayBuilder {
	b.handlerFactory = factory
	return b
}

// Build finalizes the configuration, injects all dependencies into the
// GatewayManager, starts the monitor and all registered channels.
// Returns the fully operational GatewayManager or an error if any stage fails.
func (b *GatewayBuilder) Build() (*GatewayManager, error) {
	// 0. Extract and apply system-level parameters (like buffer sizes)
	if b.systemConfig != nil {
		b.gw.SetChannelBuffer(b.systemConfig.InternalChannelBuffer)
	}

	// 1. Initialize and start the monitoring service
	if b.monitor != nil {
		b.gw.SetMonitor(b.monitor)
		if err := b.monitor.Start(); err != nil {
			return nil, fmt.Errorf("failed to start monitor: %w", err)
		}
	}

	// 2. Load and register communication channels using the provided loader
	if b.channelLoader != nil {
		b.channelLoader(b.gw)
	}

	// 3. Instantiate and bind the core message handler
	if b.handlerFactory != nil {
		handler := b.handlerFactory(b.gw)
		b.gw.SetMessageHandler(handler)
	}

	// 4. Trigger the startup sequence for all successfully registered channels
	if err := b.gw.StartAll(); err != nil {
		return nil, fmt.Errorf("failed to start channels: %w", err)
	}

	return b.gw, nil
}
