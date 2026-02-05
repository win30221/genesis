package llm

import (
	"sync"
)

// ChatHistory 管理對話歷史，支援滑動窗口 (Sliding Window) 限制長度
type ChatHistory struct {
	messages []Message
	mu       sync.RWMutex
}

// NewChatHistory 建立一個新的歷史管理員
func NewChatHistory() *ChatHistory {
	return &ChatHistory{
		messages: make([]Message, 0),
	}
}

// Add 加入一則新訊息，若超過長度則移除最舊的
func (h *ChatHistory) Add(msg Message) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.messages = append(h.messages, msg)
}

// GetMessages 取得目前的對話歷史副本
func (h *ChatHistory) GetMessages() []Message {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// 返回副本
	cp := make([]Message, len(h.messages))
	copy(cp, h.messages)
	return cp
}
