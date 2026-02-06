package handler

import (
	"context"
	"fmt"
	"genesis/pkg/config"
	"genesis/pkg/gateway"
	"genesis/pkg/llm"
	"genesis/pkg/tools"    // Added
	"genesis/pkg/tools/os" // Added
	"log"
	"strings"
	"time"

	jsoniter "github.com/json-iterator/go" // Added
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

// ChatHandler 負責管理單次對話的處理流程與狀態
type ChatHandler struct {
	client       llm.LLMClient
	gw           *gateway.GatewayManager
	history      *llm.ChatHistory
	config       *config.Config
	toolRegistry *tools.ToolRegistry // 新增
}

// NewMessageHandler 建立並初始化 ChatHandler
func NewMessageHandler(client llm.LLMClient, gw *gateway.GatewayManager, cfg *config.Config, history *llm.ChatHistory) func(*gateway.UnifiedMessage) {
	tr := tools.NewToolRegistry()
	// 在此註冊工具
	tr.Register(tools.NewOSTool(os.NewOSWorker()))

	h := &ChatHandler{
		client:       client,
		gw:           gw,
		history:      history,
		config:       cfg,
		toolRegistry: tr,
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

	// --- 新增：人機直接指令介面 (Slash Commands) ---
	// 測試指令不應加入歷史訊息，因此在此直接處理並回傳
	if strings.HasPrefix(msg.Content, "/") {
		h.handleSlashCommand(msg)
		return
	}

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

	// 選擇正確的工具格式
	var availableTools any
	pName := h.client.Provider()
	// log.Printf("[Handler] 🛠️ Current Provider: %s", pName)
	switch pName {
	case "gemini":
		availableTools = h.toolRegistry.ToGeminiFormat()
	case "ollama":
		availableTools = h.toolRegistry.ToOllamaFormat()
	default:
		log.Printf("[Handler] ⚠️ Unknown provider format for: %s", pName)
	}

	chunkCh, err := h.client.StreamChat(ctx, h.history.GetMessages(), availableTools)
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
	assistantMsg := h.collectChunks(msg.Session, chunkCh, blockCh, thinkingSent)

	// --- 新增：工具執行邏輯 ---
	if len(assistantMsg.ToolCalls) > 0 {
		// 儲存助理的 ToolCall 訊息
		h.history.Add(assistantMsg)

		for _, tc := range assistantMsg.ToolCalls {
			tool, ok := h.toolRegistry.Get(tc.Name)
			if !ok {
				log.Printf("Unknown tool call: %s", tc.Name)
				continue
			}

			// 解析參數
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				log.Printf("Failed to parse tool args: %v", err)
				continue
			}

			// 執行工具
			log.Printf("🛠️ Executing tool: %s with args: %+v", tc.Name, args)
			res, err := tool.Execute(args)
			if err != nil {
				log.Printf("Tool execution error: %v", err)
				continue
			}

			// 將結果轉為 llm.Message (role: tool)
			toolResMsg := llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    []llm.ContentBlock{},
			}
			for _, b := range res.Content {
				if b.Type == "image" {
					data, _ := tools.Base64Decode(b.Data)
					toolResMsg.Content = append(toolResMsg.Content, llm.NewImageBlock(data, "image/png"))
				} else {
					toolResMsg.Content = append(toolResMsg.Content, llm.NewTextBlock(b.Text))
				}
			}

			// Safety net: Ensure content is not empty to prevent LLM errors (e.g. Ollama "unexpected end of JSON")
			if len(toolResMsg.Content) == 0 {
				toolResMsg.Content = append(toolResMsg.Content, llm.NewTextBlock("(No output)"))
			}

			h.history.Add(toolResMsg)
		}

		// 遞迴呼叫 LLM 處理工具結果
		return h.processLLMStream(msg)
	}

	return assistantMsg
}

