package agent

import (
	"context"
	"fmt"
	"genesis/pkg/api"
	"genesis/pkg/config"
	"genesis/pkg/llm"
	"genesis/pkg/tools"
	"genesis/pkg/utils"
	"log/slog"
	"maps"
	"strings"
	"time"

	jsoniter "github.com/json-iterator/go"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

// AgentEngine manages the core reasoning loop, including LLM communication,
// tool execution, and recursive turn handling.
// It implements api.AgentEngine.
type AgentEngine struct {
	client       llm.LLMClient
	responder    api.MessageResponder
	sysCfg       *config.SystemConfig
	appCfg       *config.Config
	toolRegistry api.ToolRegistry
	sessions     *llm.SessionManager
}

// NewAgentEngine initializes a new AgentEngine with config managers.
func NewAgentEngine(
	client llm.LLMClient,
	appCfg *config.Config,
	sysCfg *config.SystemConfig,
	sessions *llm.SessionManager,
) *AgentEngine {
	return &AgentEngine{
		client:   client,
		appCfg:   appCfg,
		sysCfg:   sysCfg,
		sessions: sessions,
	}
}

// SetResponder sets the messaging interface used by the engine to send replies.
func (e *AgentEngine) SetResponder(responder api.MessageResponder) {
	e.responder = responder
}

// SetToolRegistry sets the tool registry used by the engine for tool execution.
func (e *AgentEngine) SetToolRegistry(tr api.ToolRegistry) {
	e.toolRegistry = tr
}

// RegisterTool adds one or more tools to the engine's registry.
// It automatically initializes the registry if it's currently nil.
func (e *AgentEngine) RegisterTool(tl ...api.Tool) {
	if e.toolRegistry == nil {
		e.toolRegistry = tools.NewToolRegistry()
	}
	for _, t := range tl {
		e.toolRegistry.Register(t)
	}
}

// HandleMessage is the primary entry point for processing an user message in the engine.
func (e *AgentEngine) HandleMessage(ctx context.Context, msg *api.UnifiedMessage, history *llm.ChatHistory) llm.Message {
	sessionID := fmt.Sprintf("%s_%s", msg.Session.ChannelID, msg.Session.ChatID)

	e.ensureSystemPrompt(history)

	if strings.HasPrefix(msg.Content, "/") {
		return e.handleSlashCommand(ctx, msg, history, sessionID)
	}

	userMsg := llm.Message{
		ID:        utils.GenerateID(),
		Role:      "user",
		Content:   []llm.ContentBlock{},
		Timestamp: time.Now().Unix(),
	}

	if msg.Content != "" {
		userMsg.Content = append(userMsg.Content, llm.NewTextBlock(msg.Content))
	}

	for _, file := range msg.Files {
		if file.Path != "" {
			userMsg.Content = append(userMsg.Content, llm.NewImageBlockFromFile(file.Path, file.MimeType))
			slog.InfoContext(ctx, "Attached file from disk", "name", file.Filename, "mime", file.MimeType, "path", file.Path)
		} else {
			userMsg.Content = append(userMsg.Content, llm.NewImageBlock(file.Data, file.MimeType))
			slog.InfoContext(ctx, "Attached file inline", "name", file.Filename, "mime", file.MimeType, "bytes", len(file.Data))
		}
	}

	history.Add(userMsg)
	e.sessions.SaveSession(sessionID)

	assistantMsg := e.ProcessLLMStream(ctx, msg, history)

	if len(assistantMsg.Content) > 0 {
		history.Add(assistantMsg)
		e.sessions.SaveSession(sessionID)
	}

	e.maybeSummarize(ctx, sessionID, history, assistantMsg.Usage)
	return assistantMsg
}

// ensureSystemPrompt ensures that the initial system prompt is present
// in the ChatHistory. It dynamically injects latest conversation summaries to maintain contextual continuity.
func (e *AgentEngine) ensureSystemPrompt(history *llm.ChatHistory) {
	prompt := e.appCfg.SystemPrompt

	// Inject summary if available
	if summary := history.GetSummary(); summary != "" {
		prompt = fmt.Sprintf("%s\n\n[CONVERSATION SUMMARY]\n%s", prompt, summary)
	}

	if prompt != "" {
		history.EnsureSystemMessage(prompt)
	}
}

// handleSlashCommand parses and executes manual "slash" commands entered by the user.
func (e *AgentEngine) handleSlashCommand(ctx context.Context, msg *api.UnifiedMessage, history *llm.ChatHistory, sessionID string) llm.Message {
	parts := strings.SplitN(strings.TrimPrefix(msg.Content, "/"), " ", 3)
	if len(parts) < 2 {
		e.responder.SendReply(msg.Session, "❌ Format error. Please use: /[tool_name] [action] [JSON_params(optional)]\nExample: `/os list_desktop` or `/os run_command {\"command\":\"dir\"}`")
		return llm.Message{}
	}

	toolName := parts[0]
	action := parts[1]

	if toolName == "notools" {
		msg.NoTools = true
		msg.Content = action
		if len(parts) > 2 {
			msg.Content += " " + parts[2]
		}

		assistantMsg := e.ProcessLLMStream(ctx, msg, history)
		if len(assistantMsg.Content) > 0 {
			history.Add(assistantMsg)
			e.sessions.SaveSession(sessionID)
		}
		return assistantMsg
	}

	var params map[string]any
	if len(parts) > 2 {
		if err := json.Unmarshal([]byte(parts[2]), &params); err != nil {
			if (toolName == "os" || toolName == "os_control") && action == "run_command" {
				params = map[string]any{"command": parts[2]}
			} else {
				e.responder.SendReply(msg.Session, fmt.Sprintf("❌ Parameter parsing failed: %v", err))
				return llm.Message{}
			}
		}
	} else {
		params = make(map[string]any)
	}

	args := make(map[string]any)
	args["action"] = action
	maps.Copy(args, params)

	tool, ok := e.toolRegistry.Get(toolName)
	if !ok {
		tool, ok = e.toolRegistry.Get(toolName + "_control")
		if !ok {
			e.responder.SendReply(msg.Session, fmt.Sprintf("❌ Tool not found: %s", toolName))
			return llm.Message{}
		}
	}

	e.responder.SendReply(msg.Session, fmt.Sprintf("🛠️ Manually executing tool: %s/%s...", toolName, action))

	res, err := tool.Execute(ctx, args)
	if err != nil {
		e.responder.SendReply(msg.Session, fmt.Sprintf("❌ Execution error: %v", err))
		return llm.Message{}
	}

	resBlocks := ConvertToolResult(res)
	e.StreamBlocks(ctx, msg.Session, resBlocks)

	return llm.Message{
		ID:        utils.GenerateID(),
		Role:      "assistant",
		Content:   resBlocks,
		Timestamp: time.Now().Unix(),
	}
}

// maybeSummarize triggers an asynchronous summarization if history is too long.
func (e *AgentEngine) maybeSummarize(ctx context.Context, sessionID string, history *llm.ChatHistory, usage *llm.LLMUsage) {
	sysCfg := e.sysCfg
	threshold := sysCfg.HistorySummarizeThreshold
	maxChars := sysCfg.HistoryMaxChars
	maxTokens := sysCfg.HistoryMaxTokens
	keepCount := sysCfg.HistoryKeepRecentCount

	msgs := history.GetMessages()
	msgCount := len(msgs)

	if msgCount <= keepCount {
		return
	}

	overTokens := false
	if usage != nil && usage.TotalTokens > 0 && maxTokens > 0 {
		if usage.TotalTokens >= maxTokens {
			overTokens = true
		}
	}

	totalChars := 0
	if !overTokens {
		for _, m := range msgs {
			for _, b := range m.Content {
				if b.Type == llm.BlockTypeText {
					totalChars += len(b.Text)
				}
			}
		}
	}

	overCount := threshold > 0 && msgCount >= threshold
	overSize := maxChars > 0 && totalChars >= maxChars

	if !overTokens && !overCount && !overSize {
		return
	}

	slog.InfoContext(ctx, "Triggering sliding window summarization", "session", sessionID)

	summary, err := e.summarizeSession(ctx, history)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to summarize session", "session", sessionID, "error", err)
		return
	}

	history.SetSummary(summary)
	history.TruncateHistory(e.sysCfg.HistoryKeepRecentCount)
	e.sessions.SaveSession(sessionID)
	slog.InfoContext(ctx, "Session summarized successfully", "session", sessionID)
}

