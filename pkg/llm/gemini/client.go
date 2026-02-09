package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"genesis/pkg/llm"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/genai"
)

// GeminiClient Google Gemini API client
type GeminiClient struct {
	client       *genai.Client
	model        string
	useThought   bool
	debugEnabled bool
}

// SetDebug implements the llm.LLMClient interface
func (g *GeminiClient) SetDebug(enabled bool) {
	g.debugEnabled = enabled
}

// NewGeminiClient creates a Gemini client with a single model and API key
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
		client:     client,
		model:      model,
		useThought: useThought,
	}
}

func (g *GeminiClient) Provider() string {
	return "gemini"
}

// formatModality formats ModalityTokenCount array for logging
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

// StreamChat implements llm.LLMClient.StreamChat
func (g *GeminiClient) StreamChat(ctx context.Context, messages []llm.Message, availableTools any) (<-chan llm.StreamChunk, error) {
	// Convert messages
	apiMessages, systemInstruction := g.convertMessages(messages)

	// Convert tools
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

	log.Printf("[Gemini] 🌊 Streaming with model: %s...", g.model)

	go func() {
		defer close(chunkCh)

		// Build ThinkingConfig based on useThought flag
		var thinkingCfg *genai.ThinkingConfig
		if g.useThought {
			thinkingCfg = &genai.ThinkingConfig{
				IncludeThoughts: true,
			}
		}

		iter := g.client.Models.GenerateContentStream(ctx, g.model, apiMessages, &genai.GenerateContentConfig{
			SystemInstruction: systemInstruction,
			Tools:             genaiTools,
			ThinkingConfig:    thinkingCfg,
		})

		started := false
		var lastUsage *llm.LLMUsage

		// If debug mode is enabled, open file once for the entire stream
		var debugFile *os.File
		if g.debugEnabled {
			debugID, _ := ctx.Value(llm.DebugDirContextKey).(string)
			if debugID == "" {
				debugID = time.Now().Format("20060102_150405")
			}
			debugDir := filepath.Join("debug", "chunks", "gemini")
			_ = os.MkdirAll(debugDir, 0755)
			debugFilePath := filepath.Join(debugDir, fmt.Sprintf("%s.log", debugID))
			log.Printf("[Gemini] 🛠️ Debug mode ON. Chunks will be appended to: %s", debugFilePath)
			if f, err := os.OpenFile(debugFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
				debugFile = f
				defer debugFile.Close()
			}
		}

		for resp, err := range iter {
			// Save raw packet
			if debugFile != nil && resp != nil {
				jsonData, _ := json.Marshal(resp)
				debugFile.Write(jsonData)
				debugFile.WriteString("\n")
			}
			if err != nil {
				// Try to process last resp if available
				// Google GenAI SDK iterator might return some data along with the error
				if resp == nil {
					log.Printf("Gemini Stream Error: %v", err)
					if !started {
						startResultCh <- err
					} else {
						// Stream interrupted, notify user
						chunkCh <- llm.NewErrorChunk(fmt.Sprintf("Stream interrupted: %v", err), err, true)
					}
					break
				}
				// If err != nil but resp != nil, continue processing this resp, then handle error in next iteration
				log.Printf("Gemini Stream Error (with data): %v", err)
			}

			if !started {
				started = true
				startResultCh <- nil // First chunk successful
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
					if candidate.FinishReason == "FINISH_REASON_MAX_TOKENS" {
						chunkCh <- llm.NewErrorChunk("Response truncated due to max tokens limit. You might want to adjust your prompt or settings.", nil, false)
					}
				}

				if candidate.Content != nil {
					var blocks []llm.ContentBlock
					var toolCalls []llm.ToolCall

					for _, part := range candidate.Content.Parts {
						if part.Text != "" {
							if part.Thought {
								// Thinking content
								blocks = append(blocks, llm.ContentBlock{
									Type: "thinking",
									Text: part.Text,
								})
							} else {
								// Normal response
								blocks = append(blocks, llm.ContentBlock{
									Type: "text",
									Text: part.Text,
								})
							}
						}

						if part.FunctionCall != nil {
							// Tool call
							argsB, _ := json.Marshal(part.FunctionCall.Args)
							toolCalls = append(toolCalls, llm.ToolCall{
								ID:   "", // Gemini stream IDs are sometimes missing here
								Name: part.FunctionCall.Name,
								Function: llm.FunctionCall{
									Name:      part.FunctionCall.Name,
									Arguments: string(argsB),
								},
								// Save original FunctionCall for reconstruction (includes thought_signature, etc.)
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

		// Send final chunk (with usage stats)
		if lastUsage != nil {
			chunkCh <- llm.NewFinalChunk(lastUsage.StopReason, lastUsage)
			llm.LogUsage(g.model, lastUsage)
		}
	}()

	// Wait for initialization result (first chunk or immediate error)
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

// convertMessages converts message list to GenAI format
func (g *GeminiClient) convertMessages(messages []llm.Message) ([]*genai.Content, *genai.Content) {
	var genaiContents []*genai.Content
	var systemInstruction *genai.Content

	for _, msg := range messages {
		if msg.Role == "system" {
			// System role as SystemInstruction
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
			role = "user" // Tool results are part of user role in Gemini
			genaiContents = append(genaiContents, &genai.Content{
				Role: role,
				Parts: []*genai.Part{
					{
						FunctionResponse: &genai.FunctionResponse{
							Name:     msg.Role, // Simplified for now
							Response: map[string]any{"result": msg.Content[0].Text},
						},
					},
				},
			})
			continue
		}

		var parts []*genai.Part
		// Check for previous ToolCalls (Gemini requires echoing them before response)
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				// Use original FunctionCall if available (includes thought_signature)
				if tc.Meta != nil {
					if originalFC, ok := tc.Meta["gemini_function_call"].(*genai.FunctionCall); ok {
						parts = append(parts, &genai.Part{
							FunctionCall: originalFC,
						})
						continue
					}
				}

				// Rebuild manually if original data is missing (may miss thought_signature)
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
				// Mark reasoning content as Thought when saving
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

// IsTransientError implements the llm.LLMClient interface
func (g *GeminiClient) IsTransientError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()

	// 1. Google API common 503 Service Unavailable / Overloaded
	if strings.Contains(errMsg, "503") || strings.Contains(strings.ToLower(errMsg), "overloaded") {
		return true
	}

	// 2. 429 Too Many Requests (Rate Limit)
	if strings.Contains(errMsg, "429") || strings.Contains(strings.ToLower(errMsg), "resource exhausted") {
		return true
	}

	// 3. 500 Internal Error (Occasional Google Gemini crashes)
	if strings.Contains(errMsg, "500") || strings.Contains(strings.ToLower(errMsg), "internal error") {
		return true
	}

	return false
}