// collectChunks 負責從 LLM 讀取 StreamChunk 並累積成完整訊息
func (h *ChatHandler) collectChunks(session gateway.SessionContext, chunkCh <-chan llm.StreamChunk, blockCh chan<- llm.ContentBlock, alreadySentThinking bool) llm.Message {
	var textContent string
	var thinkingContent string
	var errorContent string
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
			textContent, thinkingContent, errorContent = h.processChunk(chunk, textContent, thinkingContent, errorContent, blockCh)

		case <-timerChan:
			h.gw.SendSignal(session, "thinking")
			timerChan = nil // 只送一次
		}
	}

	var toolCalls []llm.ToolCall

	// 第二階段：處理剩餘的 chunks
	for chunk := range chunkCh {
		textContent, thinkingContent, errorContent = h.processChunk(chunk, textContent, thinkingContent, errorContent, blockCh)

		// 累積 ToolCalls
		if len(chunk.ToolCalls) > 0 {
			toolCalls = append(toolCalls, chunk.ToolCalls...)
		}

		if chunk.IsFinal {
			break
		}
	}

	// 返回完整訊息（包含 thinking 和 text）
	msg := llm.Message{
		Role:      "assistant",
		Content:   []llm.ContentBlock{},
		ToolCalls: toolCalls,
	}

	if thinkingContent != "" {
		msg.Content = append(msg.Content, llm.NewThinkingBlock(thinkingContent))
	}

	if textContent != "" {
		msg.Content = append(msg.Content, llm.NewTextBlock(textContent))
	}

	if errorContent != "" {
		msg.Content = append(msg.Content, llm.NewErrorBlock(errorContent))
	}

	return msg
}

// processChunk 處理單個 chunk 並累積內容
func (h *ChatHandler) processChunk(chunk llm.StreamChunk, currentText, currentThinking, currentError string, blockCh chan<- llm.ContentBlock) (string, string, string) {
	// 處理錯誤 chunk（只顯示給使用者，不累積到歷史文字，但累積到錯誤區塊）
	if chunk.Error != "" {
		errorMsg := fmt.Sprintf("\n❌ %s", chunk.Error)
		currentError += errorMsg
		blockCh <- llm.NewErrorBlock(errorMsg)
	}

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

	return currentText, currentThinking, currentError
}

// handleSlashCommand 處理手動輸入的指令，格式：/tool_name action {"param": "value"}
func (h *ChatHandler) handleSlashCommand(msg *gateway.UnifiedMessage) {
	parts := strings.SplitN(strings.TrimPrefix(msg.Content, "/"), " ", 3)
	if len(parts) < 2 {
		h.gw.SendReply(msg.Session, "❌ 格式錯誤。請使用: /[工具名] [動作] [JSON參數(選填)]\n例如: `/os list_desktop` 或 `/os run_command {\"command\":\"dir\"}`")
		return
	}

	toolName := parts[0]
	action := parts[1]

	var params map[string]any
	if len(parts) > 2 {
		if err := json.Unmarshal([]byte(parts[2]), &params); err != nil {
			// 如果不是 JSON，嘗試當作單一字串參數 (針對 run_command 的優化)
			if (toolName == "os" || toolName == "os_control") && action == "run_command" {
				params = map[string]any{"command": parts[2]}
			} else {
				h.gw.SendReply(msg.Session, fmt.Sprintf("❌ 參數解析失敗: %v", err))
				return
			}
		}
	} else {
		params = make(map[string]any)
	}

	// 建立符合 OSTool 預期的參數結構
	args := map[string]any{
		"action": action,
		"params": params,
	}

	tool, ok := h.toolRegistry.Get(toolName)
	if !ok {
		// 嘗試模糊比對 (例如 os_control)
		tool, ok = h.toolRegistry.Get(toolName + "_control")
		if !ok {
			h.gw.SendReply(msg.Session, fmt.Sprintf("❌ 找不到工具: %s", toolName))
			return
		}
	}

	h.gw.SendReply(msg.Session, fmt.Sprintf("🛠️ 手動執行工具: %s/%s...", toolName, action))
	res, err := tool.Execute(args)
	if err != nil {
		h.gw.SendReply(msg.Session, fmt.Sprintf("❌ 執行出錯: %v", err))
		return
	}

	// 發送結果
	resCh := make(chan llm.ContentBlock, len(res.Content))
	go func() {
		defer close(resCh)
		for _, b := range res.Content {
			if b.Type == "image" {
				data, _ := tools.Base64Decode(b.Data)
				resCh <- llm.NewImageBlock(data, "image/png")
			} else {
				resCh <- llm.NewTextBlock(b.Text)
			}
		}
	}()
	_ = h.gw.StreamReply(msg.Session, resCh)
}
