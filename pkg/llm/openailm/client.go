package openailm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"genesis/pkg/llm"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// Client is a wrapper around the official OpenAI Go SDK
type Client struct {
	client       *openai.Client
	provider     string
	model        string
	debugEnabled bool
	options      map[string]any
}

// NewClient creates a new OpenAI client
func NewClient(provider string, apiKey string, model string, baseURL string, options map[string]any) (*Client, error) {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}

	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	client := openai.NewClient(opts...)

	return &Client{
		client:   &client,
		provider: provider,
		model:    model,
		options:  options,
	}, nil
}

func (c *Client) Provider() string {
	return c.provider
}

func (c *Client) SetDebug(enabled bool) {
	c.debugEnabled = enabled
}

func (c *Client) IsTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "timeout")
}

func (c *Client) StreamChat(ctx context.Context, messages []llm.Message, availableTools any) (<-chan llm.StreamChunk, error) {
	chunkCh := make(chan llm.StreamChunk, 100)

	// Convert messages
	convertedMsgs := c.convertMessages(messages)

	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(c.model),
		Messages: convertedMsgs,
	}

	go func() {
		defer close(chunkCh)

		stream := c.client.Chat.Completions.NewStreaming(ctx, params)

		var lastFinishReason string
		var lastUsage *llm.LLMUsage

		// Initializing debug file if enabled
		var debugFile *os.File
		if c.debugEnabled {
			// Base debug dir
			debugDir := filepath.Join("debug", "chunks", c.provider)

			// If a specific session ID is provided in context, use it as parent
			if val := ctx.Value(llm.DebugDirContextKey); val != nil {
				if dirStr, ok := val.(string); ok {
					debugDir = filepath.Join("debug", "chunks", dirStr, c.provider)
				}
			}
			os.MkdirAll(debugDir, 0755)
			timestamp := time.Now().Format("20060102_150405")
			filename := filepath.Join(debugDir, fmt.Sprintf("%s.log", timestamp))
			f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err == nil {
				debugFile = f
				defer debugFile.Close()
			} else {
				slog.Error("Failed to create debug log", "error", err)
			}
		}

		var thinkingLogBuffer string
		for stream.Next() {
			event := stream.Current()

			// Use reflection to get unexported 'raw' string from event.JSON
			var raw string
			rv := reflect.ValueOf(event.JSON)
			if rv.Kind() == reflect.Struct {
				rt := rv.Type()
				for i := 0; i < rt.NumField(); i++ {
					if rt.Field(i).Name == "raw" {
						raw = rv.Field(i).String()
						break
					}
				}
			}

			// Log raw chunk if debug is enabled
			if debugFile != nil {
				debugFile.WriteString(raw + "\n")
			}

			if len(event.Choices) > 0 {
				choice := event.Choices[0]

				if choice.FinishReason != "" {
					lastFinishReason = string(choice.FinishReason)
				}

				// Capture reasoning content (not explicitly in SDK v3.19.0 yet but in raw JSON)
				var rawChoice struct {
					Reasoning        string `json:"reasoning"`         // Top-level fallback
					Thinking         string `json:"thinking"`          // Top-level fallback
					ReasoningContent string `json:"reasoning_content"` // Top-level fallback (DeepSeek)
					Choices          []struct {
						Delta struct {
							ReasoningContent string `json:"reasoning_content"`
							Reasoning        string `json:"reasoning"`
							Thinking         string `json:"thinking"`
						} `json:"delta"`
					} `json:"choices"`
				}

				if err := json.Unmarshal([]byte(raw), &rawChoice); err == nil {
					thought := rawChoice.Reasoning
					if thought == "" {
						thought = rawChoice.Thinking
					}
					if thought == "" {
						thought = rawChoice.ReasoningContent
					}

					if len(rawChoice.Choices) > 0 {
						delta := rawChoice.Choices[0].Delta
						if thought == "" {
							thought = delta.ReasoningContent
						}
						if thought == "" {
							thought = delta.Reasoning
						}
						if thought == "" {
							thought = delta.Thinking
						}
					}

					if thought != "" {
						thinkingLogBuffer += thought
						chunkCh <- llm.NewThinkingChunk(thought)
					}
				} else {
					slog.Warn("Failed to unmarshal raw JSON for reasoning capture", "error", err)
				}

				if choice.Delta.Content != "" {
					chunkCh <- llm.NewTextChunk(choice.Delta.Content)
				}

				if len(choice.Delta.ToolCalls) > 0 {
					var toolCalls []llm.ToolCall
					for _, tc := range choice.Delta.ToolCalls {
						toolCalls = append(toolCalls, llm.ToolCall{
							ID:   tc.ID,
							Name: tc.Function.Name,
							Function: llm.FunctionCall{
								Name:      tc.Function.Name,
								Arguments: tc.Function.Arguments,
							},
						})
					}
					chunkCh <- llm.StreamChunk{
						ToolCalls: toolCalls,
					}
				}
			}

			if event.Usage.TotalTokens > 0 {
				lastUsage = &llm.LLMUsage{
					PromptTokens:     int(event.Usage.PromptTokens),
					CompletionTokens: int(event.Usage.CompletionTokens),
					TotalTokens:      int(event.Usage.TotalTokens),
				}
			}
		}

		if strings.TrimSpace(thinkingLogBuffer) != "" {
			slog.Debug("Captured full thinking process", "provider", c.provider, "content", thinkingLogBuffer)
		}

		if err := stream.Err(); err != nil {
			chunkCh <- llm.NewErrorChunk(fmt.Sprintf("Stream error: %v", err), err, true)
		} else {
			// Send final chunk with accumulated stats
			reason := "stop"
			if lastFinishReason != "" {
				reason = normalizeStopReason(lastFinishReason)
			}
			chunkCh <- llm.NewFinalChunk(reason, lastUsage)
		}
	}()

	return chunkCh, nil
}