// summarizeSession calls the LLM to create a concise summary.
func (e *AgentEngine) summarizeSession(ctx context.Context, history *llm.ChatHistory) (string, error) {
	msgs := history.GetMessages()

	summaryPrompt := "你是一個對話分析助手。請根據「之前的摘要」以及「新發生的對話片段」，產出一份更新後的簡潔對話摘要。\n" +
		"摘要應包含：重要的事實、用戶偏好、以及討論結論。\n" +
		"指令：請僅輸出更新後的摘要文字，不要有開場白或解釋。"

	existing := history.GetSummary()
	if existing == "" {
		existing = "(目前尚無摘要)"
	}

	sysCfg := e.sysCfg
	keepCount := sysCfg.HistoryKeepRecentCount
	if len(msgs) <= keepCount+1 {
		return existing, nil
	}

	toSummarize := msgs[1 : len(msgs)-keepCount]

	var historyBuilder strings.Builder
	for _, m := range toSummarize {
		roleLabel := "用戶"
		switch m.Role {
		case "assistant":
			roleLabel = "助手"
		case "tool":
			roleLabel = "工具"
		}

		var msgText strings.Builder
		for _, b := range m.Content {
			if b.Type == llm.BlockTypeText {
				msgText.WriteString(b.Text)
			}
		}

		if msgText.Len() > 0 {
			historyBuilder.WriteString(fmt.Sprintf("[%s]: %s\n", roleLabel, strings.TrimSpace(msgText.String())))
		}
	}

	summarizerMsgs := []llm.Message{
		llm.NewSystemMessage(summaryPrompt),
		{
			Role: "user",
			Content: []llm.ContentBlock{
				llm.NewTextBlock(fmt.Sprintf("【之前的摘要】：\n%s\n\n【新發生的需要被總結的片段】：\n%s\n\n請提供產出整合後的最新摘要：", existing, historyBuilder.String())),
			},
		},
	}

	chunkCh, err := e.client.StreamChat(ctx, summarizerMsgs, nil)
	if err != nil {
		return "", err
	}

	var summary strings.Builder
	for chunk := range chunkCh {
		if chunk.RawError != nil {
			return "", chunk.RawError
		}
		for _, b := range chunk.ContentBlocks {
			if b.Type == llm.BlockTypeText {
				summary.WriteString(b.Text)
			}
		}
	}

	return summary.String(), nil
}

