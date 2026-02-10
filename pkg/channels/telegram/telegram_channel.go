package telegram

import (
	"fmt"
	"genesis/pkg/gateway"
	"genesis/pkg/llm"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// TelegramConfig encapsulates the credentials required to authenticate with
// the Telegram Bot API.
type TelegramConfig struct {
	Token string `json:"token"` // The secret BOT API string provided by @BotFather
}

// TelegramChannel is the production implementation of gateway.Channel for
// the Telegram platform. It handles multi-modal message reception,
// media group buffering (albums), and fragmented response streaming.
type TelegramChannel struct {
	config       TelegramConfig               // Auth credentials
	bot          *tgbotapi.BotAPI             // Underlying Telegram SDK client
	updates      tgbotapi.UpdatesChannel      // Stream of incoming events
	messageLimit int                          // Maximum character count per single message bubble
	mediaGroups  map[string]*mediaGroupBuffer // Buffer for grouping multiple images sent together
	httpClient   *http.Client                 // Client for downloading remote media from Telegram
	mu           sync.Mutex                   // Protects concurrent access to internal buffers
}

// mediaGroupBuffer aggregates multiple incoming messages marked with the
// same MediaGroupID into a single UnifiedMessage. This ensures multi-image
// posts are processed as a single atomic context by the AI.
type mediaGroupBuffer struct {
	session  gateway.SessionContext // Target session metadata
	content  string                 // Aggregated caption text
	photoIDs []string               // Collection of file identifiers
	timer    *time.Timer            // Debounce timer for finishing the group
}

func NewTelegramChannel(cfg TelegramConfig, msgLimit int, timeoutMs int) (*TelegramChannel, error) {
	bot, err := tgbotapi.NewBotAPI(cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	log.Printf("🤖 Authorized on account %s", bot.Self.UserName)

	return &TelegramChannel{
		config:       cfg,
		bot:          bot,
		messageLimit: msgLimit,
		mediaGroups:  make(map[string]*mediaGroupBuffer),
		httpClient: &http.Client{
			Timeout: time.Duration(timeoutMs) * time.Millisecond,
		},
	}, nil
}

// ID returns the unique platform identifier "telegram".
func (t *TelegramChannel) ID() string {
	return "telegram"
}

// Start initiates the long-polling update loop in a background goroutine.
// It maps platform-specific update types (text, photos, albums) into
// the internal UnifiedMessage format.
func (t *TelegramChannel) Start(ctx gateway.ChannelContext) error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	t.updates = t.bot.GetUpdatesChan(u)

	// Process updates in background
	go func() {
		for update := range t.updates {
			if update.Message == nil {
				continue
			}

			// Init Session Context
			session := gateway.SessionContext{
				ChannelID: "telegram",
				UserID:    strconv.FormatInt(update.Message.From.ID, 10),
				ChatID:    strconv.FormatInt(update.Message.Chat.ID, 10),
				Username:  update.Message.From.UserName,
			}

			// Identify photos but don't download yet to avoid blocking group logic
			var photoID string
			if len(update.Message.Photo) > 0 {
				photoID = update.Message.Photo[len(update.Message.Photo)-1].FileID
			}

			// Get content
			content := update.Message.Text
			if content == "" {
				content = update.Message.Caption
			}

			// Handle MediaGroup (album/collection)
			if update.Message.MediaGroupID != "" {
				t.handleMediaGroup(ctx, update.Message.MediaGroupID, session, content, photoID)
				continue
			}

			// Regular message (single image or plain text)
			var files []gateway.FileAttachment
			if photoID != "" {
				if file, err := t.downloadPhoto(photoID); err == nil {
					files = append(files, *file)
				} else {
					log.Printf("❌ Photo download failed: %v", err)
				}
			}

			msg := &gateway.UnifiedMessage{
				Session: session,
				Content: content,
				Files:   files,
			}
			ctx.OnMessage(t.ID(), msg)
		}
	}()

	return nil
}

// SendSignal implements the gateway.SignalingChannel interface
func (t *TelegramChannel) SendSignal(session gateway.SessionContext, signal string) error {
	if signal == llm.BlockTypeThinking {
		chatID, err := strconv.ParseInt(session.ChatID, 10, 64)
		if err != nil {
			return err
		}
		action := tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping)
		_, err = t.bot.Send(action)
		return err
	}
	return nil
}