func (c *Client) convertMessages(messages []llm.Message) []openai.ChatCompletionMessageParamUnion {
	var items []openai.ChatCompletionMessageParamUnion

	for _, m := range messages {
		// 1. Tool Outputs (Role = "tool")
		if m.Role == "tool" {
			toolMsg := &openai.ChatCompletionToolMessageParam{
				Role: "tool",
			}
			toolMsg.Content = openai.ChatCompletionToolMessageParamContentUnion{
				OfString: openai.String(m.GetTextContent()),
			}
			toolMsg.ToolCallID = m.ToolCallID
			items = append(items, openai.ChatCompletionMessageParamUnion{
				OfTool: toolMsg,
			})
			continue
		}

		// 2. Assistant Messages
		if m.Role == "assistant" {
			if len(m.ToolCalls) > 0 {
				var toolCalls []openai.ChatCompletionMessageToolCallUnionParam
				for _, tc := range m.ToolCalls {
					toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID:   tc.ID,
							Type: "function",
							Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
								Name:      tc.Name,
								Arguments: tc.Function.Arguments,
							},
						},
					})
				}
				items = append(items, openai.ChatCompletionMessageParamUnion{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Role:      "assistant",
						ToolCalls: toolCalls,
					},
				})
			} else {
				items = append(items, openai.ChatCompletionMessageParamUnion{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Role: "assistant",
						Content: openai.ChatCompletionAssistantMessageParamContentUnion{
							OfString: openai.String(m.GetTextContent()),
						},
					},
				})
			}
			continue
		}

		// 3. User Messages
		if m.Role == "user" {
			if m.HasImages() {
				var parts []openai.ChatCompletionContentPartUnionParam
				for _, block := range m.Content {
					switch block.Type {
					case llm.BlockTypeText:
						parts = append(parts, openai.ChatCompletionContentPartUnionParam{
							OfText: &openai.ChatCompletionContentPartTextParam{
								Type: "text",
								Text: block.Text,
							},
						})
					case llm.BlockTypeImage:
						if block.Source != nil {
							imgURL := block.Source.URL
							if block.Source.Type == "base64" {
								imgURL = fmt.Sprintf("data:%s;base64,%s", block.Source.MediaType, base64.StdEncoding.EncodeToString(block.Source.Data))
							}
							parts = append(parts, openai.ChatCompletionContentPartUnionParam{
								OfImageURL: &openai.ChatCompletionContentPartImageParam{
									Type: "image_url",
									ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
										URL: imgURL,
									},
								},
							})
						}
					}
				}
				items = append(items, openai.ChatCompletionMessageParamUnion{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role: "user",
						Content: openai.ChatCompletionUserMessageParamContentUnion{
							OfArrayOfContentParts: parts,
						},
					},
				})
			} else {
				items = append(items, openai.ChatCompletionMessageParamUnion{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role: "user",
						Content: openai.ChatCompletionUserMessageParamContentUnion{
							OfString: openai.String(m.GetTextContent()),
						},
					},
				})
			}
			continue
		}

		// 4. System Messages
		if m.Role == "system" {
			items = append(items, openai.ChatCompletionMessageParamUnion{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Role: "system",
					Content: openai.ChatCompletionSystemMessageParamContentUnion{
						OfString: openai.String(m.GetTextContent()),
					},
				},
			})
		}
	}

	return items
}

// normalizeStopReason converts OpenAI-specific finish_reason to
// a standardized lowercase format.
func normalizeStopReason(reason string) string {
	switch strings.ToLower(reason) {
	case "stop":
		return llm.StopReasonStop
	case "length":
		return llm.StopReasonLength
	default:
		return reason
	}
}
