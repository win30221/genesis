package handler

import (
	"context"
	"fmt"
	"genesis/pkg/api"
	"genesis/pkg/llm"
	"genesis/pkg/utils"
	"log/slog"
	"time"
)

// ChatHandler orchestrates the conversation flow, maintaining state, session history,
// and coordinating between the Gateway, LLM clients, and Tool registry.
type ChatHandler struct {
	responder api.MessageResponder // Segregated interface for sending replies
	sessions  *llm.SessionManager  // Manager for isolated session histories
	engine    api.AgentEngine      // Reasoning engine (using api interface)
}

// NewChatHandler initializes a ChatHandler instance.
// Note: responder can be nil if set later via SetResponder.
func NewChatHandler(
	engine api.AgentEngine,
	sessions *llm.SessionManager,
) *ChatHandler {
	return &ChatHandler{
		sessions: sessions,
		engine:   engine,
	}
}

// NewMessageHandler initializes a ChatHandler instance and returns a closure
// compatible with the api.MessageHandler type (aliased in gateway).
func NewMessageHandler(
	responder api.MessageResponder,
	engine api.AgentEngine,
	sessions *llm.SessionManager,
) api.MessageHandler {
	h := NewChatHandler(engine, sessions)
	h.SetResponder(responder)
	return h.OnMessage
}

// SetResponder sets the messaging interface used by the handler to send replies.
func (h *ChatHandler) SetResponder(responder api.MessageResponder) {
	h.responder = responder
}

// OnMessage is the primary entry point for processing incoming user messages.
func (h *ChatHandler) OnMessage(msg *api.UnifiedMessage) {
	go func() {
		if msg.DebugID == "" {
			msg.DebugID = utils.GenerateID()
		}

		ctx := context.WithValue(context.Background(), llm.DebugDirContextKey, msg.DebugID)
		start := time.Now()

		fmt.Println()
		slog.InfoContext(ctx, "Message received", "channel", msg.Session.ChannelID, "user", msg.Session.Username, "content", msg.Content, "files", len(msg.Files))

		sessionID := fmt.Sprintf("%s_%s", msg.Session.ChannelID, msg.Session.ChatID)
		history, err := h.sessions.GetHistory(sessionID)
		if err != nil {
			slog.ErrorContext(ctx, "Failed to resolve session history", "session", sessionID, "error", err)
			h.responder.SendReply(msg.Session, "❌ Error loading history.")
			return
		}

		// Simply delegate the message, logic, slash commands and summarization to the AgentEngine
		h.engine.HandleMessage(ctx, msg, history)

		slog.InfoContext(ctx, "Gateway logic finished", "duration", time.Since(start).String())
	}()
}
