package api

import (
	"context"
	"genesis/pkg/llm"
)

// AgentEngine defines the interface for the core reasoning engine.
type AgentEngine interface {
	HandleMessage(ctx context.Context, msg *UnifiedMessage, history *llm.ChatHistory) llm.Message
	SetResponder(responder MessageResponder)
	SetToolRegistry(tr ToolRegistry)
	RegisterTool(tools ...Tool)
}
