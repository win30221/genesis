package telegram

import (
	"context"
	"fmt"
	"genesis/pkg/api"
	"genesis/pkg/llm"
	"genesis/pkg/utils"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	stopCtx      context.Context              // Context used to forcibly abort the long-polling HTTP request
	stopCancel   context.CancelFunc           // Function to trigger the abort
}

// mediaGroupBuffer aggregates multiple incoming messages marked with the
// same MediaGroupID into a single UnifiedMessage. This ensures multi-image
// posts are processed as a single atomic context by the AI.
type mediaGroupBuffer struct {
	session  api.SessionContext // Target session metadata
	content  string             // Aggregated caption text
	photoIDs []string           // Collection of file identifiers
	timer    *time.Timer        // Debounce timer for finishing the group
}

func NewTelegramChannel(cfg TelegramConfig, msgLimit int, timeoutMs int) (api.Channel, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create a dedicated HTTP client for the bot so we can forcefully close it on reload
	// By tying the DialContext to our stopCtx, active long-polling requests will be
	// instantly aborted when Stop() is called, preventing the 409 Conflict.
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	botHttpClient := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			DialContext: func(dialCtx context.Context, network, addr string) (net.Conn, error) {
				// We wrap the context with our stopCtx so we can arbitrarily kill the connection
				mergedCtx, mergedCancel := context.WithCancel(dialCtx)
				go func() {
					select {
					case <-ctx.Done():
						mergedCancel()
					case <-mergedCtx.Done():
					}
				}()
				return dialer.DialContext(mergedCtx, network, addr)
			},
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}

	bot, err := tgbotapi.NewBotAPIWithClient(cfg.Token, tgbotapi.APIEndpoint, botHttpClient)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	slog.Info("Telegram bot authorized", "username", bot.Self.UserName)

	return &TelegramChannel{
		config:       cfg,
		bot:          bot,
		messageLimit: msgLimit,
		mediaGroups:  make(map[string]*mediaGroupBuffer),
		httpClient: &http.Client{
			Timeout: time.Duration(timeoutMs) * time.Millisecond,
		},
		stopCtx:    ctx,
		stopCancel: cancel,
	}, nil
}

// ID returns the unique platform identifier "telegram".
func (t *TelegramChannel) ID() string {
	return "telegram"
}