// ProcessLLMStream manages the core Agentic reasoning loop including streaming
// response forwarding, tool execution recursion, and error recovery.
func (e *AgentEngine) ProcessLLMStream(ctx context.Context, msg *api.UnifiedMessage, history *llm.ChatHistory) llm.Message {
	sysCfg := e.sysCfg
	timeout := time.Duration(sysCfg.LLMTimeoutMs) * time.Millisecond
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Inject native tools; clients will format them appropriately
	var availableTools []llm.Tool
	if sysCfg.EnableTools && !msg.NoTools {
		apiTools := e.toolRegistry.GetAll()
		availableTools = make([]llm.Tool, len(apiTools))
		for i, t := range apiTools {
			availableTools[i] = t
		}
	}

	chunkCh, err := e.client.StreamChat(runCtx, history.GetMessages(), availableTools)

	if err != nil {
		slog.ErrorContext(runCtx, "LLM stream init failed", "error", err)
		errMsg := fmt.Sprintf("Error during stream initiation: %v", err)
		e.responder.SendReply(msg.Session, "❌ "+errMsg)

		return llm.Message{
			ID:        utils.GenerateID(),
			Role:      "assistant",
			Content:   []llm.ContentBlock{llm.NewErrorBlock(errMsg)},
			Timestamp: time.Now().Unix(),
		}
	}

	blockCh := make(chan llm.ContentBlock, 100)
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		if err := e.responder.StreamReply(msg.Session, blockCh); err != nil {
			slog.ErrorContext(runCtx, "Failed to stream reply", "error", err)
		}
	}()

	closed := false
	safeClose := func() {
		if !closed {
			close(blockCh)
			<-streamDone
			closed = true
		}
	}
	defer safeClose()

	assistantMsg, streamErr := e.CollectChunks(runCtx, msg.Session, chunkCh, blockCh)
	safeClose()

	// --- Tool Execution Logic ---
	if len(assistantMsg.ToolCalls) > 0 {
		sessionID := fmt.Sprintf("%s_%s", msg.Session.ChannelID, msg.Session.ChatID)
		history.Add(assistantMsg)
		e.sessions.SaveSession(sessionID)

		for _, tc := range assistantMsg.ToolCalls {
			e.ResolveAndCommitToolCall(ctx, tc, msg, history)
		}

		e.sessions.SaveSession(sessionID)
		return e.ProcessLLMStream(ctx, msg, history)
	}

	reason := "UNKNOWN"
	if assistantMsg.Usage != nil {
		reason = assistantMsg.Usage.StopReason
	}

	hasContent, hasThinking, preview := SummarizeContent(assistantMsg)
	isNormal := streamErr == nil && (hasContent || hasThinking) && (reason == llm.StopReasonStop || reason == "UNKNOWN")

	if !isNormal {
		if reason == llm.StopReasonLength {
			slog.InfoContext(runCtx, "Response truncated by length limit", "thinking", hasThinking, "content", hasContent)
			e.responder.SendReply(msg.Session, "⚠️ Response truncated due to length limit.")
			return assistantMsg
		}

		if retried := e.AttemptRetry(ctx, msg, reason, streamErr, preview); retried {
			safeClose()
			return e.ProcessLLMStream(ctx, msg, history)
		}

		if streamErr != nil {
			assistantMsg.AddContentBlock(llm.NewErrorBlock(fmt.Sprintf("\n❌ Stream error: %v", streamErr)))
		} else if !hasContent && !hasThinking {
			assistantMsg.AddContentBlock(llm.NewErrorBlock(fmt.Sprintf("\n❌ Abnormal response: %s", reason)))
		}
	}

	return assistantMsg
}

