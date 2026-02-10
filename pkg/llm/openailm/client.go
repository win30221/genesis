package openailm

import (
	"context"
	"encoding/base64"
	"fmt"
	"genesis/pkg/llm"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// Client is a wrapper around the official OpenAI Go SDK
type Client struct {
	client       *openai.Client
	model        string
	debugEnabled bool
	options      map[string]any
}

// NewClient creates a new OpenAI client
func NewClient(apiKey string, model string, baseURL string, options map[string]any) (*Client, error) {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}

	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	client := openai.NewClient(opts...)

	return &Client{
		client:  &client,
		model:   model,
		options: options,
	}, nil
}

func (c *Client) Provider() string {
	return "openai"
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

		for stream.Next() {
			event := stream.Current()

			if len(event.Choices) > 0 {
				choice := event.Choices[0]

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
				usage := &llm.LLMUsage{
					PromptTokens:     int(event.Usage.PromptTokens),
					CompletionTokens: int(event.Usage.CompletionTokens),
					TotalTokens:      int(event.Usage.TotalTokens),
				}
				chunkCh <- llm.NewFinalChunk("stop", usage)
			}
		}

		if err := stream.Err(); err != nil {
			chunkCh <- llm.NewErrorChunk(fmt.Sprintf("Stream error: %v", err), err, true)
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