// downloadPhoto encapsulates the download logic
func (t *TelegramChannel) downloadPhoto(fileID string) (*gateway.FileAttachment, error) {
	// Use Telegram API to get file info (contains Path)
	fileInfo, err := t.bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return nil, fmt.Errorf("failed to get photo file info: %w", err)
	}

	// Combine download URL directly from Token to reduce API round trips
	fileURL := fileInfo.Link(t.config.Token)

	// Download content
	resp, err := t.httpClient.Get(fileURL)
	if err != nil {
		return nil, fmt.Errorf("failed to download photo: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read photo data: %w", err)
	}

	// Detect MIME type automatically
	mimeType := http.DetectContentType(data)

	return &gateway.FileAttachment{
		Filename: fileInfo.FilePath,
		MimeType: mimeType,
		Data:     data,
	}, nil
}

func (t *TelegramChannel) handleMediaGroup(ctx gateway.ChannelContext, groupID string, session gateway.SessionContext, text string, photoID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	buf, ok := t.mediaGroups[groupID]
	if !ok {
		// Create new buffer
		buf = &mediaGroupBuffer{
			session:  session,
			content:  text,
			photoIDs: []string{},
		}
		if photoID != "" {
			buf.photoIDs = append(buf.photoIDs, photoID)
		}
		t.mediaGroups[groupID] = buf

		// Set timer (send after 1s to allow more incoming media)
		buf.timer = time.AfterFunc(time.Second, func() {
			t.mu.Lock()
			if finalBuf, exists := t.mediaGroups[groupID]; exists {
				delete(t.mediaGroups, groupID)
				t.mu.Unlock()

				// Download all photos in parallel
				var wg sync.WaitGroup
				files := make([]gateway.FileAttachment, len(finalBuf.photoIDs))

				for i, pid := range finalBuf.photoIDs {
					wg.Add(1)
					go func(index int, id string) {
						defer wg.Done()
						if file, err := t.downloadPhoto(id); err == nil {
							files[index] = *file
						} else {
							log.Printf("❌ MediaGroup download failed (id: %s): %v", id, err)
						}
					}(i, pid)
				}
				wg.Wait()

				// Clean up empty items (failed downloads)
				var successfulFiles []gateway.FileAttachment
				for _, f := range files {
					if f.Data != nil {
						successfulFiles = append(successfulFiles, f)
					}
				}

				// Send to Gateway
				msg := &gateway.UnifiedMessage{
					Session: finalBuf.session,
					Content: finalBuf.content,
					Files:   successfulFiles,
				}
				ctx.OnMessage(t.ID(), msg)
				log.Printf("📦 Sent MediaGroup %s (%d/%d images, content len: %d)",
					groupID, len(successfulFiles), len(finalBuf.photoIDs), len(finalBuf.content))
			} else {
				t.mu.Unlock()
			}
		})
	} else {
		// Accumulate content and photos
		if text != "" {
			if buf.content != "" {
				buf.content += "\n" + text
			} else {
				buf.content = text
			}
		}
		if photoID != "" {
			buf.photoIDs = append(buf.photoIDs, photoID)
		}

		// Reset timer
		buf.timer.Reset(time.Second)
	}
}

func (t *TelegramChannel) Stop() error {
	t.bot.StopReceivingUpdates()
	return nil
}

