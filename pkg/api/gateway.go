package api

import (
	"genesis/pkg/llm"
)

// Channel defines the standardized lifecycle interface for communication platforms.
type Channel interface {
	ID() string
	Start(ctx ChannelContext) error
	Stop() error
	Send(session SessionContext, message string) error
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
	MessageResponder
	OnMessage(channelID string, msg *UnifiedMessage)
}

// MessageResponder defines the capabilities for sending responses back to a channel.
type MessageResponder interface {
	SendReply(session SessionContext, content string) error
	StreamReply(session SessionContext, blocks <-chan llm.ContentBlock) error
	SendSignal(session SessionContext, signal string) error
}

// UnifiedMessage defines the standardized internal data structure for all
// incoming and outgoing messages within the Genesis system.
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

// SessionContext encapsulates identity and routing information for a specific
// conversation unit on a specific communication channel.
type SessionContext struct {
	ChannelID string // Identifier of the channel that originated the session (e.g., "telegram")
	UserID    string // Platform-specific unique identifier for the user
	ChatID    string // Platform-specific identifier for the chat or group (may match UserID for DMs)
	Username  string // Display name or nickname of the user as provided by the platform
}

// FileAttachment represents a single file or binary object uploaded by a user.
type FileAttachment struct {
	Filename string // Original name of the uploaded file
	MimeType string // MIME type descriptor (e.g., "image/jpeg", "application/pdf")
	Data     []byte // Raw binary content of the file (nil if Path is set)
	Path     string // Absolute or relative path to the saved file (omits need for Data)
}

// MessageHandler defines the function signature for processing incoming messages.
// It implements the MessageProcessor interface.
type MessageHandler func(*UnifiedMessage)

// OnMessage allows MessageHandler to satisfy the MessageProcessor interface.
func (h MessageHandler) OnMessage(msg *UnifiedMessage) {
	h(msg)
}

// MessageProcessor defines the interface for components that can process incoming messages.
type MessageProcessor interface {
	OnMessage(msg *UnifiedMessage)
}

// ResponderAware defines an interface for components that require a MessageResponder to be injected.
type ResponderAware interface {
	SetResponder(responder MessageResponder)
}

// GatewayHandler is a composite interface for components that handle incoming
// messages AND are aware of the responder (e.g., ChatHandler).
type GatewayHandler interface {
	MessageProcessor
	ResponderAware
}
