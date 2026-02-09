package gateway

import "genesis/pkg/llm"

// Channel defines the standardized lifecycle interface for communication platforms.
// Every specific adaptation (e.g., Web, Telegram, Line) must implement this
// interface to integrate into the Genesis message routing ecosystem.
type Channel interface {
	// ID returns a unique string identifier for this specific channel instance
	// (e.g., "web", "telegram-bot-alpha").
	ID() string
	// Start initiates the message receiving loop or webhook listener.
	// This method must be non-blocking (asynchronous) to allow the manager
	// to start multiple channels in sequence.
	Start(ctx ChannelContext) error
	// Stop gracefully shuts down the channel and releases any held resources
	// like network ports or long-polling workers.
	Stop() error
	// Send transmits a plain text message proactively to a specific session.
	Send(session SessionContext, message string) error
	// Stream sends multiple ContentBlocks (text, thinking, images) in a
	// streaming manner to a specific session.
	Stream(session SessionContext, blocks <-chan llm.ContentBlock) error
}

// SignalingChannel is an optional extension of the Channel interface for
// platforms that support control signals (e.g., typing indicators, thinking UI).
type SignalingChannel interface {
	Channel
	// SendSignal transmits a control signal (e.g., "thinking", "role:system")
	// to the target session to change UI state or metadata.
	SendSignal(session SessionContext, signal string) error
}

// ChannelContext provides the interface for a Channel implementation to
// communicate back with the Gateway core.
type ChannelContext interface {
	// OnMessage is the callback invoked when a Channel receives an external
	// message. It standardizes the data into a UnifiedMessage for the core.
	OnMessage(channelID string, msg *UnifiedMessage)
}

// UnifiedMessage defines the standardized internal data structure for all
// incoming and outgoing messages within the Genesis system.
// It serves as a translation layer between platform-specific payloads and
// the universal LLM processing logic.
type UnifiedMessage struct {
	Session       SessionContext   // Contextual information about the source (User, Chat)
	Content       string           // Standardized text content of the message
	Files         []FileAttachment // List of file attachments like images or documents
	Raw           any              // Optional storage for the original platform-specific payload object
	RetryCount    int              // Counter for automatic recovery attempts during stream failures
	ContinueCount int              // Counter for content continuation calls (handling length limits)
	NoTools       bool             // Virtual flag to disable tool calling for specific requests
	DebugID       string           // Unique identifier for grouping agentic loop logs for this request
}

// FileAttachment represents a single file or binary object uploaded by a user.
type FileAttachment struct {
	Filename string // Original name of the uploaded file
	MimeType string // MIME type descriptor (e.g., "image/jpeg", "application/pdf")
	Data     []byte // Raw binary content of the file
}

// SessionContext encapsulates identity and routing information for a specific
// conversation unit on a specific communication channel.
type SessionContext struct {
	ChannelID string // Identifier of the channel that originated the session (e.g., "telegram")
	UserID    string // Platform-specific unique identifier for the user
	ChatID    string // Platform-specific identifier for the chat or group (may match UserID for DMs)
	Username  string // Display name or nickname of the user as provided by the platform
}
