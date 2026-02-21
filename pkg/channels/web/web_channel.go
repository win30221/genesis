package web

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"genesis/pkg/api"
	"genesis/pkg/llm"
	"genesis/pkg/utils"
	"log/slog"
	"net/http"
	"os"
	"sync"

	"github.com/gorilla/websocket"
	jsoniter "github.com/json-iterator/go"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for decoupled UI
	},
}

type WebConfig struct {
	Port int `json:"port"` // Default: 9453
}

type IncomingMessage struct {
	Text   string `json:"text"`
	Images []struct {
		Name string `json:"name"`
		Mime string `json:"mime"`
		Data string `json:"data"` // Base64 encoded
	} `json:"images"`
}

type SafeConn struct {
	*websocket.Conn
	mu sync.Mutex
}

func (sc *SafeConn) WriteMessage(messageType int, data []byte) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.Conn.WriteMessage(messageType, data)
}

type WebChannel struct {
	config      WebConfig
	server      *http.Server
	sessions    *llm.SessionManager  // Manager for fetching histories
	connections map[string]*SafeConn // Map UserID -> WS Connection
	mu          sync.RWMutex
}

func NewWebChannel(cfg WebConfig, sessions *llm.SessionManager) *WebChannel {
	return &WebChannel{
		config:      cfg,
		sessions:    sessions,
		connections: make(map[string]*SafeConn),
	}
}

func (c *WebChannel) ID() string {
	return "web"
}

func (c *WebChannel) Start(ctx api.ChannelContext) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c.handleWebSocket(w, r, ctx)
	})

	addr := fmt.Sprintf(":%d", c.config.Port)
	c.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	slog.Info("Web API listening", "port", c.config.Port)

	go func() {
		if err := c.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Web API server error", "error", err)
		}
	}()

	return nil
}

func (c *WebChannel) Stop() error {
	if c.server != nil {
		return c.server.Close()
	}
	return nil
}

func (c *WebChannel) Send(session api.SessionContext, message string) error {
	c.mu.RLock()
	conn, ok := c.connections[session.UserID]
	c.mu.RUnlock()

	if !ok {
		return fmt.Errorf("web user %s not connected", session.UserID)
	}

	return conn.WriteMessage(websocket.TextMessage, []byte(message))
}

// SendSignal implements the gateway.SignalingChannel interface
func (c *WebChannel) SendSignal(session api.SessionContext, signal string) error {
	c.mu.RLock()
	conn, ok := c.connections[session.UserID]
	c.mu.RUnlock()

	if !ok {
		return fmt.Errorf("web user %s not connected", session.UserID)
	}

	msg := map[string]string{
		"type":  "signal",
		"value": signal,
	}
	jsonData, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal signal: %w", err)
	}
	return conn.WriteMessage(websocket.TextMessage, jsonData)
}

