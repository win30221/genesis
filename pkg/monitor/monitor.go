package monitor

import "time"

// MonitorMessage represents a standardized data packet for system observability.
// It is broadcasted by the Gateway whenever a user or assistant message is
// processed, allowing different monitors (CLI, Web, Log) to display or save it.
type MonitorMessage struct {
	Timestamp   time.Time // Precision recording of when the event occurred
	MessageType string    // Identity of the sender: "USER" or "ASSISTANT"
	ChannelID   string    // Source platform ID (e.g., "telegram", "web")
	Username    string    // Display name of the participant
	Content     string    // Standardized text content of the message
}

// Monitor defines the lifecycle and message consumption protocol for
// observability plugins. Implementations are responsible for presenting
// the internal message flow to the administrator or end-user.
type Monitor interface {
	// Start initiates the monitoring session and allocates display resources
	// (e.g., clearing the terminal or opening a file handle).
	Start() error

	// Stop gracefully terminates the monitor and releases held resources.
	Stop() error

	// OnMessage receives and displays a monitoring message
	OnMessage(msg MonitorMessage)
}

// SetupEnvironment encapsulates the initialization of the system logging
// environment and the creation of a default CLI monitor instance.
// This simplifies the main bootstrap sequence.
func SetupEnvironment() Monitor {
	// Initialize global logger and print banner
	Startup()
	// Return the default implementation for terminal visualization
	return NewCLIMonitor()
}
