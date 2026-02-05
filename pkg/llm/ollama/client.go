package ollama

import (
	"context"
	"genesis/pkg/llm"
	"log"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
)

// OllamaClient Ollama API 客戶端
type OllamaClient struct {
	client  *api.Client
	model   string
	options map[string]any
}

// NewOllamaClient 創建 Ollama 客戶端
func NewOllamaClient(model string, baseURL string, options map[string]any) (*OllamaClient, error) {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return nil, err
	}

	log.Printf("%+v\n", options)

	return &OllamaClient{
		client:  client,
		model:   model,
		options: options,
	}, nil
}

func (o *OllamaClient) StreamChat(ctx context.Context, messages []llm.Message) (<-chan llm.StreamChunk, error) {
	// 轉換訊息
	apiMessages := o.convertMessages(messages)

	log.Printf("[Ollama] 🌊 Tapping model: %s...", o.model)

	chunkCh := make(chan llm.StreamChunk, 100)
	startResultCh := make(chan error, 1)

	go func() {
		defer close(chunkCh)

		req := &api.ChatRequest{
			Model:    o.model,
			Messages: apiMessages,
			Options:  o.options,
		}

		started := false
		var thoughtsCount int

		err := o.client.Chat(ctx, req, func(resp api.ChatResponse) error {
			// 第一個 callback 表示成功
			if !started {
				started = true
				startResultCh <- nil
			}

			// 處理思考內容
			if resp.Message.Thinking != "" {
				thoughtsCount++
				chunkCh <- llm.NewThinkingChunk(resp.Message.Thinking)
			}

			// 處理回應內容
			if resp.Message.Content != "" {
				chunkCh <- llm.NewTextChunk(resp.Message.Content)
			}

			// 最後 chunk
			if resp.Done {
				usage := &llm.LLMUsage{
					PromptTokens:     resp.PromptEvalCount,
					CompletionTokens: resp.EvalCount,
					TotalTokens:      resp.PromptEvalCount + resp.EvalCount,
					ThoughtsTokens:   thoughtsCount,
					StopReason:       resp.DoneReason,
				}

				chunkCh <- llm.NewFinalChunk(resp.DoneReason, usage)
				llm.LogUsage(o.model, usage)

				// 截斷警告
				if resp.DoneReason == "length" {
					log.Printf("⚠️ [Ollama] Response truncated due to num_predict limit (%v)", o.options["num_predict"])
				}
			}

			return nil
		})

		if err != nil {
			log.Printf("❌ Ollama stream error (%s): %v", o.model, err)
			if !started {
				startResultCh <- err
			}
		} else if !started {
			startResultCh <- nil
		}
	}()

	// 等待初始化結果
	select {
	case err := <-startResultCh:
		if err != nil {
			log.Printf("⚠️ [Ollama] Model %s failed immediately: %v", o.model, err)
			return nil, err
		}
		return chunkCh, nil
	case <-time.After(2 * time.Second):
		// Timeout - 可能模型正在載入
		log.Printf("⏳ [Ollama] Model %s is loading (timeout). Assuming success...", o.model)
		return chunkCh, nil
	}
}

func (o *OllamaClient) convertMessages(messages []llm.Message) []api.Message {
	var ollamaMsgs []api.Message

	for _, m := range messages {
		var content strings.Builder
		var images []api.ImageData

		for _, block := range m.Content {
			switch block.Type {
			case "text", "thinking":
				// thinking 儲存時合併到 content
				content.WriteString(block.Text)

			case "image":
				if block.Source != nil && len(block.Source.Data) > 0 {
					images = append(images, block.Source.Data)
				}
			}
		}

		msg := api.Message{
			Role:    m.Role,
			Content: content.String(),
		}

		if len(images) > 0 {
			msg.Images = images
		}

		ollamaMsgs = append(ollamaMsgs, msg)
	}

	return ollamaMsgs
}

// IsTransientError 實作 LLMClient 介面
func (o *OllamaClient) IsTransientError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()

	// 1. 連線相關錯誤 (Connection refused, reset)
	if strings.Contains(errMsg, "connection refused") || strings.Contains(errMsg, "connection reset") {
		return true
	}

	// 2. 負載過重
	if strings.Contains(strings.ToLower(errMsg), "overloaded") {
		return true
	}

	return false
}