// Stream implements gateway.Channel.Stream
func (c *WebChannel) Stream(session api.SessionContext, blocks <-chan llm.ContentBlock) error {
	c.mu.RLock()
	conn, ok := c.connections[session.UserID]
	c.mu.RUnlock()

	if !ok {
		return fmt.Errorf("web user %s not connected", session.UserID)
	}

	for block := range blocks {
		// Convert to JSON structure
		msg := map[string]interface{}{
			"type": block.Type,
		}

		if block.Type == llm.BlockTypeImage && block.Source != nil {
			if block.Source.Type == "base64" && len(block.Source.Data) > 0 {
				msg["data"] = base64.StdEncoding.EncodeToString(block.Source.Data)
				msg["mime"] = block.Source.MediaType
			} else if block.Source.Type == "file" && block.Source.Path != "" {
				fileData, err := os.ReadFile(block.Source.Path)
				if err == nil {
					msg["data"] = base64.StdEncoding.EncodeToString(fileData)
					msg["mime"] = block.Source.MediaType
				} else {
					slog.Error("Failed to read local image for stream", "path", block.Source.Path, "error", err)
				}
			} else if block.Source.Type == "url" {
				msg["url"] = block.Source.URL
			}
		} else {
			msg["text"] = block.Text
		}

		jsonData, err := json.Marshal(msg)
		if err != nil {
			slog.Error("Failed to marshal stream block", "error", err)
			continue
		}

		// Send JSON directly
		err = conn.WriteMessage(websocket.TextMessage, jsonData)
		if err != nil {
			return err
		}
	}

	// Send finish flag
	return conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"done"}`))
}

func (c *WebChannel) handleWebSocket(w http.ResponseWriter, r *http.Request, ctx api.ChannelContext) {
	rawConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WS Upgrade failed", "error", err)
		return
	}

	// Wrap connection
	conn := &SafeConn{Conn: rawConn}

	// Simple UserID based on RemoteAddr or random
	userID := r.RemoteAddr

	// Register connection
	c.mu.Lock()
	c.connections[userID] = conn
	c.mu.Unlock()

	// Send history immediately (if any)
	// For Web UI, we use "web_global" as the history sync key for now
	h, err := c.sessions.GetHistory("web_global")
	if err == nil {
		historyMsgs := h.GetMessagesForUI()
		if len(historyMsgs) > 0 {
			historyData := map[string]any{
				"type": "history",
				"data": historyMsgs,
			}
			historyJSON, err := json.Marshal(historyData)
			if err != nil {
				slog.Error("Failed to marshal history", "error", err)
			} else {
				conn.WriteMessage(websocket.TextMessage, historyJSON)
			}
		}
	}

	defer func() {
		c.mu.Lock()
		delete(c.connections, userID)
		c.mu.Unlock()
		conn.Close()
	}()

	// Init Session Context
	session := api.SessionContext{
		ChannelID: "web",
		UserID:    userID,
		ChatID:    "global", // Currently hardcoded to global for Web UI
		Username:  "WebUser",
	}

	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var content string
		var files []api.FileAttachment

		// Try to parse as JSON (includes images)
		var incoming IncomingMessage
		if err := json.Unmarshal(msgBytes, &incoming); err == nil {
			content = incoming.Text
			for _, img := range incoming.Images {
				// Base64 decode
				data, err := base64.StdEncoding.DecodeString(img.Data)
				if err != nil {
					slog.Error("Failed to decode base64 image", "name", img.Name, "error", err)
					continue
				}

				// Ensure attachments directory exists
				attachmentsDir := "data/attachments"
				if err := os.MkdirAll(attachmentsDir, 0755); err != nil {
					slog.Error("Failed to create attachments dir", "error", err)
					continue
				}

				// Generate unique local path based on content hash (SHA-256)
				hash := sha256.Sum256(data)
				// Prefix with 8-char hex timestamp for easy expiration checks
				_, ext := utils.DetectMimeAndExt(data)
				localFileName := fmt.Sprintf("%s%s%s", utils.GenerateTimestampPrefix(), hex.EncodeToString(hash[:]), ext)
				localPath := fmt.Sprintf("%s/%s", attachmentsDir, localFileName)

				// Write directly to disk (if it doesn't already exist to save IO)
				if _, err := os.Stat(localPath); os.IsNotExist(err) {
					if err := os.WriteFile(localPath, data, 0644); err != nil {
						slog.Error("Failed to save image to disk", "path", localPath, "error", err)
						continue
					}
				}

				files = append(files, api.FileAttachment{
					Filename: img.Name,
					MimeType: img.Mime,
					Data:     nil, // Don't hold in memory
					Path:     localPath,
				})
				slog.Debug("Received and saved image directly to disk", "name", img.Name, "path", localPath)
			}
		} else {
			// Fallback: treat as plain text (backward compatibility)
			content = string(msgBytes)
		}

		// Send to Gateway
		unifiedMsg := &api.UnifiedMessage{
			Session: session,
			Content: content,
			Files:   files,
		}
		ctx.OnMessage(c.ID(), unifiedMsg)
	}
}
