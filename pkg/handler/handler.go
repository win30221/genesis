package handler

import (
	"context"
	"fmt"
	"genesis/pkg/config"
	"genesis/pkg/gateway"
	"genesis/pkg/llm"
	"log"
	"time"
)

// ChatHandler 負責管理單次對話的處理流程與狀態
type ChatHandler struct {
	client  llm.LLMClient
	gw      *gateway.GatewayManager
	history *llm.ChatHistory
	config  *config.Config
}

// NewMessageHandler 建立並初始化 ChatHandler
func NewMessageHandler(client llm.LLMClient, gw *gateway.GatewayManager, cfg *config.Config, history *llm.ChatHistory) func(*gateway.UnifiedMessage) {
	h := &ChatHandler{
		client:  client,
		gw:      gw,
		history: history,
		config:  cfg,
	}

	h.initializeHistory()

	return h.OnMessage
}

// initializeHistory 確保系統提示詞已載入
func (h *ChatHandler) initializeHistory() {
	if len(h.history.GetMessages()) == 0 && h.config.SystemPrompt != "" {
		h.history.Add(llm.NewSystemMessage(h.config.SystemPrompt))
	}
}

// OnMessage 處理接收到的使用者訊息 (核心入口)
func (h *ChatHandler) OnMessage(msg *gateway.UnifiedMessage) {
	log.Printf("📩 Msg from [%s] %s: %s (files: %d)\n", msg.Session.ChannelID, msg.Session.Username, msg.Content, len(msg.Files))

	// 1. 建立使用者訊息（支援多模態）
	userMsg := llm.Message{
		Role:    "user",
		Content: []llm.ContentBlock{},
	}

	// 添加文字內容
	if msg.Content != "" {
		userMsg.Content = append(userMsg.Content, llm.NewTextBlock(msg.Content))
	}

	// 添加圖片附件
	for _, file := range msg.Files {
		userMsg.Content = append(userMsg.Content, llm.NewImageBlock(file.Data, file.MimeType))
		log.Printf("📎 Attached file: %s (%s, %d bytes)", file.Filename, file.MimeType, len(file.Data))
	}

	// 儲存使用者訊息
	h.history.Add(userMsg)

	// 2. 呼叫 LLM 並處理串流
	assistantMsg := h.processLLMStream(msg)

	// 3. 紀錄 AI 回應
	if len(assistantMsg.Content) > 0 {
		h.history.Add(assistantMsg)
	}
}

// processLLMStream 處理 LLM 呼叫、思考中指示器以及串流轉發
func (h *ChatHandler) processLLMStream(msg *gateway.UnifiedMessage) llm.Message {
	timeout := time.Duration(h.config.System.LLMTimeoutMin) * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 設定「思考中」計時器
	thinkingSent := false
	delay := time.Duration(h.config.System.ThinkingInitDelayMs) * time.Millisecond
	initTimer := time.AfterFunc(delay, func() {
		h.gw.SendSignal(msg.Session, "thinking")
		thinkingSent = true
	})

	chunkCh, err := h.client.StreamChat(ctx, h.history.GetMessages())
	initTimer.Stop()

	if err != nil {
		log.Printf("Error calling LLM Stream: %v\n", err)
		h.gw.SendReply(msg.Session, fmt.Sprintf("❌ Error: %v", err))
		return llm.Message{}
	}

	// 準備轉發給系統的串流 Channel
	blockCh := make(chan llm.ContentBlock, 100)
	go func() {
		if err := h.gw.StreamReply(msg.Session, blockCh); err != nil {
			log.Printf("Failed to stream reply: %v\n", err)
		}
	}()
	defer close(blockCh)

	// 處理 chunks
	return h.collectChunks(msg.Session, chunkCh, blockCh, thinkingSent)
}

// collectChunks 負責從 LLM 讀取 StreamChunk 並累積成完整訊息
func (h *ChatHandler) collectChunks(session gateway.SessionContext, chunkCh <-chan llm.StreamChunk, blockCh chan<- llm.ContentBlock, alreadySentThinking bool) llm.Message {
	var textContent string
	var thinkingContent string
	firstChunkReceived := false

	// 第一階段：等待第一個 Chunk 或觸發「思考中」計時器
	var thinkingTimer *time.Timer
	var timerChan <-chan time.Time
	if !alreadySentThinking {
		delay := time.Duration(h.config.System.ThinkingTokenDelayMs) * time.Millisecond
		thinkingTimer = time.NewTimer(delay)
		defer thinkingTimer.Stop()
		timerChan = thinkingTimer.C
	}

	for !firstChunkReceived {
		select {
		case chunk, ok := <-chunkCh:
			if !ok {
				return llm.Message{} // Channel已關閉且沒內容
			}
			firstChunkReceived = true
			if thinkingTimer != nil {
				thinkingTimer.Stop()
			}
			// 處理第一個 chunk
			textContent, thinkingContent = h.processChunk(chunk, textContent, thinkingContent, blockCh)

		case <-timerChan:
			h.gw.SendSignal(session, "thinking")
			timerChan = nil // 只送一次
		}
	}

	// 第二階段：處理剩餘的 chunks
	for chunk := range chunkCh {
		textContent, thinkingContent = h.processChunk(chunk, textContent, thinkingContent, blockCh)

		if chunk.IsFinal {
			break
		}
	}

	// 返回完整訊息（包含 thinking 和 text）
	msg := llm.Message{
		Role:    "assistant",
		Content: []llm.ContentBlock{},
	}

	if thinkingContent != "" {
		msg.Content = append(msg.Content, llm.NewThinkingBlock(thinkingContent))
	}

	if textContent != "" {
		msg.Content = append(msg.Content, llm.NewTextBlock(textContent))
	}

	return msg
}

// processChunk 處理單個 chunk 並累積內容
func (h *ChatHandler) processChunk(chunk llm.StreamChunk, currentText, currentThinking string, blockCh chan<- llm.ContentBlock) (string, string) {
	for _, block := range chunk.ContentBlocks {
		switch block.Type {
		case "text":
			currentText += block.Text
			// 直接發送 ContentBlock
			blockCh <- block

		case "thinking":
			currentThinking += block.Text
			if h.config.System.ShowThinking {
				// 直接發送 ContentBlock
				blockCh <- block
			}
		}
	}

	return currentText, currentThinking
}
