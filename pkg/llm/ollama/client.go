package ollama

import (
	"context"
	"fmt"
	"genesis/pkg/llm"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/ollama/ollama/api"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

// OllamaClient Ollama API 客戶端
type OllamaClient struct {
	client  *api.Client
	model   string
	options map[string]any
}

// NewOllamaClient 創建 Ollama 客戶端
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
		Transport: transport,
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
		// But api.ClientFromEnvironment creates its own client.
		// If we want to enforce our client, we should try to construct it manually if env vars are simple,
		// or just use the default fallback if baseURL is empty.
		// However, most users set baseURL in config.
		client, err = api.ClientFromEnvironment()
	}

	if err != nil {
		return nil, err
	}

	log.Printf("✅ [Ollama] Initialized client for %s (BaseURL: %s)", model, baseURL)
	log.Printf("%+v\n", options)

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
	// 轉換訊息
	apiMessages := o.convertMessages(messages)

	// log.Printf("[Ollama] 🌊 Tapping model: %s...", o.model)

	chunkCh := make(chan llm.StreamChunk, 100)
	startResultCh := make(chan error) // Unbuffered to detect if reader is present

	go func() {
		defer close(chunkCh)

		// 轉換工具 (使用 JSON 轉換以避開 SDK 類型不相容問題)
		var ollamaTools []api.Tool
		if availableTools != nil {
			log.Printf("[Ollama] 🛠️ Converting tools of type: %T", availableTools)
			rawB, err := json.Marshal(availableTools)
			if err != nil {
				log.Printf("[Ollama] ❌ Failed to marshal tools: %v", err)
			} else {
				if err := json.Unmarshal(rawB, &ollamaTools); err != nil {
					log.Printf("[Ollama] ❌ Failed to unmarshal to api.Tool: %v", err)
				}
			}
		}

		log.Printf("[Ollama] 🏗️ Tools available: %d", len(ollamaTools))

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

		err := o.client.Chat(ctx, req, func(resp api.ChatResponse) error {
			// 第一個 callback 表示成功
			if !started {
				started = true
				// 嘗試通知初始化，如果沒人聽(已Timeout)則略過
				select {
				case startResultCh <- nil:
				default:
				}
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

			// 處理工具調用
			if len(resp.Message.ToolCalls) > 0 {
				// log.Printf("[Ollama] 🛠️ DEBUG: Raw ToolCalls: %+v", resp.Message.ToolCalls)
				// log.Printf("[Ollama] 🛠️ Received ToolCalls: %d", len(resp.Message.ToolCalls))
				var toolCalls []llm.ToolCall
				for _, tc := range resp.Message.ToolCalls {
					argsB, _ := json.Marshal(tc.Function.Arguments)
					toolCalls = append(toolCalls, llm.ToolCall{
						ID:   tc.ID, // 改為抓取 ID
						Name: tc.Function.Name,
						Function: llm.FunctionCall{
							Name:      tc.Function.Name,
							Arguments: string(argsB),
						},
					})
					log.Printf("[Ollama] 🛠️ Tool Call: %s(%s) id: %s", tc.Function.Name, string(argsB), tc.ID)
				}
				chunkCh <- llm.StreamChunk{
					ToolCalls: toolCalls,
				}
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
				// 嘗試通知初始化等待者
				select {
				case startResultCh <- err:
					// 成功發送給等待者
				default:
					// 等待者已超時放棄，改發送錯誤訊息給使用者
					chunkCh <- llm.NewTextChunk(fmt.Sprintf("\n❌ Error loading model %s: %v", o.model, err))
				}
			}
		} else if !started {
			select {
			case startResultCh <- nil:
			default:
			}
		}
	}()

	// 等待初始化結果
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

		// 處理工具調用（如果是 Assistant 角色且有 ToolCalls）
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var ollamaToolCalls []api.ToolCall
			for _, tc := range m.ToolCalls {
				// 將 JSON 字串轉回 map
				var args map[string]any
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					log.Printf("[Ollama] ⚠️ Failed to unmarshal tool arguments for history: %v", err)
				}

				// 手動建立 api.ToolCall 以確保 Arguments 被正確處理
				// api.ToolCallFunctionArguments 支持從 map 反序列化
				argBytes, _ := json.Marshal(args)
				var apiArgs api.ToolCallFunctionArguments
				_ = json.Unmarshal(argBytes, &apiArgs)

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

		// 處理工具結果（如果是 Tool 角色）
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
