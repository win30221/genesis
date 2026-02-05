package telegram

import (
	"fmt"
	"genesis/pkg/gateway"
	"genesis/pkg/llm"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// TelegramConfig 定義 Telegram 相關設定
type TelegramConfig struct {
	Token string `json:"token"`
}

// TelegramChannel 實作 gateway.Channel 介面
type TelegramChannel struct {
	config       TelegramConfig
	bot          *tgbotapi.BotAPI
	updates      tgbotapi.UpdatesChannel
	messageLimit int // Configurable message limit
	mediaGroups  map[string]*mediaGroupBuffer
	httpClient   *http.Client
	mu           sync.Mutex
}

type mediaGroupBuffer struct {
	session  gateway.SessionContext
	content  string
	photoIDs []string
	timer    *time.Timer
}

func NewTelegramChannel(cfg TelegramConfig, msgLimit int, timeoutSec int) (*TelegramChannel, error) {
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
			Timeout: time.Duration(timeoutSec) * time.Second,
		},
	}, nil
}

func (t *TelegramChannel) ID() string {
	return "telegram"
}

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

			// 辨識圖片但先不下載，避免阻塞分組邏輯
			var photoID string
			if len(update.Message.Photo) > 0 {
				photoID = update.Message.Photo[len(update.Message.Photo)-1].FileID
			}

			// Get content
			content := update.Message.Text
			if content == "" {
				content = update.Message.Caption
			}

			// 處理 MediaGroup (相簿/合集)
			if update.Message.MediaGroupID != "" {
				t.handleMediaGroup(ctx, update.Message.MediaGroupID, session, content, photoID)
				continue
			}

			// 一般訊息 (單張圖片或純文字)
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

// downloadPhoto 封裝下載邏輯
func (t *TelegramChannel) downloadPhoto(fileID string) (*gateway.FileAttachment, error) {
	// 使用 Telegram API 取得檔案資訊（包含 Path）
	fileInfo, err := t.bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return nil, fmt.Errorf("failed to get photo file info: %w", err)
	}

	// 直接從 Token 組合下載 URL，減少一次 GetFileDirectURL 的 API 往返
	fileURL := fileInfo.Link(t.config.Token)

	// 下載內容
	resp, err := t.httpClient.Get(fileURL)
	if err != nil {
		return nil, fmt.Errorf("failed to download photo: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read photo data: %w", err)
	}

	// 自動偵測 MIME 類型
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
		// 建立新緩衝區
		buf = &mediaGroupBuffer{
			session:  session,
			content:  text,
			photoIDs: []string{},
		}
		if photoID != "" {
			buf.photoIDs = append(buf.photoIDs, photoID)
		}
		t.mediaGroups[groupID] = buf

		// 設定定時器 (1秒後發送，給下載預留空間)
		buf.timer = time.AfterFunc(time.Second, func() {
			t.mu.Lock()
			if finalBuf, exists := t.mediaGroups[groupID]; exists {
				delete(t.mediaGroups, groupID)
				t.mu.Unlock()

				// 在定時器內「併發」下載所有圖片
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

				// 清理下載失敗的空項目
				var successfulFiles []gateway.FileAttachment
				for _, f := range files {
					if f.Data != nil {
						successfulFiles = append(successfulFiles, f)
					}
				}

				// 發送到 Gateway
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
		// 累積內容與圖片
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

		// 延長定時器
		buf.timer.Reset(time.Second)
	}
}

func (t *TelegramChannel) Stop() error {
	t.bot.StopReceivingUpdates()
	return nil
}

func (t *TelegramChannel) Send(session gateway.SessionContext, message string) error {
	// Telegram Chat ID 必須是 int64
	chatID, err := strconv.ParseInt(session.ChatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat id for telegram: %s", session.ChatID)
	}

	msgRunes := []rune(message)
	totalLen := len(msgRunes)

	if totalLen <= t.messageLimit {
		// 短訊息直接發送
		msg := tgbotapi.NewMessage(chatID, message)
		if _, err := t.bot.Send(msg); err != nil {
			return fmt.Errorf("telegram send failed: %w", err)
		}
		return nil
	}

	// 長訊息分段發送
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

// Stream 實作 gateway.Channel.Stream
// Telegram 採用累積+分段發送的策略，並將 thinking 和 text 分成兩個獨立訊息
func (t *TelegramChannel) Stream(session gateway.SessionContext, blocks <-chan llm.ContentBlock) error {
	var thinkingBuffer string
	var textBuffer string
	var thinkingSent bool

	for block := range blocks {
		switch block.Type {
		case "thinking":
			thinkingBuffer += block.Text
		case "text":
			// 當收到第一個文字塊時，如果思考內容還沒發送，先發送思考內容
			if thinkingBuffer != "" && !thinkingSent {
				thinkingMsg := "💭 思考過程：\n\n" + thinkingBuffer
				if err := t.Send(session, thinkingMsg); err != nil {
					log.Printf("❌ Failed to send thinking message: %v", err)
				}
				thinkingSent = true
			}
			textBuffer += block.Text
		}
	}

	// 先發送思考過程（如果迴圈結束還沒發過，例如只有思考或結束太快）
	if thinkingBuffer != "" && !thinkingSent {
		thinkingMsg := "💭 思考過程：\n\n" + thinkingBuffer
		if err := t.Send(session, thinkingMsg); err != nil {
			log.Printf("❌ Failed to send thinking message: %v", err)
		}
	}

	// 再發送回覆內容（如果有）
	if textBuffer != "" {
		replyMsg := "🤖 回答內容：\n\n" + textBuffer
		return t.Send(session, replyMsg)
	}

	return nil
}
