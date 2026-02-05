package gateway

import (
	"fmt"
	"genesis/pkg/config"
	"genesis/pkg/monitor"

	jsoniter "github.com/json-iterator/go"
)

// GatewayBuilder 用於建構 GatewayManager
type GatewayBuilder struct {
	gw             *GatewayManager
	monitor        monitor.Monitor
	systemConfig   *config.SystemConfig
	channelConfigs map[string]jsoniter.RawMessage
	channelLoader  func(*GatewayManager, map[string]jsoniter.RawMessage)
	handlerFactory func(*GatewayManager) MessageHandler // Handler 工廠函數
}

// NewGatewayBuilder 建立一個新的 GatewayBuilder
func NewGatewayBuilder() *GatewayBuilder {
	return &GatewayBuilder{
		gw: NewGatewayManager(),
	}
}

// WithMonitor 設定監控器
func (b *GatewayBuilder) WithMonitor(m monitor.Monitor) *GatewayBuilder {
	b.monitor = m
	return b
}

// WithSystemConfig 設定系統配置
func (b *GatewayBuilder) WithSystemConfig(cfg *config.SystemConfig) *GatewayBuilder {
	b.systemConfig = cfg
	return b
}

// WithChannelConfigs 設定 Channel 配置
func (b *GatewayBuilder) WithChannelConfigs(configs map[string]jsoniter.RawMessage) *GatewayBuilder {
	b.channelConfigs = configs
	return b
}

// WithChannelLoader 設定 Channel 載入函數
func (b *GatewayBuilder) WithChannelLoader(loader func(*GatewayManager, map[string]jsoniter.RawMessage)) *GatewayBuilder {
	b.channelLoader = loader
	return b
}

// WithHandlerFactory 設定 Handler 工廠函數（接受 Gateway 引用並返回 MessageHandler）
func (b *GatewayBuilder) WithHandlerFactory(factory func(*GatewayManager) MessageHandler) *GatewayBuilder {
	b.handlerFactory = factory
	return b
}

// Build 建構並啟動 Gateway
func (b *GatewayBuilder) Build() (*GatewayManager, error) {
	// 0. 設定系統層級參數
	if b.systemConfig != nil {
		b.gw.SetChannelBuffer(b.systemConfig.InternalChannelBuffer)
	}

	// 1. 設定監控器
	if b.monitor != nil {
		b.gw.SetMonitor(b.monitor)
		if err := b.monitor.Start(); err != nil {
			return nil, fmt.Errorf("failed to start monitor: %w", err)
		}
	}

	// 2. 載入 Channels
	if b.channelConfigs != nil && b.channelLoader != nil {
		b.channelLoader(b.gw, b.channelConfigs)
	}

	// 3. 設定訊息處理器（使用工廠函數建立，傳入 Gateway 引用）
	if b.handlerFactory != nil {
		handler := b.handlerFactory(b.gw)
		b.gw.SetMessageHandler(handler)
	}

	// 4. 啟動所有 Channels
	if err := b.gw.StartAll(); err != nil {
		return nil, fmt.Errorf("failed to start channels: %w", err)
	}

	return b.gw, nil
}
