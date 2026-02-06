package web

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"genesis/pkg/gateway"
	"genesis/pkg/llm"
	"log"
	"net/http"
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
	Port int `json:"port"` // Default: 8080
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
	gw          *gateway.GatewayManager
	history     *llm.ChatHistory     // Shared history
	connections map[string]*SafeConn // Map UserID -> WS Connection
	mu          sync.RWMutex
}

func NewWebChannel(cfg WebConfig, gw *gateway.GatewayManager, history *llm.ChatHistory) *WebChannel {
	return &WebChannel{
		config:      cfg,
		gw:          gw,
		history:     history,
		connections: make(map[string]*SafeConn),
	}
}

func (c *WebChannel) ID() string {
	return "web"
}

func (c *WebChannel) Start(ctx gateway.ChannelContext) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c.handleWebSocket(w, r, ctx)
	})

	addr := fmt.Sprintf(":%d", c.config.Port)
	c.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	log.Printf("🌐 Web API listening on :%d/ws", c.config.Port)

	go func() {
		if err := c.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("❌ Web API server error: %v", err)
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

func (c *WebChannel) Send(session gateway.SessionContext, message string) error {
	c.mu.RLock()
	conn, ok := c.connections[session.UserID]
	c.mu.RUnlock()

	if !ok {
		return fmt.Errorf("web user %s not connected", session.UserID)
	}

	return conn.WriteMessage(websocket.TextMessage, []byte(message))
}

// SendSignal 實作 gateway.SignalingChannel 介面
func (c *WebChannel) SendSignal(session gateway.SessionContext, signal string) error {
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
	jsonData, _ := json.Marshal(msg)
	return conn.WriteMessage(websocket.TextMessage, jsonData)
}

// Stream 實作 gateway.Channel.Stream
func (c *WebChannel) Stream(session gateway.SessionContext, blocks <-chan llm.ContentBlock) error {
	c.mu.RLock()
	conn, ok := c.connections[session.UserID]
	c.mu.RUnlock()

	if !ok {
		return fmt.Errorf("web user %s not connected", session.UserID)
	}

	for block := range blocks {
		// 轉換為 JSON 結構
		msg := map[string]string{
			"type": block.Type,
			"text": block.Text,
		}
		jsonData, _ := json.Marshal(msg)

		// 直接發送 JSON
		err := conn.WriteMessage(websocket.TextMessage, jsonData)
		if err != nil {
			return err
		}
	}

	// 發送結束標記
	return conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"done"}`))
}

func (c *WebChannel) handleWebSocket(w http.ResponseWriter, r *http.Request, ctx gateway.ChannelContext) {
	rawConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("WS Upgrade failed:", err)
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

	// 立即發送歷史紀錄（若有）
	historyMsgs := c.history.GetMessages()
	if len(historyMsgs) > 0 {
		historyData := map[string]interface{}{
			"type": "history",
			"data": historyMsgs,
		}
		historyJSON, _ := json.Marshal(historyData)
		conn.WriteMessage(websocket.TextMessage, historyJSON)
	}

	defer func() {
		c.mu.Lock()
		delete(c.connections, userID)
		c.mu.Unlock()
		conn.Close()
	}()

	// Init Session Context
	session := gateway.SessionContext{
		ChannelID: "web",
		UserID:    userID,
		ChatID:    "global",
		Username:  "WebUser",
	}

	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var content string
		var files []gateway.FileAttachment

		// 嘗試解析為 JSON (包含圖片)
		var incoming IncomingMessage
		if err := json.Unmarshal(msgBytes, &incoming); err == nil {
			content = incoming.Text
			for _, img := range incoming.Images {
				// Base64 解碼
				data, err := base64.StdEncoding.DecodeString(img.Data)
				if err != nil {
					log.Printf("❌ Failed to decode base64 image %s: %v", img.Name, err)
					continue
				}

				files = append(files, gateway.FileAttachment{
					Filename: img.Name,
					MimeType: img.Mime,
					Data:     data,
				})
				log.Printf("📸 Received image: %s (%d bytes)", img.Name, len(data))
			}
		} else {
			// Fallback: 視為純文字 (向下相容)
			content = string(msgBytes)
		}

		// Send to Gateway
		unifiedMsg := &gateway.UnifiedMessage{
			Session: session,
			Content: content,
			Files:   files,
		}
		ctx.OnMessage(c.ID(), unifiedMsg)
	}
}
