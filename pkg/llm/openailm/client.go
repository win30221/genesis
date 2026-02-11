package openailm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"genesis/pkg/llm"
	"log/slog"
	"reflect"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
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
	msg := strings.ToLower(err.Error())

	// Transient: network-level issues
	if strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "timeout") {
		return true
	}

	// Transient: server-side temporary failures
	if strings.Contains(msg, "500 internal") ||
		strings.Contains(msg, "502 bad gateway") ||
		strings.Contains(msg, "503 service unavailable") ||
		strings.Contains(msg, "overloaded") {
		return true
	}

	// Everything else (400 Bad Request, 401 Unauthorized, etc.) is non-transient
	return false
}

func (c *Client) StreamChat(ctx context.Context, messages []llm.Message, availableTools any) (<-chan llm.StreamChunk, error) {
	chunkCh := make(chan llm.StreamChunk, 100)

	// Convert messages
	convertedMsgs := c.convertMessages(messages)

	// 調用 API
	params := responses.ResponseNewParams{
		Model: c.model,
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: convertedMsgs,
		},
	}

	opts := []option.RequestOption{}

	// Handle unified "thinking_effort" option
	if effortStr, ok := c.options["thinking_effort"].(string); ok && effortStr != "" && effortStr != "off" {
		var effort shared.ReasoningEffort
		switch effortStr {
		case "low":
			effort = shared.ReasoningEffortLow
		case "medium":
			effort = shared.ReasoningEffortMedium
		case "high":
			effort = shared.ReasoningEffortHigh
		default:
			effort = shared.ReasoningEffortMedium
		}

		params.Reasoning = shared.ReasoningParam{
			Effort: effort,
		}
	}

	// Handle unified "temperature" option (optional)
	if t, ok := c.options["temperature"].(float64); ok {
		opts = append(opts, option.WithJSONSet("temperature", t))
	}

	// Handle unified "top_p" option (optional)
	if p, ok := c.options["top_p"].(float64); ok {
		opts = append(opts, option.WithJSONSet("top_p", p))
	}

	// Handle unified "max_tokens" option (mapped to max_completion_tokens for o1/newer models)
	if maxTok, ok := c.options["max_tokens"].(float64); ok {
		opts = append(opts, option.WithJSONSet("max_completion_tokens", int(maxTok)))
	}

	if tools := c.convertTools(availableTools); len(tools) > 0 {
		params.Tools = tools
	}

	go func() {
		defer close(chunkCh)

		stream := c.client.Responses.NewStreaming(ctx, params, opts...)
		defer stream.Close()

		var lastFinishReason string
		var lastUsage *llm.LLMUsage

		// StreamDebugger handles file creation and lifecycle
		debugger := llm.NewStreamDebugger(ctx, c.provider, c.debugEnabled)
		defer debugger.Close()

		var assistantTextAccumulator strings.Builder
		var thinkingLogBuffer string
		toolCallsMap := make(map[string]*llm.ToolCall)

		for stream.Next() {
			event := stream.Current()

			// Use reflection to get unexported 'raw' string from event.JSON for debug logging and fallback
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
			if raw != "" {
				debugger.WriteString(raw)
			}

			// Fallback thinking capture from raw JSON (DeepSeek/GPT-5 legacy style)
			var rawChoice struct {
				Reasoning        string `json:"reasoning"`
				Thinking         string `json:"thinking"`
				ReasoningContent string `json:"reasoning_content"`
			}
			if raw != "" && json.Unmarshal([]byte(raw), &rawChoice) == nil {
				thought := rawChoice.Reasoning
				if thought == "" {
					thought = rawChoice.Thinking
				}
				if thought == "" {
					thought = rawChoice.ReasoningContent
				}
				if thought != "" {
					thinkingLogBuffer += thought
					chunkCh <- llm.NewThinkingChunk(thought)
				}
			}

			// Handle different event types using SDK native types
			switch variant := event.AsAny().(type) {
			case responses.ResponseTextDeltaEvent:
				chunkCh <- llm.NewTextChunk(variant.Delta)
				assistantTextAccumulator.WriteString(variant.Delta)

			case responses.ResponseReasoningTextDeltaEvent:
				thinkingLogBuffer += variant.Delta
				chunkCh <- llm.NewThinkingChunk(variant.Delta)

			case responses.ResponseReasoningSummaryTextDeltaEvent:
				thinkingLogBuffer += variant.Delta
				chunkCh <- llm.NewThinkingChunk(variant.Delta)

			case responses.ResponseFunctionCallArgumentsDeltaEvent:
				tc, ok := toolCallsMap[variant.ItemID]
				if !ok {
					tc = &llm.ToolCall{
						ID: variant.ItemID,
					}
					toolCallsMap[variant.ItemID] = tc
				}
				tc.Function.Arguments += variant.Delta

			case responses.ResponseFunctionCallArgumentsDoneEvent:
				tc, ok := toolCallsMap[variant.ItemID]
				if ok && variant.Name != "" {
					tc.Name = variant.Name
					tc.Function.Name = variant.Name
				}

			case responses.ResponseOutputItemAddedEvent:
				// If it's a function call, we can initialize it here
				if variant.Item.Type == "function_call" {
					tc, ok := toolCallsMap[variant.Item.ID]
					if !ok {
						tc = &llm.ToolCall{ID: variant.Item.ID}
						toolCallsMap[variant.Item.ID] = tc
					}
					if variant.Item.Name != "" {
						tc.Name = variant.Item.Name
						tc.Function.Name = variant.Item.Name
					}
				}

			case responses.ResponseOutputItemDoneEvent:
				// Ensure name is captured even if late
				if variant.Item.Type == "function_call" {
					tc, ok := toolCallsMap[variant.Item.ID]
					if ok && variant.Item.Name != "" {
						tc.Name = variant.Item.Name
						tc.Function.Name = variant.Item.Name
					}
				}

			case responses.ResponseCompletedEvent:
				lastFinishReason = "stop"
				if variant.Response.Usage.TotalTokens > 0 {
					lastUsage = &llm.LLMUsage{
						PromptTokens:     int(variant.Response.Usage.InputTokens),
						CompletionTokens: int(variant.Response.Usage.OutputTokens),
						TotalTokens:      int(variant.Response.Usage.TotalTokens),
						StopReason:       llm.StopReasonStop,
					}
				}

			case responses.ResponseFailedEvent:
				lastFinishReason = "failed"
				chunkCh <- llm.NewErrorChunk("API Response Failed", nil, true)

			case responses.ResponseIncompleteEvent:
				lastFinishReason = "length"
				chunkCh <- llm.NewErrorChunk("API Response Incomplete", nil, true)

			case responses.ResponseErrorEvent:
				chunkCh <- llm.NewErrorChunk(fmt.Sprintf("API Error: %s", variant.Message), nil, true)
			}
		}
		if strings.TrimSpace(thinkingLogBuffer) != "" {
			slog.Debug("Captured full thinking process", "provider", c.provider, "content", thinkingLogBuffer)
		}

		// If we found tool calls, emit them now
		if len(toolCallsMap) > 0 {
			toolCallsFound := make([]llm.ToolCall, 0, len(toolCallsMap))
			for _, tc := range toolCallsMap {
				toolCallsFound = append(toolCallsFound, *tc)
			}
			chunkCh <- llm.StreamChunk{
				ToolCalls: toolCallsFound,
			}
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

func (c *Client) convertMessages(messages []llm.Message) []responses.ResponseInputItemUnionParam {
	items := make([]responses.ResponseInputItemUnionParam, 0, len(messages))

	for _, m := range messages {
		switch m.Role {
		case "system":
			items = append(items, responses.ResponseInputItemParamOfMessage(
				m.GetTextContent(),
				responses.EasyInputMessageRoleSystem,
			))
		case "user":
			if m.HasImages() {
				var contentParts responses.ResponseInputMessageContentListParam
				for _, block := range m.Content {
					switch block.Type {
					case llm.BlockTypeText:
						contentParts = append(contentParts, responses.ResponseInputContentUnionParam{
							OfInputText: &responses.ResponseInputTextParam{
								Text: block.Text,
							},
						})
					case llm.BlockTypeImage:
						if block.Source != nil {
							imgURL := block.Source.URL
							if block.Source.Type == "base64" {
								imgURL = fmt.Sprintf("data:%s;base64,%s", block.Source.MediaType, base64.StdEncoding.EncodeToString(block.Source.Data))
							}
							contentParts = append(contentParts, responses.ResponseInputContentUnionParam{
								OfInputImage: &responses.ResponseInputImageParam{
									Detail:   responses.ResponseInputImageDetailAuto,
									ImageURL: param.NewOpt(imgURL),
								},
							})
						}
					}
				}
				items = append(items, responses.ResponseInputItemParamOfMessage(
					contentParts,
					responses.EasyInputMessageRoleUser,
				))
			} else {
				items = append(items, responses.ResponseInputItemParamOfMessage(
					m.GetTextContent(),
					responses.EasyInputMessageRoleUser,
				))
			}
		case "assistant":
			// Text content
			if text := m.GetTextContent(); text != "" {
				items = append(items, responses.ResponseInputItemParamOfMessage(
					text,
					responses.EasyInputMessageRoleAssistant,
				))
			}
			// Tool calls
			for _, tc := range m.ToolCalls {
				items = append(items, responses.ResponseInputItemParamOfFunctionCall(
					tc.Function.Arguments,
					tc.ID,
					tc.Name,
				))
			}
		case "tool", "tool_result":
			// Tool result
			items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(
				m.ToolCallID,
				m.GetTextContent(),
			))
		}
	}

	return items
}

func (c *Client) convertTools(availableTools any) []responses.ToolUnionParam {
	if availableTools == nil {
		return nil
	}

	rawTools, ok := availableTools.([]map[string]any)
	if !ok {
		return nil
	}

	var tools []responses.ToolUnionParam
	for _, t := range rawTools {
		if funcMap, ok := t["function"].(map[string]any); ok {
			name, _ := funcMap["name"].(string)
			desc, _ := funcMap["description"].(string)
			params, _ := funcMap["parameters"].(map[string]any)

			tools = append(tools, responses.ToolUnionParam{
				OfFunction: &responses.FunctionToolParam{
					Name:        name,
					Description: openai.String(desc),
					Parameters:  params,
				},
			})
		}
	}
	return tools
}

// findFunctionCalls 在任意 JSON 結構中遞迴查找 type=="function_call" 的物件
func findFunctionCalls(v any, out *[]map[string]any) {
	switch val := v.(type) {
	case map[string]any:
		// check current map
		if t, ok := val["type"].(string); ok && t == "function_call" {
			// copy relevant fields
			entry := map[string]any{}
			if a, ok := val["arguments"].(string); ok {
				entry["arguments"] = a
			}
			if cid, ok := val["call_id"].(string); ok {
				entry["call_id"] = cid
			}
			if id, ok := val["id"].(string); ok {
				entry["id"] = id
			}
			if name, ok := val["name"].(string); ok {
				entry["name"] = name
			}
			*out = append(*out, entry)
		}
		// recurse into map fields
		for _, vv := range val {
			findFunctionCalls(vv, out)
		}
	case []any:
		for _, item := range val {
			findFunctionCalls(item, out)
		}
	}
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
