package llm

import (
	"sync"
)

// ChatHistory is a concurrency-safe manager for the linear conversation log.
// It acts as the "short-term memory" for a single conversation session,
// accumulating messages from all roles (user, system, assistant, tool).
type ChatHistory struct {
	messages []Message    // In-memory slice of messages in chronological order
	mu       sync.RWMutex // Read-Write mutex to protect concurrent access from multiple goroutines
}

// NewChatHistory initializes a fresh ChatHistory manager with an empty message set.
func NewChatHistory() *ChatHistory {
	return &ChatHistory{
		messages: make([]Message, 0),
	}
}

// Add appends a new Message to the end of the conversation history.
// This operation is protected by a write lock.
func (h *ChatHistory) Add(msg Message) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.messages = append(h.messages, msg)
}

// GetMessages returns a deep-copy of the current conversation history.
// By returning a copy, it ensures that callers can safely iterate or
// manipulate the list without affecting the live history or causing race
// conditions during subsequent appends.
func (h *ChatHistory) GetMessages() []Message {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Perform a thread-safe copy of the slice
	cp := make([]Message, len(h.messages))
	copy(cp, h.messages)
	return cp
}
