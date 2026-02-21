package api

import (
	"context"
	"genesis/pkg/llm"
)

// Tool defines the structural interface for any capability that the AI Agent
// can execute. It includes metadata for prompt injection (JSON Schema)
// and the execution logic itself.
type Tool interface {
	llm.Tool
	// Execute performs the actual tool logic using the provided argument map.
	Execute(ctx context.Context, args map[string]any) (*ToolResult, error)
}

// ToolResult encapsulates the outcome of a tool execution.
// It can contain multiple content blocks (text logs, images) and
// arbitrary metadata for the handler to process.
type ToolResult struct {
	Content []ContentBlock `json:"content"`           // Ordered blocks of result data
	Details map[string]any `json:"details,omitempty"` // Arbitrary technical metadata
}

// ContentBlock is an atomic data unit within a ToolResult.
// It is designed to be converted into llm.ContentBlocks by the handler.
type ContentBlock struct {
	Type     string `json:"type"`                // Data format: "text" or "image"
	Text     string `json:"text,omitempty"`      // String content (for text type)
	Data     string `json:"data,omitempty"`      // Base64 encoded image data (for image type)
	MimeType string `json:"mime_type,omitempty"` // MIME type for image data (e.g., "image/jpeg")
}

// ToolRegistry defines the interface for managing and accessing tools.
type ToolRegistry interface {
	Register(tool Tool)
	Unregister(name string)
	Get(name string) (Tool, bool)
	GetAll() []Tool
}
