package ollama

import (
	"context"
	"fmt"
	"genesis/pkg/llm"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"os"
	"path/filepath"

	"io"
	"regexp"

	jsoniter "github.com/json-iterator/go"
	"github.com/ollama/ollama/api"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

// OllamaClient Ollama API client
type OllamaClient struct {
	client       *api.Client
	model        string
	options      map[string]any
	debugEnabled bool
}

// SetDebug implements the llm.LLMClient interface
func (o *OllamaClient) SetDebug(enabled bool) {
	o.debugEnabled = enabled
}

// NewOllamaClient creates an Ollama client
func NewOllamaClient(model string, baseURL string, options map[string]any) (*OllamaClient, error) {
	var client *api.Client
	var err error

	// Custom Transport to ensure no timeouts are imposed by the client
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 0, // Explicitly no timeout
	}

	customClient := &http.Client{
		Transport: &JSONFixingRoundTripper{Proxied: transport},
		Timeout:   0, // Explicitly no timeout
	}

	if baseURL != "" {
		u, err := url.Parse(baseURL)
		if err != nil {
			return nil, fmt.Errorf("invalid base URL: %w", err)
		}
		client = api.NewClient(u, customClient)
	} else {
		// Even for environment-based, we prefer our custom client if possible
		client, err = api.ClientFromEnvironment()
	}

	if err != nil {
		return nil, err
	}

	slog.Info("Ollama client initialized", "model", model, "base_url", baseURL)

	return &OllamaClient{
		client:  client,
		model:   model,
		options: options,
	}, nil
}

func (o *OllamaClient) Provider() string {
	return "ollama"
}

func (o *OllamaClient) StreamChat(ctx context.Context, messages []llm.Message, availableTools any) (<-chan llm.StreamChunk, error) {
	// Convert messages
	apiMessages := o.convertMessages(messages)

	chunkCh := make(chan llm.StreamChunk, 100)
	startResultCh := make(chan error) // Unbuffered to detect if reader is present

	go func() {
		defer close(chunkCh)

		// Convert tools (using JSON conversion to work around SDK type mismatch issues)
		var ollamaTools []api.Tool
		if availableTools != nil {
			slog.Debug("Converting tools", "provider", "ollama", "type", fmt.Sprintf("%T", availableTools))
			rawB, err := json.Marshal(availableTools)
			if err != nil {
				slog.Error("Failed to marshal tools", "provider", "ollama", "error", err)
			} else {
				if err := json.Unmarshal(rawB, &ollamaTools); err != nil {
					slog.Error("Failed to unmarshal to api.Tool", "provider", "ollama", "error", err)
				}
			}
		}

		slog.Debug("Tools available", "provider", "ollama", "count", len(ollamaTools))

		streamVal := true
		req := &api.ChatRequest{
			Model:    o.model,
			Messages: apiMessages,
			Options:  o.options,
			Tools:    ollamaTools,
			Stream:   &streamVal,
		}

		started := false
		var thoughtsCount int

		// If debug mode is enabled, open file once for the entire stream
		var debugFile *os.File
		if o.debugEnabled {
			debugID, _ := ctx.Value(llm.DebugDirContextKey).(string)
			if debugID == "" {
				debugID = time.Now().Format("20060102_150405")
			}
			debugDir := filepath.Join("debug", "chunks", "ollama")
			_ = os.MkdirAll(debugDir, 0755)
			debugFilePath := filepath.Join(debugDir, fmt.Sprintf("%s.log", debugID))
			slog.Debug("Debug mode ON", "provider", "ollama", "file", debugFilePath)
			if f, err := os.OpenFile(debugFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
				debugFile = f
				defer debugFile.Close()
			}
		}
		// Track chunks for log preview
		chunkIdx := 0

		err := o.client.Chat(ctx, req, func(resp api.ChatResponse) error {
			chunkIdx++
			// Save raw packet
			if debugFile != nil {
				jsonData, _ := json.Marshal(resp)
				debugFile.Write(jsonData)
				debugFile.WriteString("\n")
			}
			// First callback indicates success
			if !started {
				started = true
				// Notify initialization, skip if no listener (Timeout)
				select {
				case startResultCh <- nil:
				default:
				}
			}

			// Handle reasoning content
			if resp.Message.Thinking != "" {
				thoughtsCount++
				chunkCh <- llm.NewThinkingChunk(resp.Message.Thinking)
			}

			// Handle response content
			if resp.Message.Content != "" {
				chunkCh <- llm.NewTextChunk(resp.Message.Content)
			}

			// Handle tool calls
			if len(resp.Message.ToolCalls) > 0 {
				var toolCalls []llm.ToolCall
				for _, tc := range resp.Message.ToolCalls {
					argsB, err := json.Marshal(tc.Function.Arguments)
					if err != nil {
						slog.Warn("Failed to marshal tool call arguments", "provider", "ollama", "error", err)
						argsB = []byte("{}")
					}
					toolCalls = append(toolCalls, llm.ToolCall{
						ID:   tc.ID,
						Name: tc.Function.Name,
						Function: llm.FunctionCall{
							Name:      tc.Function.Name,
							Arguments: string(argsB),
						},
					})
					slog.Debug("Tool call", "provider", "ollama", "name", tc.Function.Name, "args", string(argsB), "id", tc.ID)
				}
				chunkCh <- llm.StreamChunk{
					ToolCalls: toolCalls,
				}
			}

			// Final chunk
			if resp.Done {
				usage := &llm.LLMUsage{
					PromptTokens:     resp.PromptEvalCount,
					CompletionTokens: resp.EvalCount,
					TotalTokens:      resp.PromptEvalCount + resp.EvalCount,
					ThoughtsTokens:   thoughtsCount,
					StopReason:       resp.DoneReason,
				}

				// Truncation is only logged; Handler manages continuation
				if resp.DoneReason == llm.StopReasonLength {
					slog.Warn("Response truncated due to length", "provider", "ollama")
				}

				chunkCh <- llm.NewFinalChunk(resp.DoneReason, usage)
				llm.LogUsage(o.model, usage)
			}

			return nil
		})

		if err != nil {
			slog.Error("Stream error", "provider", "ollama", "model", o.model, "chunks", chunkIdx, "error", err)
			if !started {
				// Notify initialization waiter
				select {
				case startResultCh <- err:
				default:
					// Waiter timed out, send error message to user instead
					chunkCh <- llm.NewErrorChunk(fmt.Sprintf("Error loading model %s: %v", o.model, err), err, true)
				}
			} else {
				// Stream started but interrupted, notify user
				chunkCh <- llm.NewErrorChunk(fmt.Sprintf("Stream interrupted: %v", err), err, true)
			}
		} else if !started {
			select {
			case startResultCh <- nil:
			default:
			}
		}
	}()

	// Wait for initialization result
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

// convertMessages converts messages to Ollama API format
func (o *OllamaClient) convertMessages(messages []llm.Message) []api.Message {
	var ollamaMsgs []api.Message

	for _, m := range messages {
		var textContent strings.Builder
		var thinkingContent strings.Builder
		var images []api.ImageData

		for _, block := range m.Content {
			switch block.Type {
			case llm.BlockTypeText:
				textContent.WriteString(block.Text)
			case llm.BlockTypeThinking:
				thinkingContent.WriteString(block.Text)
			case llm.BlockTypeImage:
				if block.Source != nil && len(block.Source.Data) > 0 {
					images = append(images, block.Source.Data)
				}
			}
		}

		// Combine content: add separator if both thinking and text exist
		thinking := thinkingContent.String()
		text := textContent.String()
		var combined string
		if thinking != "" && text != "" {
			combined = thinking + "\n" + text
		} else {
			combined = thinking + text
		}

		msg := api.Message{
			Role:    m.Role,
			Content: combined,
		}

		// Handle tool calls (if Assistant role and has ToolCalls)
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var ollamaToolCalls []api.ToolCall
			for _, tc := range m.ToolCalls {
				// Convert JSON string back to map
				var args map[string]any
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					slog.Warn("Failed to unmarshal tool arguments for history", "provider", "ollama", "error", err)
				}

				// Manually create api.ToolCall to ensure Arguments are handled correctly
				// api.ToolCallFunctionArguments supports unmarshaling from map
				argBytes, err := json.Marshal(args)
				if err != nil {
					slog.Warn("Failed to marshal tool arguments for history", "provider", "ollama", "error", err)
					argBytes = []byte("{}")
				}
				var apiArgs api.ToolCallFunctionArguments
				if err := json.Unmarshal(argBytes, &apiArgs); err != nil {
					slog.Warn("Failed to unmarshal to api.ToolCallFunctionArguments", "provider", "ollama", "error", err)
				}

				ollamaToolCalls = append(ollamaToolCalls, api.ToolCall{
					ID: tc.ID,
					Function: api.ToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: apiArgs,
					},
				})
			}
			msg.ToolCalls = ollamaToolCalls
		}

		// Handle tool results (if Tool role)
		if m.Role == "tool" {
			msg.Role = "tool"
			msg.ToolCallID = m.ToolCallID
		}

		if len(images) > 0 {
			msg.Images = images
		}

		ollamaMsgs = append(ollamaMsgs, msg)
	}

	return ollamaMsgs
}

