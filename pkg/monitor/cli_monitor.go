package monitor

import (
	"fmt"
	"io"
	"os"
)

// CLIMonitor implements the Monitor interface, providing a direct
// terminal-based visualization of messages flowing through all channels.
type CLIMonitor struct {
	writer io.Writer // The output destination, typically os.Stdout.
}

// NewCLIMonitor creates a new CLI monitor
func NewCLIMonitor() *CLIMonitor {
	return &CLIMonitor{
		writer: os.Stdout,
	}
}

// Start starts the CLI monitor
func (m *CLIMonitor) Start() error {
	fmt.Fprintln(m.writer, "----------------------------------------------------------------")
	fmt.Fprintln(m.writer, "💬 CLI Monitor Active - All channel messages will appear here")
	fmt.Fprintln(m.writer, "----------------------------------------------------------------")
	return nil
}

// Stop stops the CLI monitor
func (m *CLIMonitor) Stop() error {
	return nil
}

// OnMessage receives and displays a monitoring message
func (m *CLIMonitor) OnMessage(msg MonitorMessage) {
	timestamp := msg.Timestamp.Format("2006-01-02 15:04:05")

	var displayMsg string
	if msg.MessageType == "ASSISTANT" {
		displayMsg = fmt.Sprintf("[AI] %s", msg.Content)
	} else {
		displayMsg = fmt.Sprintf("[%s/%s] %s", msg.ChannelID, msg.Username, msg.Content)
	}

	// Use gray color for timestamp
	fmt.Fprintf(m.writer, "\033[90m[%s]\033[0m %s\n", timestamp, displayMsg)
}
