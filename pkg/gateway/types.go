package gateway

import (
	"genesis/pkg/api"
)

// Re-export types from api package via aliases to maintain backward compatibility
// during the refactor.
type Channel = api.Channel
type SignalingChannel = api.SignalingChannel
type MessageResponder = api.MessageResponder
type ChannelContext = api.ChannelContext
type UnifiedMessage = api.UnifiedMessage
type FileAttachment = api.FileAttachment
type SessionContext = api.SessionContext

// MessageHandler is still defined here as a function type, or can be aliased.
type MessageHandler = api.MessageHandler
