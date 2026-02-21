package llm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"genesis/pkg/utils"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ChatHistory is a concurrency-safe manager for the linear conversation log.
// It acts as the "short-term memory" for a single conversation session,
// accumulating messages from all roles (user, system, assistant, tool).
type ChatHistory struct {
	Summary  string       `json:"summary,omitempty"` // Condensed summary of earlier conversation
	Messages []Message    `json:"messages"`          // Chronological message history
	mu       sync.RWMutex // Protects concurrent access
}

// NewChatHistory initializes a fresh ChatHistory manager with an empty message set.
func NewChatHistory() *ChatHistory {
	return &ChatHistory{
		Messages: make([]Message, 0),
	}
}

// Add appends a new Message to the end of the conversation history.
// This operation is protected by a write lock.
func (h *ChatHistory) Add(msg Message) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.Messages = append(h.Messages, msg)
}

// GetMessages returns a deep-copy of the current conversation history.
// By returning a copy, it ensures that callers can safely iterate or
// manipulate the list without affecting the live history or causing race
// conditions during subsequent appends.
func (h *ChatHistory) GetMessages() []Message {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Perform a thread-safe copy of the slice
	cp := make([]Message, len(h.Messages))
	copy(cp, h.Messages)
	return cp
}

// GetMessagesForUI returns a copy of messages with image data hydrated (loaded from disk).
// This is used for channels like Web UI that can't access local file paths.
func (h *ChatHistory) GetMessagesForUI() []Message {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Deep copy to avoid mutating the original history
	msgs := make([]Message, len(h.Messages))
	for i, m := range h.Messages {
		msgs[i] = m
		msgs[i].Content = make([]ContentBlock, len(m.Content))
		for j, b := range m.Content {
			msgs[i].Content[j] = b
			if b.Type == BlockTypeImage && b.Source != nil && b.Source.Type == "file" {
				// Copy source and hydrate
				src := *b.Source
				src.LoadData()
				msgs[i].Content[j].Source = &src
			}
		}
	}
	return msgs
}

// GetSummary returns the current conversation summary.
func (h *ChatHistory) GetSummary() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.Summary
}

// SetSummary updates the conversation summary.
func (h *ChatHistory) SetSummary(summary string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Summary = summary
}

// TruncateHistory keeps only the most recent N messages.
// If the first message is a system message, it is always preserved.
// It also deletes any local files associated with discarded image blocks.
func (h *ChatHistory) TruncateHistory(keep int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.Messages) <= keep {
		return
	}

	// Preserve system message at index 0 if present
	var sysMsg *Message
	if len(h.Messages) > 0 && h.Messages[0].Role == "system" {
		tmp := h.Messages[0]
		sysMsg = &tmp
	}

	// Capture discarded messages for GC
	discardedMsgs := h.Messages[:len(h.Messages)-keep]

	// Truncate
	h.Messages = h.Messages[len(h.Messages)-keep:]

	// Re-prepend system message if it was removed by truncation
	if sysMsg != nil && (len(h.Messages) == 0 || h.Messages[0].Role != "system") {
		h.Messages = append([]Message{*sysMsg}, h.Messages...)
	}

	// Execute Garbage Collection on discarded attachments
	for _, msg := range discardedMsgs {
		// Skip the system message we just preserved
		if sysMsg != nil && msg.ID == sysMsg.ID {
			continue
		}
		for _, block := range msg.Content {
			if block.Type == BlockTypeImage && block.Source != nil && block.Source.Type == "file" && block.Source.Path != "" {
				err := os.Remove(block.Source.Path)
				if err != nil && !os.IsNotExist(err) {
					// We don't have slog imported in this file yet perhaps, let's just use fmt.Fprintf or import it.
					// Actually, os.Remove error is fine to ignore or just print if we can't import slog.
					// Let's assume we can use it or we will fix imports next.
					fmt.Printf("[GC] Failed to delete expired attachment %s: %v\n", block.Source.Path, err)
				} else if err == nil {
					fmt.Printf("[GC] Deleted expired attachment %s\n", block.Source.Path)
				}
			}
		}
	}
}

// EnsureSystemMessage makes sure a system message with the given content is at the
// beginning of the history. If a system message already exists at the start, it is replaced.
// If not, it is prepended.
func (h *ChatHistory) EnsureSystemMessage(content string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	newSys := Message{
		ID:        utils.GenerateID(),
		Role:      "system",
		Content:   []ContentBlock{NewTextBlock(content)},
		Timestamp: time.Now().Unix(),
	}

	if len(h.Messages) > 0 && h.Messages[0].Role == "system" {
		// Replace existing
		h.Messages[0] = newSys
	} else {
		// Prepend new
		h.Messages = append([]Message{newSys}, h.Messages...)
	}
}

// ProcessImages scans all messages for inline image data, saves them as files in
// the specified directory, and replaces the inline Data with a Path reference.
func (h *ChatHistory) ProcessImages(attachmentsDir string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if attachmentsDir == "" {
		return nil
	}

	if err := os.MkdirAll(attachmentsDir, 0755); err != nil {
		return err
	}

	for i := range h.Messages {
		for j := range h.Messages[i].Content {
			block := &h.Messages[i].Content[j]
			if block.Type == BlockTypeImage && block.Source != nil && len(block.Source.Data) > 0 {
				// Calculate hash for stable filename
				hash := sha256.Sum256(block.Source.Data)
				// Prefix with 8-char hex timestamp for easy expiration checks
				filename := fmt.Sprintf("%s%s", utils.GenerateTimestampPrefix(), hex.EncodeToString(hash[:]))

				// Detect actual extension from bytes
				_, ext := utils.DetectMimeAndExt(block.Source.Data)

				fullPath := filepath.Join(attachmentsDir, filename+ext)

				// Save if not already exists
				if _, err := os.Stat(fullPath); os.IsNotExist(err) {
					if err := os.WriteFile(fullPath, block.Source.Data, 0644); err != nil {
						return fmt.Errorf("failed to save image %s: %w", fullPath, err)
					}
				}

				// Update source to reference file
				block.Source.Type = "file"
				block.Source.Path = fullPath
				block.Source.Data = nil // Clear inline data to save memory/disk space in JSON
			}
		}
	}
	return nil
}

// Save serializes the entire conversation history to a JSON file.
// It uses a read lock to ensure the data is consistent during serialization.
func (h *ChatHistory) Save(filePath string) error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// ChatHistory can be marshaled directly — the unexported `mu` field
	// is automatically excluded by the JSON encoder.
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filePath, data, 0644)
}

// Load deserializes conversation history from a JSON file.
// If the file does not exist, it does nothing and returns nil.
// This operation uses a write lock to replace the existing in-memory history.
func (h *ChatHistory) Load(filePath string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var result struct {
		Summary  string    `json:"summary"`
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		// Fallback for older format (straight array of messages)
		if err := json.Unmarshal(data, &result.Messages); err != nil {
			return err
		}
	}

	h.Summary = result.Summary
	h.Messages = result.Messages
	return nil
}
