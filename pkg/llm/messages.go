package llm

import (
	"encoding/base64"
	"time"
)

//----------------------------------------------------------------
// Message - 通用訊息結構（對齊 pi-agent-core）
//----------------------------------------------------------------

// Message 表示一條對話訊息
type Message struct {
	Role      string         `json:"role"`    // "user", "assistant", "system", "tool"
	Content   []ContentBlock `json:"content"` // 內容區塊陣列
	Timestamp int64          `json:"timestamp,omitempty"`

	// ToolUse 包含 LLM 產生的工具調用請求（僅 role: assistant 時有效）
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// ToolCallID 關聯此訊息所屬的工具調用 ID（僅 role: tool 時有效）
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ToolCall 表示 LLM 產生的工具調用請求
type ToolCall struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Function FunctionCall `json:"function"`

	// Meta 保存提供者特定的元數據（例如 Gemini 的 thought_signature）
	// 不會被序列化到 JSON，僅用於內部傳遞
	Meta map[string]any `json:"-"`
}

// FunctionCall 包含具體的工具名稱與參數
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON 字串
}

//----------------------------------------------------------------
// ContentBlock - 統一的內容區塊
//----------------------------------------------------------------

// ContentBlock 表示訊息中的一個內容區塊
// 支援類型：text, thinking, image（未來可擴展 audio, video 等）
type ContentBlock struct {
	Type string `json:"type"` // "text", "thinking", "image"

	// Text 相關（type: "text" | "thinking"）
	Text string `json:"text,omitempty"`

	// Image 相關（type: "image"）
	Source *ImageSource `json:"source,omitempty"`
}

//----------------------------------------------------------------
// ImageSource - 圖片來源
//----------------------------------------------------------------

// ImageSource 表示圖片的來源資料
type ImageSource struct {
	Type      string `json:"type"`       // "base64" | "url"
	MediaType string `json:"media_type"` // "image/jpeg", "image/png", etc.
	Data      []byte `json:"-"`          // 原始位元組資料（不序列化）
	URL       string `json:"url,omitempty"`
}

// MarshalJSON 自訂 JSON 序列化（將 Data 轉為 base64）
func (is *ImageSource) MarshalJSON() ([]byte, error) {
	type Alias ImageSource
	if is.Type == "base64" && len(is.Data) > 0 {
		return []byte(`{"type":"base64","media_type":"` + is.MediaType + `","data":"` + base64.StdEncoding.EncodeToString(is.Data) + `"}`), nil
	}
	return []byte(`{"type":"` + is.Type + `","media_type":"` + is.MediaType + `","url":"` + is.URL + `"}`), nil
}

// UnmarshalJSON 自訂 JSON 反序列化（將 base64 轉為 Data）
func (is *ImageSource) UnmarshalJSON(data []byte) error {
	type Alias ImageSource
	aux := &struct {
		DataBase64 string `json:"data"`
		*Alias
	}{
		Alias: (*Alias)(is),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.DataBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(aux.DataBase64)
		if err != nil {
			return err
		}
		is.Data = decoded
	}

	return nil
}

//----------------------------------------------------------------
// StreamChunk - 串流 chunk 結構
//----------------------------------------------------------------

// StreamChunk 表示 LLM 串流回應的一個 chunk（增量式）
type StreamChunk struct {
	// 內容區塊（增量，只包含新增的內容）
	ContentBlocks []ContentBlock `json:"content_blocks,omitempty"`

	// 工具調用（增量）
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// 是否為最後一個 chunk
	IsFinal bool `json:"is_final"`

	// 停止原因（只在最後 chunk 有值）
	FinishReason string `json:"finish_reason,omitempty"`

	// 用量統計（可能在中間 chunk 就有，但最後 chunk 一定有）
	Usage *LLMUsage `json:"usage,omitempty"`
}

//----------------------------------------------------------------
// Helper Functions - Message
//----------------------------------------------------------------

// NewTextMessage 建立純文字訊息
func NewTextMessage(role, text string) Message {
	return Message{
		Role: role,
		Content: []ContentBlock{{
			Type: "text",
			Text: text,
		}},
		Timestamp: time.Now().Unix(),
	}
}

// NewSystemMessage 建立系統訊息
func NewSystemMessage(text string) Message {
	return NewTextMessage("system", text)
}

// NewUserMessage 建立使用者訊息
func NewUserMessage(text string) Message {
	return NewTextMessage("user", text)
}

// NewAssistantMessage 建立助理訊息
func NewAssistantMessage(text string) Message {
	return NewTextMessage("assistant", text)
}

// AddContentBlock 添加內容區塊到訊息
func (m *Message) AddContentBlock(block ContentBlock) {
	m.Content = append(m.Content, block)
}

// GetTextContent 提取所有文字內容（排除 thinking）
func (m *Message) GetTextContent() string {
	var result string
	for _, block := range m.Content {
		if block.Type == "text" {
			result += block.Text
		}
	}
	return result
}

// GetThinkingContent 提取所有思考內容
func (m *Message) GetThinkingContent() string {
	var result string
	for _, block := range m.Content {
		if block.Type == "thinking" {
			result += block.Text
		}
	}
	return result
}

// FilterBlocks 過濾指定類型的區塊
func (m *Message) FilterBlocks(blockType string) []ContentBlock {
	var filtered []ContentBlock
	for _, block := range m.Content {
		if block.Type == blockType {
			filtered = append(filtered, block)
		}
	}
	return filtered
}

// HasImages 判斷訊息是否包含圖片
func (m *Message) HasImages() bool {
	for _, block := range m.Content {
		if block.Type == "image" {
			return true
		}
	}
	return false
}

//----------------------------------------------------------------
// Helper Functions - ContentBlock
//----------------------------------------------------------------

// NewTextBlock 建立文字區塊
func NewTextBlock(text string) ContentBlock {
	return ContentBlock{
		Type: "text",
		Text: text,
	}
}

// NewThinkingBlock 建立思考區塊
func NewThinkingBlock(text string) ContentBlock {
	return ContentBlock{
		Type: "thinking",
		Text: text,
	}
}

// NewImageBlock 建立圖片區塊（base64）
func NewImageBlock(data []byte, mimeType string) ContentBlock {
	return ContentBlock{
		Type: "image",
		Source: &ImageSource{
			Type:      "base64",
			MediaType: mimeType,
			Data:      data,
		},
	}
}

// NewImageBlockFromURL 建立圖片區塊（URL）
func NewImageBlockFromURL(url, mimeType string) ContentBlock {
	return ContentBlock{
		Type: "image",
		Source: &ImageSource{
			Type:      "url",
			MediaType: mimeType,
			URL:       url,
		},
	}
}

//----------------------------------------------------------------
// Helper Functions - StreamChunk
//----------------------------------------------------------------

// NewTextChunk 建立文字 chunk
func NewTextChunk(text string) StreamChunk {
	return StreamChunk{
		ContentBlocks: []ContentBlock{{
			Type: "text",
			Text: text,
		}},
	}
}

// NewThinkingChunk 建立思考 chunk
func NewThinkingChunk(text string) StreamChunk {
	return StreamChunk{
		ContentBlocks: []ContentBlock{{
			Type: "thinking",
			Text: text,
		}},
	}
}

// NewFinalChunk 建立最終 chunk（帶用量統計）
func NewFinalChunk(reason string, usage *LLMUsage) StreamChunk {
	return StreamChunk{
		IsFinal:      true,
		FinishReason: reason,
		Usage:        usage,
	}
}
