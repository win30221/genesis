package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"genesis/pkg/config"
	"genesis/pkg/gateway"
	"genesis/pkg/llm"
	"genesis/pkg/tools"    // Added
	"genesis/pkg/tools/os" // Added
	"log/slog"
	"strings"
	"time"

	jsoniter "github.com/json-iterator/go" // Added
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

// ChatHandler orchestrates the conversation flow, maintaining state, session history,
// and coordinating between the Gateway, LLM clients, and Tool registry.
// It implements the core "Agentic Loop" where the AI can think, respond, and act.
type ChatHandler struct {
	client       llm.LLMClient           // The LLM provider client (Gemini, Ollama, etc.)
	gw           *gateway.GatewayManager // Manager for sending replies back to communication channels
	history      *llm.ChatHistory        // In-memory buffer for the conversation's message history
	config       *config.Config          // Business-level application configuration
	systemConfig *config.SystemConfig    // Technical/engine-level configuration parameters
	toolRegistry *tools.ToolRegistry     // Registry containing all available tools for agentic actions
}

// NewMessageHandler initializes a ChatHandler instance and returns a closure
// compatible with the gateway.MessageHandler type.
// It sets up the necessary tool controllers, registers core tools, and
// ensures the handler is ready to process incoming unified messages.
// Parameters:
//   - client: The LLM client implementation to be used for reasoning.
//   - gw: The gateway manager for routing replies.
//   - cfg: App-level configuration (keys, prompt).
//   - sysCfg: Engine-level configuration (timeouts, retries).
//   - history: The chat history manager for maintaining context.
//
// Returns:
//   - A MessageHandler function that can be registered with the Gateway.
func NewMessageHandler(client llm.LLMClient, gw *gateway.GatewayManager, cfg *config.Config, sysCfg *config.SystemConfig, history *llm.ChatHistory) func(*gateway.UnifiedMessage) {
	tr := tools.NewToolRegistry()
	// Register tools here
	tr.Register(tools.NewOSTool(os.NewOSWorker()))

	h := &ChatHandler{
		client:       client,
		gw:           gw,
		history:      history,
		config:       cfg,
		systemConfig: sysCfg,
		toolRegistry: tr,
	}

	h.initializeHistory()

	// Sync debug switch based on config
	client.SetDebug(sysCfg.DebugChunks)

	return h.OnMessage
}

// initializeHistory ensures that the initial system prompt is present
// in the ChatHistory as the very first message if history is empty.
func (h *ChatHandler) initializeHistory() {
	if len(h.history.GetMessages()) == 0 && h.config.SystemPrompt != "" {
		h.history.Add(llm.NewSystemMessage(h.config.SystemPrompt))
	}
}

// OnMessage is the primary entry point for processing incoming user messages.
// It performs the following orchestration steps:
// 1. Assigns a unique DebugID for log grouping if not already present.
// 2. Intercepts and executes Slash Commands (test/debug tools).
// 3. Normalizes platform-specific attachments (images, etc.) into LLM ContentBlocks.
// 4. Appends the new user perspective to the ChatHistory.
// 5. Triggers the recursive processLLMStream loop for AI reasoning and action.
// 6. Persists the final AI assistant response to the conversation history.
func (h *ChatHandler) OnMessage(msg *gateway.UnifiedMessage) {
	if msg.DebugID == "" {
		b := make([]byte, 2)
		rand.Read(b)
		msg.DebugID = fmt.Sprintf("%x", b)
	}
	start := time.Now()

	slog.Info("Message received", "channel", msg.Session.ChannelID, "user", msg.Session.Username, "content", msg.Content, "files", len(msg.Files), "debug_id", msg.DebugID)

	// --- Slash Commands ---
	// Test commands should not be added to history, handle and return directly
	if strings.HasPrefix(msg.Content, "/") {
		h.handleSlashCommand(msg)
		return
	}

	// 1. Create user message (multi-modal support)
	userMsg := llm.Message{
		Role:    "user",
		Content: []llm.ContentBlock{},
	}

	// Add text content
	if msg.Content != "" {
		userMsg.Content = append(userMsg.Content, llm.NewTextBlock(msg.Content))
	}

	// Add image attachments
	for _, file := range msg.Files {
		userMsg.Content = append(userMsg.Content, llm.NewImageBlock(file.Data, file.MimeType))
		slog.Info("Attached file", "name", file.Filename, "mime", file.MimeType, "bytes", len(file.Data))
	}

	// Store user message
	h.history.Add(userMsg)

	// 2. Call LLM and handle stream
	assistantMsg := h.processLLMStream(msg)

	// 3. Record AI response
	if len(assistantMsg.Content) > 0 {
		h.history.Add(assistantMsg)
	}

	slog.Info("Agent loop finished", "duration", time.Since(start).String(), "debug_id", msg.DebugID)
}

