package gemini

import (
	"context"
	"encoding/json"
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

func (g *GeminiClient) Provider() string {
	return "gemini"
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
func (g *GeminiClient) StreamChat(ctx context.Context, messages []llm.Message, availableTools any) (<-chan llm.StreamChunk, error) {
	// 轉換訊息
	apiMessages, systemInstruction := g.convertMessages(messages)

	// 轉換工具
	var genaiTools []*genai.Tool
	if availableTools != nil {
		if tools, ok := availableTools.([]map[string]any); ok {
			var fds []*genai.FunctionDeclaration
			for _, t := range tools {
				fd := &genai.FunctionDeclaration{
					Name:        t["name"].(string),
					Description: t["description"].(string),
				}
				if params, ok := t["parameters"].(map[string]any); ok {
					schemaB, _ := json.Marshal(params)
					var schema genai.Schema
					json.Unmarshal(schemaB, &schema)
					fd.Parameters = &schema
				}
				fds = append(fds, fd)
			}
			if len(fds) > 0 {
				genaiTools = append(genaiTools, &genai.Tool{
					FunctionDeclarations: fds,
				})
			}
		}
	}

	chunkCh := make(chan llm.StreamChunk, 100)
	startResultCh := make(chan error, 1) // Unbuffered to detect if reader is present

	// log.Printf("[Gemini] 🌊 Streaming with model: %s...", g.model)
	log.Printf("[Gemini] 🌊 Streaming with model: %s...", g.model)

	go func() {
		defer close(chunkCh)

		iter := g.client.Models.GenerateContentStream(ctx, g.model, apiMessages, &genai.GenerateContentConfig{
			SystemInstruction: systemInstruction,
			Tools:             genaiTools,
			ThinkingConfig: &genai.ThinkingConfig{
				IncludeThoughts: true,
			},
		})

		started := false
		var lastUsage *llm.LLMUsage

		for resp, err := range iter {
			if err != nil {
				// 嘗試優先處理最後一次 resp (如果有的話)
				// Google GenAI SDK 迭代器可能在返回錯誤的同時返回最後一點資料
				if resp == nil {
					log.Printf("Gemini Stream Error: %v", err)
					if !started {
						startResultCh <- err
					} else {
						// Stream 中斷，通知使用者
						chunkCh <- llm.NewTextChunk(fmt.Sprintf("\n❌ Stream interrupted: %v", err))
					}
					break
				}
				// 如果 err != nil 但 resp != nil，繼續處理這次的 resp，然後在下一次迭代或是這裡處理錯誤
				// 根據 Go iterator 慣例，這裡我們記錄錯誤但繼續處理當前數據
				log.Printf("Gemini Stream Error (with data): %v", err)
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
					var toolCalls []llm.ToolCall

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

						if part.FunctionCall != nil {
							// 工具調用
							argsB, _ := json.Marshal(part.FunctionCall.Args)
							toolCalls = append(toolCalls, llm.ToolCall{
								ID:   "", // Gemini 串流中 ID 有時不在此處
								Name: part.FunctionCall.Name,
								Function: llm.FunctionCall{
									Name:      part.FunctionCall.Name,
									Arguments: string(argsB),
								},
								// 保存完整的 FunctionCall 以便後續重建（包含 thought_signature 等隱藏欄位）
								Meta: map[string]any{
									"gemini_function_call": part.FunctionCall,
								},
							})
							log.Printf("[Gemini] 🛠️ Tool Call: %s(%s)", part.FunctionCall.Name, string(argsB))
						}
					}

					if len(blocks) > 0 || len(toolCalls) > 0 {
						chunkCh <- llm.StreamChunk{
							ContentBlocks: blocks,
							ToolCalls:     toolCalls,
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

		if msg.Role == "tool" {
			role = "user" // Gemini 中工具結果是 user role 的一部分
			genaiContents = append(genaiContents, &genai.Content{
				Role: role,
				Parts: []*genai.Part{
					{
						FunctionResponse: &genai.FunctionResponse{
							Name:     msg.Role, // 其實應該是工具名稱，這裡暫時簡化
							Response: map[string]any{"result": msg.Content[0].Text},
						},
					},
				},
			})
			continue
		}

		var parts []*genai.Part
		// 先檢查是否有舊的 ToolCall (如果有，Gemini 需要回傳對應的 FunctionCall)
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				// 優先使用保存的原始 FunctionCall（包含 thought_signature）
				if tc.Meta != nil {
					if originalFC, ok := tc.Meta["gemini_function_call"].(*genai.FunctionCall); ok {
						parts = append(parts, &genai.Part{
							FunctionCall: originalFC,
						})
						continue
					}
				}

				// 如果沒有保存的原始資料，則手動重建（可能會缺少 thought_signature）
				var args map[string]any
				json.Unmarshal([]byte(tc.Function.Arguments), &args)
				parts = append(parts, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						Name: tc.Function.Name,
						Args: args,
					},
				})
			}
		}

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
