package monitor

import "time"

// MonitorMessage 代表一則監控訊息
type MonitorMessage struct {
	Timestamp   time.Time
	MessageType string // "USER" or "ASSISTANT"
	ChannelID   string
	Username    string
	Content     string
}

// Monitor 介面定義了監控器的行為
type Monitor interface {
	// Start 啟動監控器
	Start() error

	// Stop 停止監控器
	Stop() error

	// OnMessage 接收並顯示監控訊息
	OnMessage(msg MonitorMessage)
}
