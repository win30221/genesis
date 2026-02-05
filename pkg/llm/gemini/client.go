package gemini

import (
	"context"
	"fmt"
	"genesis/pkg/llm"
	"log"
	"strings"

	"google.golang.org/genai"
)

// GeminiClient Google Gemini API 客戶端
type GeminiClient struct {
	client *genai.Client
	model  string
}

// NewGeminiClient 創建單一模型/單一 Key 的 Gemini 客戶端
func NewGeminiClient(apiKey string, model string, useThought bool) *GeminiClient {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Fatalf("❌ Fatal: Failed to create Gemini client: %v", err)
	}

	return &GeminiClient{
		client: client,
		model:  model,
	}
}

// 格式化 ModalityTokenCount 陣列
func formatModality(details []*genai.ModalityTokenCount) string {
	if len(details) == 0 {
		return "0"
	}
	var res []string
	for _, d := range details {
		res = append(res, fmt.Sprintf("%v: %d", d.Modality, d.TokenCount))
	}
	return strings.Join(res, " | ")
}

// StreamChat 實作 LLMClient.StreamChat
func (g *GeminiClient) StreamChat(ctx context.Context, messages []llm.Message) (<-chan llm.StreamChunk, error) {
	// 轉換訊息
	apiMessages, systemInstruction := g.convertMessages(messages)

	chunkCh := make(chan llm.StreamChunk, 100)
	startResultCh := make(chan error, 1)

	log.Printf("[Gemini] 🌊 Streaming with model: %s...", g.model)

	go func() {
		defer close(chunkCh)

		iter := g.client.Models.GenerateContentStream(ctx, g.model, apiMessages, &genai.GenerateContentConfig{
			SystemInstruction: systemInstruction,
			ThinkingConfig: &genai.ThinkingConfig{
				IncludeThoughts: true,
			},
		})

		started := false
		var lastUsage *llm.LLMUsage

		for resp, err := range iter {
			if err != nil {
				if !started {
					startResultCh <- err
				} else {
					log.Printf("Gemini Stream Error: %v", err)
				}
				break
			}

			if !started {
				started = true
				startResultCh <- nil // 第一個 chunk 成功
			}

			// Capture Usage Metadata (usually in the last chunk)
			if resp.UsageMetadata != nil {
				u := resp.UsageMetadata
				lastUsage = &llm.LLMUsage{
					PromptTokens:     int(u.PromptTokenCount),
					PromptDetail:     formatModality(u.PromptTokensDetails),
					CompletionTokens: int(u.CandidatesTokenCount),
					CompletionDetail: formatModality(u.CandidatesTokensDetails),
					TotalTokens:      int(u.TotalTokenCount),
					ThoughtsTokens:   int(u.ThoughtsTokenCount),
					CachedTokens:     int(u.CachedContentTokenCount),
				}
			}

			for _, candidate := range resp.Candidates {
				if candidate.FinishReason != "" && lastUsage != nil {
					lastUsage.StopReason = string(candidate.FinishReason)
				}

				if candidate.Content != nil {
					var blocks []llm.ContentBlock
					for _, part := range candidate.Content.Parts {
						if part.Text != "" {
							if part.Thought {
								// 思考內容
								blocks = append(blocks, llm.ContentBlock{
									Type: "thinking",
									Text: part.Text,
								})
							} else {
								// 正常回應
								blocks = append(blocks, llm.ContentBlock{
									Type: "text",
									Text: part.Text,
								})
							}
						}
					}

					if len(blocks) > 0 {
						chunkCh <- llm.StreamChunk{
							ContentBlocks: blocks,
						}
					}
				}
			}
		}

		// 發送最終 chunk（帶用量統計）
		if lastUsage != nil {
			chunkCh <- llm.NewFinalChunk(lastUsage.StopReason, lastUsage)
			llm.LogUsage(g.model, lastUsage)
		}
	}()

	// 等待初始化結果 (第一個 chunk 或立即報錯)
	select {
	case err := <-startResultCh:
		if err != nil {
			return nil, err
		}
		return chunkCh, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// convertMessages 轉換訊息列表
func (g *GeminiClient) convertMessages(messages []llm.Message) ([]*genai.Content, *genai.Content) {
	var genaiContents []*genai.Content
	var systemInstruction *genai.Content

	for _, msg := range messages {
		if msg.Role == "system" {
			// System 作為 SystemInstruction
			var parts []*genai.Part
			for _, block := range msg.Content {
				if block.Type == "text" && block.Text != "" {
					parts = append(parts, &genai.Part{Text: block.Text})
				}
			}
			if len(parts) > 0 {
				systemInstruction = &genai.Content{Parts: parts}
			}
			continue
		}

		role := "user"
		if msg.Role == "assistant" {
			role = "model"
		}

		var parts []*genai.Part
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				if block.Text == "" {
					continue // 略過空文本
				}
				parts = append(parts, &genai.Part{Text: block.Text})

			case "thinking":
				if block.Text == "" {
					continue
				}
				// 儲存時思考內容標記為 Thought
				parts = append(parts, &genai.Part{
					Text:    block.Text,
					Thought: true,
				})

			case "image":
				if block.Source != nil && len(block.Source.Data) > 0 {
					parts = append(parts, &genai.Part{
						InlineData: &genai.Blob{
							MIMEType: block.Source.MediaType,
							Data:     block.Source.Data,
						},
					})
				}
			}
		}

		if len(parts) > 0 {
			genaiContents = append(genaiContents, &genai.Content{
				Role:  role,
				Parts: parts,
			})
		}
	}

	return genaiContents, systemInstruction
}

// IsTransientError 實作 LLMClient 介面
func (g *GeminiClient) IsTransientError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()

	// 1. Google API 常見的 503 Service Unavailable / Overloaded
	if strings.Contains(errMsg, "503") || strings.Contains(strings.ToLower(errMsg), "overloaded") {
		return true
	}

	// 2. 429 Too Many Requests (Rate Limit)
	if strings.Contains(errMsg, "429") || strings.Contains(strings.ToLower(errMsg), "resource exhausted") {
		return true
	}

	return false
}