// CollectChunks is an auxiliary method dedicated to consuming a StreamChunk channel.
func (e *AgentEngine) CollectChunks(ctx context.Context, session api.SessionContext, chunkCh <-chan llm.StreamChunk, blockCh chan<- llm.ContentBlock) (llm.Message, error) {
	msg := llm.Message{
		ID:        utils.GenerateID(),
		Role:      "assistant",
		Content:   []llm.ContentBlock{},
		Timestamp: time.Now().Unix(),
	}
	var lastError error

	sysCfg := e.sysCfg
	delay := time.Duration(sysCfg.ThinkingInitDelayMs) * time.Millisecond
	thinkingTimer := time.NewTimer(delay)
	defer thinkingTimer.Stop()
	timerChan := thinkingTimer.C

	for {
		select {
		case chunk, ok := <-chunkCh:
			if !ok {
				return msg, lastError
			}
			if chunk.RawError != nil {
				return msg, chunk.RawError
			}

			if thinkingTimer != nil {
				thinkingTimer.Stop()
				thinkingTimer = nil
				timerChan = nil
			}

			e.ProcessChunk(ctx, chunk, &msg, blockCh)

			if chunk.IsFinal {
				return msg, lastError
			}

		case <-timerChan:
			e.responder.SendSignal(session, "thinking")
			timerChan = nil
		}
	}
}

// HandleToolCall encapsulates the logic for resolving, parsing, and executing an individual tool call.
func (e *AgentEngine) HandleToolCall(ctx context.Context, tc llm.ToolCall) []llm.ContentBlock {
	cleanName := strings.TrimPrefix(tc.Name, "functions.")

	tool, ok := e.toolRegistry.Get(cleanName)
	if !ok {
		slog.ErrorContext(ctx, "Unknown tool call", "name", tc.Name, "clean_name", cleanName)
		return []llm.ContentBlock{llm.NewTextBlock(fmt.Sprintf("Error: Unknown tool '%s'", tc.Name))}
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		slog.ErrorContext(ctx, "Failed to parse tool args", "error", err)
		return []llm.ContentBlock{llm.NewTextBlock(fmt.Sprintf("Error: Failed to parse tool arguments: %v", err))}
	}

	slog.InfoContext(ctx, "Executing tool", "name", tc.Name, "args", args)
	res, err := tool.Execute(ctx, args)
	if err != nil {
		slog.ErrorContext(ctx, "Tool execution error", "name", tc.Name, "error", err)
		return []llm.ContentBlock{llm.NewTextBlock(fmt.Sprintf("Error: Tool execution failed: %v", err))}
	}

	return ConvertToolResult(res)
}

// ResolveAndCommitToolCall is a resilience wrapper that ensures Every tool call
// results in a tool message being added to the history, even if the tool panics.
func (e *AgentEngine) ResolveAndCommitToolCall(ctx context.Context, tc llm.ToolCall, msg *api.UnifiedMessage, history *llm.ChatHistory) {
	var resultBlocks []llm.ContentBlock

	defer func() {
		if r := recover(); r != nil {
			slog.ErrorContext(ctx, "Tool execution panicked", "tool", tc.Name, "error", r)
			resultBlocks = []llm.ContentBlock{llm.NewTextBlock("Error: Internal processing panic")}
		}

		toolResMsg := llm.Message{
			ID:         utils.GenerateID(),
			Role:       "tool",
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			Content:    resultBlocks,
			Timestamp:  time.Now().Unix(),
		}
		history.Add(toolResMsg)

		e.responder.SendSignal(msg.Session, "role:system")
		e.StreamBlocks(ctx, msg.Session, resultBlocks)
	}()

	resultBlocks = e.HandleToolCall(ctx, tc)
}

