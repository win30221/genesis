package gateway

import (
	"fmt"
	"genesis/pkg/llm"
	"genesis/pkg/monitor"
	"log"
	"sync"
	"time"
)

// MessageHandler 是一個回呼函數型別，用於處理從 Gateway 接收到的標準化訊息
type MessageHandler func(msg *UnifiedMessage)

// GatewayManager 負責管理所有的 Channels 並統一路由訊息
type GatewayManager struct {
	channels      map[string]Channel
	msgHandler    MessageHandler
	monitor       monitor.Monitor // 監控器
	channelBuffer int             // 內部 Channel 緩衝大小
	mu            sync.RWMutex
}

// NewGatewayManager 建立一個新的 GatewayManager
func NewGatewayManager() *GatewayManager {
	return &GatewayManager{
		channels:      make(map[string]Channel),
		channelBuffer: 100, // 預設值
	}
}

// SetChannelBuffer 設定內部的 Channel 緩衝大小
func (g *GatewayManager) SetChannelBuffer(size int) {
	if size > 0 {
		g.channelBuffer = size
	}
}

// SetMessageHandler 設定處理訊息的核心邏輯 (通常是 LLM 處理函式)
func (g *GatewayManager) SetMessageHandler(handler MessageHandler) {
	g.msgHandler = handler
}

// SetMonitor 設定監控器
func (g *GatewayManager) SetMonitor(m monitor.Monitor) {
	g.monitor = m
}

// Register 註冊一個 Channel
func (g *GatewayManager) Register(c Channel) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.channels[c.ID()] = c
}

// GetChannel 取得特定的 Channel (通常用於主動發送訊息)
func (g *GatewayManager) GetChannel(id string) (Channel, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	c, ok := g.channels[id]
	return c, ok
}

// StartAll 啟動所有已註冊的 Channels
func (g *GatewayManager) StartAll() error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	for id, c := range g.channels {
		log.Printf("Starting channel: %s", id)
		// 啟動 Channel，並傳入 self 作為 Context
		if err := c.Start(g); err != nil {
			return fmt.Errorf("failed to start channel %s: %w", id, err)
		}
	}
	return nil
}

// StopAll 停止所有 Channels
func (g *GatewayManager) StopAll() {
	g.mu.RLock()
	defer g.mu.RUnlock()

	for id, c := range g.channels {
		log.Printf("Stopping channel: %s", id)
		if err := c.Stop(); err != nil {
			log.Printf("Error stopping channel %s: %v", id, err)
		}
	}
}

// SendReply 統一的回覆介面，透過 Channel 介面送回訊息
func (g *GatewayManager) SendReply(session SessionContext, content string) error {
	log.Printf("[Gateway] -> Reply to %s (%s): %s", session.ChannelID, session.Username, content)

	// 廣播到監控器
	if g.monitor != nil {
		g.monitor.OnMessage(monitor.MonitorMessage{
			Timestamp:   time.Now(),
			MessageType: "ASSISTANT",
			ChannelID:   session.ChannelID,
			Username:    session.Username,
			Content:     content,
		})
	}

	c, ok := g.GetChannel(session.ChannelID)
	if !ok {
		return fmt.Errorf("channel %s not found", session.ChannelID)
	}
	return c.Send(session, content)
}

// SendSignal 發送一個控制訊號 (如 thinking) 到 Channel
func (g *GatewayManager) SendSignal(session SessionContext, signal string) error {
	c, ok := g.GetChannel(session.ChannelID)
	if !ok {
		return fmt.Errorf("channel %s not found", session.ChannelID)
	}

	// 檢查 Channel 是否支援訊號介面
	if sc, ok := c.(SignalingChannel); ok {
		log.Printf("[Gateway] -> Signal to %s (%s): %s", session.ChannelID, session.Username, signal)
		return sc.SendSignal(session, signal)
	}

	// 不支援的通道安靜地忽略
	return nil
}

// StreamReply 統一的串流回覆介面
func (g *GatewayManager) StreamReply(session SessionContext, blocks <-chan llm.ContentBlock) error {
	// log.Printf("[Gateway] -> Stream Reply to %s (%s) started", session.ChannelID, session.Username)

	c, ok := g.GetChannel(session.ChannelID)
	if !ok {
		return fmt.Errorf("channel %s not found", session.ChannelID)
	}

	// 建立一個新的 channel 來包裝原始 blocks，以便收集完整內容廣播到監控器
	wrappedBlocks := make(chan llm.ContentBlock, g.channelBuffer)
	var fullContent string

	go func() {
		defer close(wrappedBlocks)
		for block := range blocks {
			// 只收集 text 類型的內容用於監控
			if block.Type == "text" {
				fullContent += block.Text
			}
			wrappedBlocks <- block
		}
		// 串流結束後，廣播完整訊息到監控器
		if fullContent != "" && g.monitor != nil {
			g.monitor.OnMessage(monitor.MonitorMessage{
				Timestamp:   time.Now(),
				MessageType: "ASSISTANT",
				ChannelID:   session.ChannelID,
				Username:    session.Username,
				Content:     fullContent,
			})
		}
	}()

	return c.Stream(session, wrappedBlocks)
}

// OnMessage 實作 ChannelContext 介面，接收來自 Channel 的訊息
func (g *GatewayManager) OnMessage(channelID string, msg *UnifiedMessage) {
	// 增強型 Log
	log.Printf("[Gateway] <- Received from %s [%s(%s)]: %s",
		channelID, msg.Session.Username, msg.Session.UserID, msg.Content)

	// 廣播到監控器
	if g.monitor != nil {
		g.monitor.OnMessage(monitor.MonitorMessage{
			Timestamp:   time.Now(),
			MessageType: "USER",
			ChannelID:   channelID,
			Username:    msg.Session.Username,
			Content:     msg.Content,
		})
	}

	if g.msgHandler != nil {
		// 將訊息轉發給核心處理器 (LLM)
		g.msgHandler(msg)
	} else {
		log.Println("[Gateway] Warning: No message handler set")
	}
}