// Start initiates the long-polling update loop in a background goroutine.
// It maps platform-specific update types (text, photos, albums) into
// the internal UnifiedMessage format.
func (t *TelegramChannel) Start(ctx api.ChannelContext) error {
	offset := 0

	// Process updates in background with manual loop to allow Context cancellation
	go func() {
		for {
			select {
			case <-t.stopCtx.Done():
				return // Gracefully exit on shutdown
			default:
			}

			// We use WithContext to wrap the underlying request so we can cancel it mid-flight
			reqConfig := tgbotapi.NewUpdate(offset)
			reqConfig.Timeout = 60

			// tgbotapi uses Request(c Chattable). We need to do a custom Request wrapped with Context
			// Since tgbotapi doesn't natively expose Context in v5 GetUpdates, we cancel the entire HTTP client
			// by using a custom RoundTripper, or we can just stick to Request but use the package's internal mechanism.
			// Actually, BotAPI.Client *is* our *http.Client. We can just shut it down via CloseIdleConnections? No, it doesn't abort Active ones.
			// Let's manually implement MakeRequest with Context or just wait for timeout.
			// The easiest way to abort an active request on our dedicated http.Client is CloseIdleConnections AND returning from the loop.
			// But since we want immediate abort, let's swap the http.Client out or just let it die.
			// Actually, NewBotAPI creates Requests without context.
			// To forcibly kill the active long-poll request, modifying the transport's DialContext or using ctx-aware client is needed.
			// Wait, the telegram library is inherently flawed for cancellation without Context.
			// BUT, if we simply break the loop on stopCtx.Done(), the goroutine exits. It won't call OnMessage.
			// The Conflict error happens because the new Bot starts *while* the old request is still stuck waiting on the server.
			// To fix this, we need to abort the TCP connection of the active request.
			// Go's http.Transport provides CancelRequest(*http.Request), but we don't have the Request object.

			// Let's use the native GetUpdates instead of GetUpdatesChan so we have control over the offset
			updates, err := t.bot.GetUpdates(reqConfig)
			if err != nil {
				select {
				case <-t.stopCtx.Done():
					return // Ignore error if we are shutting down
				default:
					slog.Debug("Failed to get telegram updates", "error", err)
					time.Sleep(3 * time.Second)
					continue
				}
			}

			for _, update := range updates {
				if update.UpdateID >= offset {
					offset = update.UpdateID + 1

					if update.Message == nil {
						continue
					}

					// Init Session Context
					session := api.SessionContext{
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
					if photoID != "" {
						// Process image asynchronously to avoid blocking the update loop
						go func(s api.SessionContext, text string, pID string) {
							var files []api.FileAttachment
							if file, err := t.downloadPhoto(pID); err == nil {
								files = append(files, *file)
							} else {
								slog.Error("Photo download failed", "error", err)
							}

							msg := &api.UnifiedMessage{
								Session: s,
								Content: text,
								Files:   files,
							}
							ctx.OnMessage(t.ID(), msg)
						}(session, content, photoID)
					} else {
						// Process text immediately
						msg := &api.UnifiedMessage{
							Session: session,
							Content: content,
						}
						ctx.OnMessage(t.ID(), msg)
					}
				}
			}
		}
	}()

	return nil
}

// SendSignal implements the gateway.SignalingChannel interface
func (t *TelegramChannel) SendSignal(session api.SessionContext, signal string) error {
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

// downloadPhoto encapsulates the download logic, streaming directly to disk
func (t *TelegramChannel) downloadPhoto(fileID string) (*api.FileAttachment, error) {
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

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download photo: status code %d", resp.StatusCode)
	}

	// Ensure attachments directory exists
	attachmentsDir := "data/attachments"
	if err := os.MkdirAll(attachmentsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create attachments directory: %w", err)
	}

	// Telegram FileIDs are unique to the file content.
	// We use a glob-based check to skip downloading if any extension of this file exists.
	basePattern := fmt.Sprintf("%s/tg_%s", attachmentsDir, fileID)
	if matches, _ := filepath.Glob(basePattern + "*"); len(matches) > 0 {
		localPath := matches[0]
		mimeType, _ := utils.DetectFileMimeAndExt(localPath)

		// File already exists, return it directly
		return &api.FileAttachment{
			Filename: fileInfo.FilePath,
			MimeType: mimeType,
			Data:     nil, // We don't keep it in memory
			Path:     localPath,
		}, nil
	}

	// Create local file with extension from Telegram's path
	ext := filepath.Ext(fileInfo.FilePath)
	localPath := basePattern + ext

	outFile, err := os.Create(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create local file: %w", err)
	}
	defer outFile.Close()

	// Stream directly to disk
	if _, err := io.Copy(outFile, resp.Body); err != nil {
		return nil, fmt.Errorf("failed to save photo data to disk: %w", err)
	}

	// Final verification: if extension was missing, detect it now and rename
	mimeType, detectedExt := utils.DetectFileMimeAndExt(localPath)
	if ext == "" {
		newPath := basePattern + detectedExt
		if err := os.Rename(localPath, newPath); err == nil {
			localPath = newPath
		}
	}

	return &api.FileAttachment{
		Filename: fileInfo.FilePath,
		MimeType: mimeType,
		Data:     nil, // We don't keep it in memory
		Path:     localPath,
	}, nil
}

func (t *TelegramChannel) handleMediaGroup(ctx api.ChannelContext, groupID string, session api.SessionContext, text string, photoID string) {
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
				files := make([]api.FileAttachment, len(finalBuf.photoIDs))

				for i, pid := range finalBuf.photoIDs {
					wg.Add(1)
					go func(index int, id string) {
						defer wg.Done()
						if file, err := t.downloadPhoto(id); err == nil {
							files[index] = *file
						} else {
							slog.Error("MediaGroup download failed", "file_id", id, "error", err)
						}
					}(i, pid)
				}
				wg.Wait()

				// Clean up empty items (failed downloads)
				var successfulFiles []api.FileAttachment
				for _, f := range files {
					if f.Data != nil {
						successfulFiles = append(successfulFiles, f)
					}
				}

				// Send to Gateway
				msg := &api.UnifiedMessage{
					Session: finalBuf.session,
					Content: finalBuf.content,
					Files:   successfulFiles,
				}
				ctx.OnMessage(t.ID(), msg)
				slog.Info("MediaGroup sent", "group", groupID, "images", fmt.Sprintf("%d/%d", len(successfulFiles), len(finalBuf.photoIDs)), "content_len", len(finalBuf.content))
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
	t.stopCancel() // Cancel our custom long-polling loop immediately

	// Forcefully close lingering HTTP connections
	// Note: HTTP/1.1 connections stuck in Read won't abort via CloseIdleConnections().
	// But it will clear the pool.
	if httpClient, ok := t.bot.Client.(*http.Client); ok && httpClient != nil {
		if transport, ok := httpClient.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	}

	return nil
}

func (t *TelegramChannel) Send(session api.SessionContext, message string) error {
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

func (t *TelegramChannel) sendPhoto(session api.SessionContext, block llm.ContentBlock) error {
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
	} else if block.Source.Type == "file" && block.Source.Path != "" {
		photo = tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(block.Source.Path))
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
func (t *TelegramChannel) Stream(session api.SessionContext, blocks <-chan llm.ContentBlock) error {
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
					slog.Error("Failed to send thinking", "error", err)
				}
				thinkingSent = true
			}
			textBuf.WriteString(block.Text)
		case llm.BlockTypeImage:
			// Send current text buffer first to maintain order
			if textBuf.Len() > 0 {
				replyMsg := "🤖 Assistant response:\n\n" + textBuf.String()
				if err := t.Send(session, replyMsg); err != nil {
					slog.Error("Failed to send text before image", "error", err)
				}
				textBuf.Reset()
			}
			if err := t.sendPhoto(session, block); err != nil {
				slog.Error("Failed to send photo", "error", err)
			}
		}
	}

	// Send thinking process if the loop ends and it hasn't been sent yet
	if thinkingBuf.Len() > 0 && !thinkingSent {
		thinkingMsg := "💭 Reasoning process:\n\n" + thinkingBuf.String()
		if err := t.Send(session, thinkingMsg); err != nil {
			slog.Error("Failed to send thinking", "error", err)
		}
	}

	// Send assistant response (if any)
	if textBuf.Len() > 0 {
		replyMsg := "🤖 Assistant response:\n\n" + textBuf.String()
		return t.Send(session, replyMsg)
	}

	return nil
}
