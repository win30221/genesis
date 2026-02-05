package monitor

import (
	"fmt"
	"io"
	"os"
)

// CLIMonitor 實作 Monitor 介面，在終端機顯示所有通道的訊息
type CLIMonitor struct {
	writer io.Writer
}

// NewCLIMonitor 建立一個新的 CLI 監控器
func NewCLIMonitor() *CLIMonitor {
	return &CLIMonitor{
		writer: os.Stdout,
	}
}

// Start 啟動 CLI 監控器
func (m *CLIMonitor) Start() error {
	fmt.Fprintln(m.writer, "----------------------------------------------------------------")
	fmt.Fprintln(m.writer, "💬 CLI Monitor Active - All channel messages will appear here")
	fmt.Fprintln(m.writer, "----------------------------------------------------------------")
	return nil
}

// Stop 停止 CLI 監控器
func (m *CLIMonitor) Stop() error {
	return nil
}

// OnMessage 接收並顯示監控訊息
func (m *CLIMonitor) OnMessage(msg MonitorMessage) {
	timestamp := msg.Timestamp.Format("2006-01-02 15:04:05")

	var displayMsg string
	if msg.MessageType == "ASSISTANT" {
		displayMsg = fmt.Sprintf("[AI] %s", msg.Content)
	} else {
		displayMsg = fmt.Sprintf("[%s/%s] %s", msg.ChannelID, msg.Username, msg.Content)
	}

	// 使用灰色顯示時間戳
	fmt.Fprintf(m.writer, "\033[90m[%s]\033[0m %s\n", timestamp, displayMsg)
}
