package tools

import "context"

// Tool defines the structural interface for any capability that the AI Agent
// can execute. It includes metadata for prompt injection (JSON Schema)
// and the execution logic itself.
type Tool interface {
	// Name returns the unique identifier for the tool (e.g., "os_command").
	Name() string
	// Description provides a detailed prompt for the LLM to understand when to use the tool.
	Description() string
	// Parameters returns the JSON Schema "properties" part for the tool's input.
	Parameters() map[string]any
	// RequiredParameters returns a list of mandatory field names for the input object.
	RequiredParameters() []string
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
	Type             string `json:"type"`                        // Data format: "text" or "image"
	Text             string `json:"text,omitempty"`              // String content (for text type)
	Data             string `json:"data,omitempty"`              // Base64 encoded image data (for image type)
	MimeType         string `json:"mime_type,omitempty"`         // MIME type for image data (e.g., "image/jpeg")
	IsThought        bool   `json:"is_thought,omitempty"`        // Whether this represents internal reasoning (mostly for internal passing)
	ThoughtSignature []byte `json:"thought_signature,omitempty"` // Cryptographic or provider-specific token signature
}

// ToolRegistry acts as a central inventory for all tools available to the Agent.
// It provides helper methods to convert tool schemas into formats compatible
// with various LLM providers (Gemini, Anthropic, Ollama).
type ToolRegistry struct {
	tools map[string]Tool // Internal map of tool name to implementation
}

// NewToolRegistry creates a new tool registry
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry
func (tr *ToolRegistry) Register(tool Tool) {
	tr.tools[tool.Name()] = tool
}

// Unregister removes a tool from the registry
func (tr *ToolRegistry) Unregister(name string) {
	delete(tr.tools, name)
}

// Get retrieves a tool by name
func (tr *ToolRegistry) Get(name string) (Tool, bool) {
	tool, ok := tr.tools[name]
	return tool, ok
}

// GetAll returns all registered tools
func (tr *ToolRegistry) GetAll() []Tool {
	tools := make([]Tool, 0, len(tr.tools))
	for _, tool := range tr.tools {
		tools = append(tools, tool)
	}
	return tools
}

// ToAnthropicFormat converts to Anthropic API tool format
func (tr *ToolRegistry) ToAnthropicFormat() []any {
	tools := make([]any, 0, len(tr.tools))
	for _, tool := range tr.tools {
		tools = append(tools, map[string]any{
			"name":        tool.Name(),
			"description": tool.Description(),
			"input_schema": map[string]any{
				"type":       "object",
				"properties": tool.Parameters(),
			},
		})
	}
	return tools
}

// ToGeminiFormat converts to Google Gemini API tool format
func (tr *ToolRegistry) ToGeminiFormat() any {
	var fds []map[string]any
	for _, tool := range tr.tools {
		fds = append(fds, map[string]any{
			"name":        tool.Name(),
			"description": tool.Description(),
			"parameters": map[string]any{
				"type":       "object",
				"properties": tool.Parameters(),
				"required":   tool.RequiredParameters(),
			},
		})
	}
	return fds
}

// ToOllamaFormat converts to Ollama (OpenAI-compatible) API tool format
func (tr *ToolRegistry) ToOllamaFormat() []map[string]any {
	var tools []map[string]any
	for _, tool := range tr.tools {
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name(),
				"description": tool.Description(),
				"parameters": map[string]any{
					"type":       "object",
					"properties": tool.Parameters(),
					"required":   tool.RequiredParameters(),
				},
			},
		})
	}
	return tools
}
