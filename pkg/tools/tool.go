package tools

import (
	"genesis/pkg/api"
	"sync"
)

// Re-export types from api package via aliases to maintain backward compatibility
type Tool = api.Tool
type ToolResult = api.ToolResult
type ContentBlock = api.ContentBlock

// ToolRegistry acts as a central inventory for all tools available to the Agent.
type ToolRegistry struct {
	mu    sync.RWMutex    // Protects concurrent access to the tools map
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
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.tools[tool.Name()] = tool
}

// Unregister removes a tool from the registry
func (tr *ToolRegistry) Unregister(name string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	delete(tr.tools, name)
}

// Get retrieves a tool by name
func (tr *ToolRegistry) Get(name string) (Tool, bool) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	tool, ok := tr.tools[name]
	return tool, ok
}

// GetAll returns all registered tools
func (tr *ToolRegistry) GetAll() []Tool {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	tools := make([]Tool, 0, len(tr.tools))
	for _, tool := range tr.tools {
		tools = append(tools, tool)
	}
	return tools
}
