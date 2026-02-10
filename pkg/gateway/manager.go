package gateway

import (
	"fmt"
	"genesis/pkg/llm"
	"genesis/pkg/monitor"
	"log"
	"strings"
	"sync"
	"time"
)

// MessageHandler is a callback function type that defines the processing
// signature for standardized messages arriving from the Gateway.
// It is typically implemented by the core processing unit (e.g., ChatHandler).
type MessageHandler func(msg *UnifiedMessage)

// GatewayManager is the central orchestration hub that manages multiple
// communication channels and unifies message routing for both input and output.
// It implements the ChannelContext interface to receive callbacks from channels.
type GatewayManager struct {
	channels      map[string]Channel // Registry of active channel instances indexed by ID
	msgHandler    MessageHandler     // Callback for business logic processing
	monitor       monitor.Monitor    // Interface for broadcasting message logs to monitoring tools
	channelBuffer int                // Buffer size for internal Go channels during streaming
	mu            sync.RWMutex       // Mutex protecting the concurrent access to the channels map
}

// NewGatewayManager initializes a new GatewayManager instance with default
// parameters like a standard internal channel buffer size.
func NewGatewayManager() *GatewayManager {
	return &GatewayManager{
		channels:      make(map[string]Channel),
		channelBuffer: 100, // Default buffer size for stream wrapping
	}
}

// SetChannelBuffer configures the size of internal Go channels used in
// StreamReply to prevent blocking during chunk processing.
func (g *GatewayManager) SetChannelBuffer(size int) {
	if size > 0 {
		g.channelBuffer = size
	}
}

// SetMessageHandler injects the core logic callback that will be invoked
// whenever a standardized message is received from any registered channel.
func (g *GatewayManager) SetMessageHandler(handler MessageHandler) {
	g.msgHandler = handler
}

// SetMonitor sets the monitoring implementation responsible for displaying
// or persisting user and assistant messages for observatory purposes.
func (g *GatewayManager) SetMonitor(m monitor.Monitor) {
	g.monitor = m
}

// Register adds a new communication Channel instance to the manager's registry.
func (g *GatewayManager) Register(c Channel) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.channels[c.ID()] = c
}

// GetChannel retrieves a specifically registered Channel instance by its ID.
// This is commonly used for high-level routing or proactive messaging.
func (g *GatewayManager) GetChannel(id string) (Channel, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	c, ok := g.channels[id]
	return c, ok
}

// StartAll iterates through all registered channels and invokes their
// Start() method, passing the manager itself as the ChannelContext.
// Returns the first error encountered during the batch startup process.
func (g *GatewayManager) StartAll() error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	for id, c := range g.channels {
		log.Printf("Starting channel: %s", id)
		// Inject self as the context for receiving messages from the channel
		if err := c.Start(g); err != nil {
			return fmt.Errorf("failed to start channel %s: %w", id, err)
		}
	}
	return nil
}

// StopAll gracefully shuts down all registered channels to release system
// resources like network listeners or API long-polling workers.
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

// SendReply is a convenience wrapper around StreamReply for sending simple
// text messages. It packages the content into a single ContentBlock and
// delegates to Stream, ensuring all replies follow one unified code path.
func (g *GatewayManager) SendReply(session SessionContext, content string) error {
	ch := make(chan llm.ContentBlock, 1)
	ch <- llm.ContentBlock{Type: llm.BlockTypeText, Text: content}
	close(ch)
	return g.StreamReply(session, ch)
}

// SendSignal transmits a control signal (tipically for UI updates like
// typing indicators) to the target channel if it supports SignalingChannel.
func (g *GatewayManager) SendSignal(session SessionContext, signal string) error {
	c, ok := g.GetChannel(session.ChannelID)
	if !ok {
		return fmt.Errorf("channel %s not found", session.ChannelID)
	}

	// Verify if the channel implementation supports control signaling
	if sc, ok := c.(SignalingChannel); ok {
		log.Printf("[Gateway] -> Signal to %s (%s): %s", session.ChannelID, session.Username, signal)
		return sc.SendSignal(session, signal)
	}

	// Silently ignore signal attempts for unsupported platforms (e.g., CLI)
	return nil
}

// StreamReply handles multi-block streaming content. It wraps the provided
// blocks channel to concurrently forward data while aggregating text for the monitor.
func (g *GatewayManager) StreamReply(session SessionContext, blocks <-chan llm.ContentBlock) error {
	c, ok := g.GetChannel(session.ChannelID)
	if !ok {
		return fmt.Errorf("channel %s not found", session.ChannelID)
	}

	// Create a wrapper channel to calculate full content while streaming
	wrappedBlocks := make(chan llm.ContentBlock, g.channelBuffer)
	var sb strings.Builder

	go func() {
		defer close(wrappedBlocks)
		for block := range blocks {
			// Aggregate text blocks only for monitoring historical summary
			if block.Type == llm.BlockTypeText {
				sb.WriteString(block.Text)
			}
			wrappedBlocks <- block
		}
		// Finalize the monitor entry once the stream is fully drained
		if sb.Len() > 0 && g.monitor != nil {
			g.monitor.OnMessage(monitor.MonitorMessage{
				Timestamp:   time.Now(),
				MessageType: "ASSISTANT",
				ChannelID:   session.ChannelID,
				Username:    session.Username,
				Content:     sb.String(),
			})
		}
	}()

	return c.Stream(session, wrappedBlocks)
}

// OnMessage implements the ChannelContext interface. It receives standardized
// messages from channels, logs them, broadcasts to monitor, and forwards to handler.
func (g *GatewayManager) OnMessage(channelID string, msg *UnifiedMessage) {
	// Structured logging for inbound user communications
	log.Printf("[Gateway] <- Received from %s [%s(%s)]: %s",
		channelID, msg.Session.Username, msg.Session.UserID, msg.Content)

	// Broadcast the user message to the monitor for real-time observation
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
		// Forward message to the business logic handler (e.g., ChatHandler)
		g.msgHandler(msg)
	} else {
		log.Println("[Gateway] Warning: No message handler set")
	}
}
