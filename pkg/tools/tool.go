package tools

// Tool 定義工具介面,類似 OpenClaw 的 AgentTool
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any   // Properties in JSON Schema
	RequiredParameters() []string // Required fields in JSON Schema
	Execute(args map[string]any) (*ToolResult, error)
}

// ToolResult 工具執行結果
type ToolResult struct {
	Content []ContentBlock `json:"content"`
	Details map[string]any `json:"details,omitempty"`
}

// ContentBlock 內容區塊
type ContentBlock struct {
	Type             string `json:"type"` // "text" or "image"
	Text             string `json:"text,omitempty"`
	Data             string `json:"data,omitempty"` // base64 for images
	IsThought        bool   `json:"is_thought,omitempty"`
	ThoughtSignature []byte `json:"thought_signature,omitempty"`
}

// Message 對話訊息
type Message struct {
	Role    string             `json:"role"` // "user" or "assistant"
	Content []ContentBlock     `json:"content"`
	ToolUse []ToolUse          `json:"tool_use,omitempty"`
	Results []ToolResultWithID `json:"tool_results,omitempty"`
}

// ToolUse LLM 要求使用工具
type ToolUse struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Input            map[string]any `json:"input"`
	ThoughtSignature []byte         `json:"thought_signature,omitempty"`
}

// ToolResultWithID 帶 ID 的工具結果
type ToolResultWithID struct {
	ToolUseID string         `json:"tool_use_id"`
	ToolName  string         `json:"tool_name"` // Added for Gemini support
	Content   []ContentBlock `json:"content"`
	Details   map[string]any `json:"details,omitempty"`
}

// ToolRegistry 工具註冊表
type ToolRegistry struct {
	tools map[string]Tool
}

// NewToolRegistry 創建工具註冊表
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

// Register 註冊工具
func (tr *ToolRegistry) Register(tool Tool) {
	tr.tools[tool.Name()] = tool
}

// Unregister 註銷工具
func (tr *ToolRegistry) Unregister(name string) {
	delete(tr.tools, name)
}

// Get 獲取工具
func (tr *ToolRegistry) Get(name string) (Tool, bool) {
	tool, ok := tr.tools[name]
	return tool, ok
}

// GetAll 獲取所有工具
func (tr *ToolRegistry) GetAll() []Tool {
	tools := make([]Tool, 0, len(tr.tools))
	for _, tool := range tr.tools {
		tools = append(tools, tool)
	}
	return tools
}

// ToAnthropicFormat 轉換為 Anthropic API 格式
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