// StreamBlocks is a utility to pipe a slice of content blocks into the gateway's stream.
func (e *AgentEngine) StreamBlocks(ctx context.Context, session api.SessionContext, blocks []llm.ContentBlock) {
	if len(blocks) == 0 {
		return
	}
	resCh := make(chan llm.ContentBlock, len(blocks))
	for _, b := range blocks {
		resCh <- b
	}
	close(resCh)
	if err := e.responder.StreamReply(session, resCh); err != nil {
		slog.ErrorContext(ctx, "Failed to stream blocks", "error", err)
	}
}

// ProcessChunk handles the low-level parsing of a single LLM StreamChunk.
func (e *AgentEngine) ProcessChunk(ctx context.Context, chunk llm.StreamChunk, msg *llm.Message, blockCh chan<- llm.ContentBlock) {
	if chunk.Error != "" {
		errorMsg := fmt.Sprintf("\n❌ %s", chunk.Error)
		msg.AddContentBlock(llm.NewErrorBlock(errorMsg))
		blockCh <- llm.NewErrorBlock(errorMsg)
	}

	for _, block := range chunk.ContentBlocks {
		msg.AddContentBlock(block)

		switch block.Type {
		case llm.BlockTypeText:
			blockCh <- block
		case llm.BlockTypeThinking:
			if e.sysCfg.ShowThinking {
				blockCh <- block
			}
		case llm.BlockTypeImage:
			blockCh <- block
		}
	}

	if len(chunk.ToolCalls) > 0 {
		msg.ToolCalls = append(msg.ToolCalls, chunk.ToolCalls...)
	}

	if chunk.Usage != nil {
		msg.Usage = chunk.Usage
	}
}

// AttemptRetry checks if a retry is allowed and, if so, increments the counter.
func (e *AgentEngine) AttemptRetry(ctx context.Context, msg *api.UnifiedMessage, reason string, streamErr error, preview string) bool {
	if streamErr != nil && !e.client.IsTransientError(streamErr) {
		slog.ErrorContext(ctx, "Non-transient error, skipping retry", "error", streamErr)
		e.responder.SendReply(msg.Session, fmt.Sprintf("❌ %v", streamErr))
		return false
	}

	sysCfg := e.sysCfg
	maxRetries := sysCfg.MaxRetries
	if msg.RetryCount >= maxRetries {
		slog.ErrorContext(ctx, "Max retries reached", "max", maxRetries, "reason", reason, "error", streamErr)
		e.responder.SendReply(msg.Session, "❌ AI response remains abnormal, please try rephrasing or restarting the conversation.")
		return false
	}

	msg.RetryCount++
	slog.WarnContext(ctx, "Abnormal response, retrying",
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
	e.responder.SendReply(msg.Session, retryNotice)

	time.Sleep(time.Duration(sysCfg.RetryDelayMs) * time.Millisecond)
	return true
}

// SummarizeContent performs a single pass over the message to derive content info.
func SummarizeContent(msg llm.Message) (hasContent, hasThinking bool, preview string) {
	var sb strings.Builder
	sb.Grow(100)

	for _, b := range msg.Content {
		if b.Type == llm.BlockTypeThinking && len(b.Text) > 0 {
			hasThinking = true
		} else if b.Type == llm.BlockTypeText && len(b.Text) > 0 {
			hasContent = true
			if sb.Len() < 100 {
				remaining := 100 - sb.Len()
				if len(b.Text) > remaining {
					sb.WriteString(b.Text[:remaining])
				} else {
					sb.WriteString(b.Text)
				}
			}
		}
	}

	preview = sb.String()
	if len(preview) >= 100 {
		preview += "..."
	}
	return
}

// ConvertToolResult transforms a api.ToolResult into a slice of llm.ContentBlock.
func ConvertToolResult(res *api.ToolResult) []llm.ContentBlock {
	var blocks []llm.ContentBlock
	for _, b := range res.Content {
		if b.Type == llm.BlockTypeImage {
			data, err := tools.Base64Decode(b.Data)
			if err != nil {
				slog.Error("Failed to decode image data", "error", err)
				blocks = append(blocks, llm.NewTextBlock(fmt.Sprintf("Error: Failed to decode image: %v", err)))
				continue
			}
			mimeType := b.MimeType
			if mimeType == "" {
				mimeType = "image/png"
			}
			blocks = append(blocks, llm.NewImageBlock(data, mimeType))
		} else {
			blocks = append(blocks, llm.NewTextBlock(b.Text))
		}
	}
	if len(blocks) == 0 {
		blocks = append(blocks, llm.NewTextBlock("(No output)"))
	}
	return blocks
}
