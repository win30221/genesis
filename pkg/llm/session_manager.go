package llm

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

var filenameSafeRegex = regexp.MustCompile(`[^a-zA-Z0-9_\-]`)

// SessionManager manages multiple conversation histories isolated by session ID.
type SessionManager struct {
	histories map[string]*ChatHistory
	storage   string
	mu        sync.RWMutex
}

// NewSessionManager initializes a SessionManager with a specific storage directory.
func NewSessionManager(storage string) *SessionManager {
	if storage != "" {
		os.MkdirAll(storage, 0755)
	}
	return &SessionManager{
		histories: make(map[string]*ChatHistory),
		storage:   storage,
	}
}

// GetHistory retrieves an existing ChatHistory for a session or creates/loads a new one.
func (sm *SessionManager) GetHistory(sessionID string) (*ChatHistory, error) {
	sm.mu.RLock()
	h, ok := sm.histories[sessionID]
	sm.mu.RUnlock()

	if ok {
		return h, nil
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Double check under lock
	if h, ok = sm.histories[sessionID]; ok {
		return h, nil
	}

	h = NewChatHistory()
	if sm.storage != "" {
		safeID := filenameSafeRegex.ReplaceAllString(sessionID, "_")
		historyPath := filepath.Join(sm.storage, fmt.Sprintf("history_%s.json", safeID))
		if err := h.Load(historyPath); err != nil {
			return nil, err
		}
	}

	sm.histories[sessionID] = h
	return h, nil
}

// SaveSession persists a specific session's history to disk.
func (sm *SessionManager) SaveSession(sessionID string) error {
	sm.mu.RLock()
	h, ok := sm.histories[sessionID]
	sm.mu.RUnlock()

	if !ok || sm.storage == "" {
		return nil
	}

	safeID := filenameSafeRegex.ReplaceAllString(sessionID, "_")
	historyPath := filepath.Join(sm.storage, fmt.Sprintf("history_%s.json", safeID))
	attachmentsDir := filepath.Join(sm.storage, "..", "attachments")

	h.ProcessImages(attachmentsDir)
	return h.Save(historyPath)
}