// processLLMStream manages the core Agentic reasoning loop including streaming
// response forwarding, tool execution recursion, and error recovery.
//
// Internal Workflow:
//
//	A. Sets up a context with a hard timeout derived from SystemConfig.LLMTimeoutMs.
//	B. Performs a streaming chat call to the LLM client.
//	C. Uses collectChunks to aggregate tokens while concurrently pushing text blocks back to the Gateway.
//	D. If tool calls are detected in the LLM response:
//	   - Checks security/system constraints (e.g., EnableTools).
//	   - Executes tools via the Registry and appends results to history.
//	   - Recursively calls processLLMStream to let the AI process the tool outcomes.
//	E. Handles "length limit" triggers by automatically requesting content continuation.
//	F. Performs transparent retries on transient connection or streaming errors.
//
// Parameters:
//   - msg: The original unified message containing session context for reply routing.
//
// Returns:
//   - The final aggregated llm.Message representing the AI's complete response/state.
func (h *ChatHandler) processLLMStream(msg *gateway.UnifiedMessage) llm.Message {
	timeout := time.Duration(h.systemConfig.LLMTimeoutMs) * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Inject debug tracking ID (used to group agent loop logs into one folder)
	if msg.DebugID != "" {
		ctx = context.WithValue(ctx, llm.DebugDirContextKey, msg.DebugID)
	}

	// Set up "thinking" timer
	thinkingSent := false
	delay := time.Duration(h.systemConfig.ThinkingInitDelayMs) * time.Millisecond
	initTimer := time.AfterFunc(delay, func() {
		h.gw.SendSignal(msg.Session, "thinking")
		thinkingSent = true
	})

	// Select the correct tool format
	var availableTools any
	if h.systemConfig.EnableTools && !msg.NoTools {
		pName := h.client.Provider()
		switch pName {
		case "gemini":
			availableTools = h.toolRegistry.ToGeminiFormat()
		case "ollama", "openai":
			availableTools = h.toolRegistry.ToOllamaFormat()
		default:
			slog.Warn("Unknown provider format", "provider", pName)
		}
	}

	chunkCh, err := h.client.StreamChat(ctx, h.history.GetMessages(), availableTools)
	initTimer.Stop()

	if err != nil {
		slog.Error("LLM stream init failed", "error", err)
		h.gw.SendReply(msg.Session, fmt.Sprintf("❌ Error: %v", err))
		return llm.Message{}
	}

	// Prepare the stream channel for system forwarding
	blockCh := make(chan llm.ContentBlock, 100)
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		if err := h.gw.StreamReply(msg.Session, blockCh); err != nil {
			slog.Error("Failed to stream reply", "error", err)
		}
	}()

	// Encapsulate closing logic to ensure manual closing and waiting before recursion
	closed := false
	safeClose := func() {
		if !closed {
			close(blockCh)
			<-streamDone
			closed = true
		}
	}
	defer safeClose()

	// Handle chunks
	assistantMsg, streamErr := h.collectChunks(msg.Session, chunkCh, blockCh, thinkingSent)

	// 1. End of LLM turn, close current stream block in time (e.g., block containing thinking process)
	safeClose()

	// --- Tool Execution Logic ---
	if len(assistantMsg.ToolCalls) > 0 {
		// Store assistant's ToolCall message
		h.history.Add(assistantMsg)

		for _, tc := range assistantMsg.ToolCalls {
			var resultBlocks []llm.ContentBlock
			success := false

			tool, ok := h.toolRegistry.Get(tc.Name)
			if !ok {
				errMsg := fmt.Sprintf("Error: Unknown tool '%s'", tc.Name)
				slog.Error("Unknown tool call", "name", tc.Name)
				resultBlocks = append(resultBlocks, llm.NewTextBlock(errMsg))
			} else {
				// Parse parameters
				var args map[string]any
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					errMsg := fmt.Sprintf("Error: Failed to parse tool arguments: %v", err)
					slog.Error("Failed to parse tool args", "error", err)
					resultBlocks = append(resultBlocks, llm.NewTextBlock(errMsg))
				} else {
					// Execute tool
					slog.Info("Executing tool", "name", tc.Name, "args", args)
					res, err := tool.Execute(args)
					if err != nil {
						errMsg := fmt.Sprintf("Error: Tool execution failed: %v", err)
						slog.Error("Tool execution error", "error", err)
						resultBlocks = append(resultBlocks, llm.NewTextBlock(errMsg))
					} else {
						success = true
						// Convert tools.ContentBlock to llm.ContentBlock
						for _, b := range res.Content {
							if b.Type == "text" {
								resultBlocks = append(resultBlocks, llm.NewTextBlock(b.Text))
							} else if b.Type == "image" {
								data, _ := base64.StdEncoding.DecodeString(b.Data)
								resultBlocks = append(resultBlocks, llm.NewImageBlock(data, "image/png"))
							}
						}
					}
				}
			}

			// MUST add tool result to history to avoid OpenAI 400 error
			toolResMsg := llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    resultBlocks,
			}
			h.history.Add(toolResMsg)

			// 2. Use independent stream to send tool results to frontend (Role: tool/system)
			h.gw.SendSignal(msg.Session, "role:system")

			resCh := make(chan llm.ContentBlock, len(toolResMsg.Content))
			for _, b := range toolResMsg.Content {
				resCh <- b
			}
			close(resCh)
			if err := h.gw.StreamReply(msg.Session, resCh); err != nil {
				slog.Error("Failed to stream tool result", "error", err)
			}
			_ = success // Keep track if needed later
		}

		// safeClose already called before recursion
		return h.processLLMStream(msg)
	}

	// 3. Anomaly detection and automatic retry
	reason := "UNKNOWN"
	if assistantMsg.Usage != nil {
		reason = assistantMsg.Usage.StopReason
	}

	hasContent, hasThinking, preview := summarizeContent(assistantMsg)
	hasToolCalls := len(assistantMsg.ToolCalls) > 0
	// A response is normal if:
	// 1. It stopped because of a valid reason (stop/length) OR it's UNKNOWN but we have content and no stream error.
	// 2. No stream error occurred.
	// 3. We actually got some content, thinking process, or tool calls.
	isNormal := streamErr == nil && (hasContent || hasThinking || hasToolCalls) && (reason == llm.StopReasonStop || reason == llm.StopReasonLength || reason == "UNKNOWN")

	if !isNormal {
		// 3.1 Handle continuation logic (StopReason == "length")
		if reason == llm.StopReasonLength && (hasContent || hasThinking) {
			maxCont := h.systemConfig.MaxContinuations
			if msg.ContinueCount < maxCont {
				msg.ContinueCount++
				slog.Info("Truncation detected, continuing", "thinking", hasThinking, "content", hasContent, "continuation", fmt.Sprintf("%d/%d", msg.ContinueCount, maxCont), "preview", preview)

				h.gw.SendReply(msg.Session, fmt.Sprintf("⚠️ Content truncated due to length, attempting to continue (%d/%d)...", msg.ContinueCount, maxCont))

				// Store current partial response in history
				h.history.Add(assistantMsg)

				time.Sleep(time.Duration(h.systemConfig.RetryDelayMs) * time.Millisecond)
				safeClose()
				return h.processLLMStream(msg)
			} else {
				slog.Warn("Max continuation reached", "max", maxCont)
				h.gw.SendReply(msg.Session, "❌ Max continuation reached, forced stop.")
				return assistantMsg
			}
		}

		// 3.2 Handle general retry logic
		if retried := h.attemptRetry(msg, reason, streamErr, preview); retried {
			safeClose()
			return h.processLLMStream(msg)
		}
	}

	return assistantMsg
}