func (t *TelegramChannel) Send(session gateway.SessionContext, message string) error {
	// Telegram Chat ID must be int64
	chatID, err := strconv.ParseInt(session.ChatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat id for telegram: %s", session.ChatID)
	}

	msgRunes := []rune(message)
	totalLen := len(msgRunes)

	if totalLen <= t.messageLimit {
		// Send short message directly
		msg := tgbotapi.NewMessage(chatID, message)
		if _, err := t.bot.Send(msg); err != nil {
			return fmt.Errorf("telegram send failed: %w", err)
		}
		return nil
	}

	// Send long message in chunks
	for i := 0; i < totalLen; i += t.messageLimit {
		end := i + t.messageLimit
		if end > totalLen {
			end = totalLen
		}
		chunk := string(msgRunes[i:end])
		msg := tgbotapi.NewMessage(chatID, chunk)
		if _, err := t.bot.Send(msg); err != nil {
			return fmt.Errorf("telegram send chunk failed at index %d: %w", i, err)
		}
	}

	return nil
}

func (t *TelegramChannel) sendPhoto(session gateway.SessionContext, block llm.ContentBlock) error {
	chatID, err := strconv.ParseInt(session.ChatID, 10, 64)
	if err != nil {
		return err
	}

	if block.Source == nil {
		return fmt.Errorf("image source is nil")
	}

	var photo tgbotapi.Chattable
	if block.Source.Type == "base64" && len(block.Source.Data) > 0 {
		photo = tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{
			Name:  "screenshot.png",
			Bytes: block.Source.Data,
		})
	} else if block.Source.Type == "url" {
		photo = tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(block.Source.URL))
	} else {
		return fmt.Errorf("unsupported image source type: %s", block.Source.Type)
	}

	_, err = t.bot.Send(photo)
	return err
}

// Stream implements the streaming response protocol for Telegram.
// Since Telegram doesn't natively support mid-message streaming updates,
// this implementation uses an "Accumulation + Buffered Flush" strategy:
// 1. Thinking blocks are collected and sent as an initial bubble.
// 2. Text blocks are aggregated until the stream ends or an image/tool occurs.
// 3. Images are sent immediately as separate messages.
func (t *TelegramChannel) Stream(session gateway.SessionContext, blocks <-chan llm.ContentBlock) error {
	var thinkingBuf strings.Builder
	var textBuf strings.Builder
	var thinkingSent bool

	for block := range blocks {
		switch block.Type {
		case llm.BlockTypeThinking:
			thinkingBuf.WriteString(block.Text)
		case llm.BlockTypeText, llm.BlockTypeError:
			// Send thinking buffer when the first text block arrives if not already sent
			if thinkingBuf.Len() > 0 && !thinkingSent {
				thinkingMsg := "💭 Reasoning process:\n\n" + thinkingBuf.String()
				if err := t.Send(session, thinkingMsg); err != nil {
					log.Printf("❌ Failed to send thinking message: %v", err)
				}
				thinkingSent = true
			}
			textBuf.WriteString(block.Text)
		case llm.BlockTypeImage:
			// Send current text buffer first to maintain order
			if textBuf.Len() > 0 {
				replyMsg := "🤖 Assistant response:\n\n" + textBuf.String()
				if err := t.Send(session, replyMsg); err != nil {
					log.Printf("❌ Failed to send buffered text before image: %v", err)
				}
				textBuf.Reset()
			}
			if err := t.sendPhoto(session, block); err != nil {
				log.Printf("❌ Failed to send photo in stream: %v", err)
			}
		}
	}

	// Send thinking process if the loop ends and it hasn't been sent yet
	if thinkingBuf.Len() > 0 && !thinkingSent {
		thinkingMsg := "💭 Reasoning process:\n\n" + thinkingBuf.String()
		if err := t.Send(session, thinkingMsg); err != nil {
			log.Printf("❌ Failed to send thinking message: %v", err)
		}
	}

	// Send assistant response (if any)
	if textBuf.Len() > 0 {
		replyMsg := "🤖 Assistant response:\n\n" + textBuf.String()
		return t.Send(session, replyMsg)
	}

	return nil
}