// IsTransientError implements the llm.LLMClient interface
func (o *OllamaClient) IsTransientError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()

	// 1. Connection related errors (Connection refused, reset)
	if strings.Contains(errMsg, "connection refused") || strings.Contains(errMsg, "connection reset") {
		return true
	}

	// 2. High load
	if strings.Contains(strings.ToLower(errMsg), "overloaded") {
		return true
	}

	return false
}

//----------------------------------------------------------------
// JSONFixingRoundTripper - Interceptor that fixes illegal JSON escapes
//----------------------------------------------------------------

// JSONFixingRoundTripper intercepts response and fixes illegal escapes (e.g., \$)
type JSONFixingRoundTripper struct {
	Proxied http.RoundTripper
}

func (j *JSONFixingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := j.Proxied.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	// Only filter text-type responses (mainly stream JSON)
	if strings.Contains(resp.Header.Get("Content-Type"), "application/json") ||
		strings.Contains(resp.Header.Get("Content-Type"), "application/x-ndjson") {
		resp.Body = &jsonFixingReadCloser{body: resp.Body}
	}
	return resp, nil
}

type jsonFixingReadCloser struct {
	body io.ReadCloser
}

var illegalEscapeRegex = regexp.MustCompile(`\\([^\/\\bfnrtu"])`)

func (j *jsonFixingReadCloser) Read(p []byte) (n int, err error) {
	n, err = j.body.Read(p)
	if n > 0 {
		// Preprocess illegal escapes in the buffer
		// e.g., convert \$ to $ to avoid JSON parsing failures
		content := string(p[:n])
		fixed := illegalEscapeRegex.ReplaceAllString(content, "$1")
		if len(fixed) < len(content) {
			// If length decreases, adjust reported n and fill remaining space
			// Since we only replace single characters (removing backslash), this is safe at the byte array level
			copy(p, []byte(fixed))
			n = len(fixed)
		}
	}
	return n, err
}

func (j *jsonFixingReadCloser) Close() error {
	return j.body.Close()
}