// collectChunks is an auxiliary method dedicated to consuming a StreamChunk channel.
// It performs real-time state management by detecting changes between "thinking"
// and "content" phases and triggers appropriate UI signals via the Gateway.
// It also accumulates the raw tokens into a final llm.Message object.
// Parameters:
//   - session: Target session for sending thinking/UI signals.
//   - chunkCh: Inbound channel providing stream fragments from the LLM client.
//   - blockCh: Outbound channel for forwarding processed ContentBlocks to the Gateway.
//   - alreadySentThinking: Flag to prevent redundant thinking UI signals in recursive calls.
//
// Returns:
//   - The fully aggregated message and any error encountered during consumption.
func (h *ChatHandler) collectChunks(session gateway.SessionContext, chunkCh <-chan llm.StreamChunk, blockCh chan<- llm.ContentBlock, alreadySentThinking bool) (llm.Message, error) {
	var textContent string
	var thinkingContent string
	var errorContent string
	var toolCalls []llm.ToolCall
	var lastUsage *llm.LLMUsage
	var lastError error
	firstChunkReceived := false

	// Phase 1: Wait for first chunk or trigger "thinking" timer
	var thinkingTimer *time.Timer
	var timerChan <-chan time.Time
	if !alreadySentThinking {
		delay := time.Duration(h.systemConfig.ThinkingTokenDelayMs) * time.Millisecond
		thinkingTimer = time.NewTimer(delay)
		defer thinkingTimer.Stop()
		timerChan = thinkingTimer.C
	}

Phase1Loop:
	for !firstChunkReceived {
		select {
		case chunk, ok := <-chunkCh:
			if !ok {
				return llm.Message{}, nil // Channel closed and no content
			}
			if chunk.RawError != nil {
				return llm.Message{}, chunk.RawError
			}
			firstChunkReceived = true
			if thinkingTimer != nil {
				thinkingTimer.Stop()
			}
			// Handle first chunk
			textContent, thinkingContent, errorContent = h.processChunk(chunk, textContent, thinkingContent, errorContent, blockCh)
			if len(chunk.ToolCalls) > 0 {
				toolCalls = append(toolCalls, chunk.ToolCalls...)
			}
			if chunk.Usage != nil {
				lastUsage = chunk.Usage
			}
			if chunk.IsFinal {
				break Phase1Loop
			}

		case <-timerChan:
			h.gw.SendSignal(session, "thinking")
			timerChan = nil // Send only once
		}
	}

	// Phase 2: Process remaining chunks
	for chunk := range chunkCh {
		if chunk.RawError != nil {
			lastError = chunk.RawError
		}
		textContent, thinkingContent, errorContent = h.processChunk(chunk, textContent, thinkingContent, errorContent, blockCh)

		// Accumulate ToolCalls
		if len(chunk.ToolCalls) > 0 {
			toolCalls = append(toolCalls, chunk.ToolCalls...)
		}

		// Accumulate Usage
		if chunk.Usage != nil {
			lastUsage = chunk.Usage
		}

		if chunk.IsFinal {
			break
		}
	}

	// Return complete message (including thinking and text)
	msg := llm.Message{
		Role:      "assistant",
		Content:   []llm.ContentBlock{},
		ToolCalls: toolCalls,
		Usage:     lastUsage,
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

	return msg, lastError
}

// processChunk handles the low-level parsing of a single LLM StreamChunk.
// It extracts text, reasoning tokens (thinking), and error messages, appending them
// to the provided accumulation buffers. It also emits UI blocks if the chunk
// contains valid user-facing content.
func (h *ChatHandler) processChunk(chunk llm.StreamChunk, currentText, currentThinking, currentError string, blockCh chan<- llm.ContentBlock) (string, string, string) {
	// Handle error chunk (display to user only, don't accumulate to history text, but accumulate to error block)
	if chunk.Error != "" {
		errorMsg := fmt.Sprintf("\n❌ %s", chunk.Error)
		currentError += errorMsg
		blockCh <- llm.NewErrorBlock(errorMsg)
	}

	for _, block := range chunk.ContentBlocks {
		switch block.Type {
		case llm.BlockTypeText:
			currentText += block.Text
			// Directly send ContentBlock
			blockCh <- block

		case llm.BlockTypeThinking:
			currentThinking += block.Text
			if h.systemConfig.ShowThinking {
				// Directly send ContentBlock
				blockCh <- block
			}
		case llm.BlockTypeImage:
			// Images in stream (less common in Ollama, but supported for completeness)
			blockCh <- block
		}
	}

	return currentText, currentThinking, currentError
}

// handleSlashCommand parses and executes manual "slash" commands entered by the user.
// These commands are typically used for direct tool debugging or administrative tasks.
// Format: /tool_name action {"param": "value"}
func (h *ChatHandler) handleSlashCommand(msg *gateway.UnifiedMessage) {
	parts := strings.SplitN(strings.TrimPrefix(msg.Content, "/"), " ", 3)
	if len(parts) < 2 {
		h.gw.SendReply(msg.Session, "❌ Format error. Please use: /[tool_name] [action] [JSON_params(optional)]\nExample: `/os list_desktop` or `/os run_command {\"command\":\"dir\"}`")
		return
	}

	toolName := parts[0]
	action := parts[1]

	// Handle /notools virtual command (normal conversation without tools)
	if toolName == "notools" {
		msg.NoTools = true
		msg.Content = action
		if len(parts) > 2 {
			msg.Content += " " + parts[2]
		}
		assistantMsg := h.processLLMStream(msg)
		h.history.Add(assistantMsg)
		return
	}

	var params map[string]any
	if len(parts) > 2 {
		if err := json.Unmarshal([]byte(parts[2]), &params); err != nil {
			// If not JSON, try treating it as a single string parameter (optimization for run_command)
			if (toolName == "os" || toolName == "os_control") && action == "run_command" {
				params = map[string]any{"command": parts[2]}
			} else {
				h.gw.SendReply(msg.Session, fmt.Sprintf("❌ Parameter parsing failed: %v", err))
				return
			}
		}
	} else {
		params = make(map[string]any)
	}

	// Create parameter structure expected by OSTool
	args := map[string]any{
		"action": action,
		"params": params,
	}

	tool, ok := h.toolRegistry.Get(toolName)
	if !ok {
		// Attempt fuzzy matching (e.g., os_control)
		tool, ok = h.toolRegistry.Get(toolName + "_control")
		if !ok {
			h.gw.SendReply(msg.Session, fmt.Sprintf("❌ Tool not found: %s", toolName))
			return
		}
	}

	h.gw.SendReply(msg.Session, fmt.Sprintf("🛠️ Manually executing tool: %s/%s...", toolName, action))
	res, err := tool.Execute(args)
	if err != nil {
		h.gw.SendReply(msg.Session, fmt.Sprintf("❌ Execution error: %v", err))
		return
	}

	// Send results
	blocks := convertToolResult(res)
	resCh := make(chan llm.ContentBlock, len(blocks))
	for _, b := range blocks {
		resCh <- b
	}
	close(resCh)
	_ = h.gw.StreamReply(msg.Session, resCh)
}

// attemptRetry checks if a retry is allowed and, if so, increments the counter
// and sends a notification to the user. Returns true if the caller should proceed
// with the retry (recursive call), false if max retries have been exhausted.
//
// Error classification is delegated entirely to each LLM Client's IsTransientError().
// The handler does NOT parse error strings itself.
func (h *ChatHandler) attemptRetry(msg *gateway.UnifiedMessage, reason string, streamErr error, preview string) bool {
	// Delegate error classification to the LLM client
	if streamErr != nil && !h.client.IsTransientError(streamErr) {
		slog.Error("Non-transient error, skipping retry", "error", streamErr)
		h.gw.SendReply(msg.Session, fmt.Sprintf("❌ %v", streamErr))
		return false
	}

	maxRetries := h.systemConfig.MaxRetries
	if msg.RetryCount >= maxRetries {
		slog.Error("Max retries reached", "max", maxRetries, "reason", reason, "error", streamErr)
		h.gw.SendReply(msg.Session, "❌ AI response remains abnormal, please try rephrasing or restarting the conversation.")
		return false
	}

	msg.RetryCount++
	slog.Warn("Abnormal response, retrying",
		"reason", reason,
		"error", streamErr,
		"preview", preview,
		"has_content", preview != "",
		"retry", fmt.Sprintf("%d/%d", msg.RetryCount, maxRetries),
	)

	retryNotice := fmt.Sprintf("⚠️ Abnormal response (%s), attempting automatic fix (%d/%d)...", reason, msg.RetryCount, maxRetries)
	if streamErr != nil {
		retryNotice = fmt.Sprintf("⚠️ Connection error (%v), attempting automatic recovery (%d/%d)...", streamErr, msg.RetryCount, maxRetries)
	}
	h.gw.SendReply(msg.Session, retryNotice)

	time.Sleep(time.Duration(h.systemConfig.RetryDelayMs) * time.Millisecond)
	return true
}

// summarizeContent scans the assistant message and returns whether it has
// text content, thinking content, and a truncated preview string for logging.
func summarizeContent(msg llm.Message) (hasContent, hasThinking bool, preview string) {
	var sb strings.Builder
	for _, b := range msg.Content {
		if b.Type == llm.BlockTypeThinking && b.Text != "" {
			hasThinking = true
		}
		if b.Type == llm.BlockTypeText && b.Text != "" {
			hasContent = true
		}
		sb.WriteString(b.Text)
	}
	preview = sb.String()
	if len(preview) > 100 {
		preview = preview[:100] + "..."
	}
	return
}

// convertToolResult transforms a tools.ToolResult into a slice of llm.ContentBlock.
// It handles both text and image content types and ensures non-empty output.
func convertToolResult(res *tools.ToolResult) []llm.ContentBlock {
	var blocks []llm.ContentBlock
	for _, b := range res.Content {
		if b.Type == llm.BlockTypeImage {
			data, _ := tools.Base64Decode(b.Data)
			blocks = append(blocks, llm.NewImageBlock(data, "image/png"))
		} else {
			blocks = append(blocks, llm.NewTextBlock(b.Text))
		}
	}
	// Safety net: ensure content is not empty to prevent LLM errors
	if len(blocks) == 0 {
		blocks = append(blocks, llm.NewTextBlock("(No output)"))
	}
	return blocks
}
