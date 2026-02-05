package llm

import (
	"context"
	"fmt" // Import tools for structs
	"log"
	"strings"
	"time"

	jsoniter "github.com/json-iterator/go"
)

// json 用於 package llm 內部的 JSON 處理，統一使用 json-iterator
var json = jsoniter.ConfigCompatibleWithStandardLibrary

// LLMUsage 定義通用的用量統計結構
type LLMUsage struct {
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	ThoughtsTokens   int    `json:"thoughts_tokens,omitempty"`
	CachedTokens     int    `json:"cached_tokens,omitempty"`
	PromptDetail     string `json:"prompt_detail,omitempty"`
	CompletionDetail string `json:"completion_detail,omitempty"`
	StopReason       string `json:"stop_reason,omitempty"`
}

// LLMResponse is deprecated - use StreamChunk instead
// TODO(agent): Re-enable when implementing agent framework
/*
type LLMResponse struct {
	Content    []ContentBlock `json:"content"`
	ToolUses   []ToolUse      `json:"tool_uses,omitempty"`
	Usage      *LLMUsage      `json:"usage,omitempty"`
	StopReason string         `json:"stop_reason"`
}
*/

// LogUsage 印出統一格式的用量統計
func LogUsage(model string, usage *LLMUsage) {
	if usage == nil {
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "\n> ### 📊 完整用量統計 (%s)\n", model)
	fmt.Fprintf(&sb, "> | 統計項目 | Token 數量 | 詳細拆解 |\n")
	fmt.Fprintf(&sb, "> | :--- | :--- | :--- |\n")
	fmt.Fprintf(&sb, "> | **提示 (Prompt)** | %d | %s |\n", usage.PromptTokens, usage.PromptDetail)
	fmt.Fprintf(&sb, "> | **回答 (Response)** | %d | %s |\n", usage.CompletionTokens, usage.CompletionDetail)
	fmt.Fprintf(&sb, "> | **總計 (Total)** | **%d** | - |\n", usage.TotalTokens)
	fmt.Fprintf(&sb, "> | **思考 (Thoughts)** | %d | - |\n", usage.ThoughtsTokens)

	if usage.StopReason != "" {
		fmt.Fprintf(&sb, "> | **停止原因 (Reason)** | %s | - |\n", usage.StopReason)
	}

	if usage.CachedTokens > 0 {
		fmt.Fprintf(&sb, "> | **快取 (Cached)** | %d | - |\n", usage.CachedTokens)
	}

	fmt.Fprint(&sb, "> ---")

	log.Println(sb.String())
}

// LLMClient 通用 LLM 客戶端介面
type LLMClient interface {
	// StreamChat 流式對話，返回 StreamChunk channel
	// messages: 對話歷史（使用 llm.Message 結構）
	// 返回值: StreamChunk channel（增量式內容 + 最終用量統計）
	StreamChat(ctx context.Context, messages []Message) (<-chan StreamChunk, error)

	// IsTransientError 判斷是否為暫時性錯誤 (如 503, Rate Limit)
	IsTransientError(err error) bool
}

// FallbackClient 支援多個 Client 分級嘗試
type FallbackClient struct {
	Clients    []LLMClient
	MaxRetries int
	RetryDelay time.Duration
}

func (f *FallbackClient) StreamChat(ctx context.Context, messages []Message) (<-chan StreamChunk, error) {
	var lastErr error
	for i, client := range f.Clients {
		if i > 0 {
			log.Printf("⚠️ Previous provider failed. Trying fallback provider #%d...", i+1)
		}

		// 使用配置的重試次數，若為 0 則至少執行 1 次
		maxRetries := f.MaxRetries
		if maxRetries <= 0 {
			maxRetries = 1
		}

		for retry := 1; retry <= maxRetries; retry++ {
			if retry > 1 {
				log.Printf("🔄 Retrying provider #%d (attempt %d/%d)...", i, retry, maxRetries)
				// 稍微等待一下再重試
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(retry-1) * f.RetryDelay):
				}
			}

			ch, err := client.StreamChat(ctx, messages)
			if err == nil {
				return ch, nil
			}

			lastErr = err

			// Check if the error is transient using the client's implementation
			if client.IsTransientError(err) && retry < maxRetries {
				log.Printf("❌ Provider #%d failed with transient error: %v. Retrying...", i+1, err)
				continue
			}

			// 非暫時性錯誤，或者已達最大重試次數
			log.Printf("❌ Provider #%d failed: %v", i+1, err)
			break
		}
	}
	return nil, fmt.Errorf("all fallback providers failed. Last error: %v", lastErr)
}

// IsTransientError 實作 LLMClient 介面
// FallbackClient 本身通常不直接拋出暫時性錯誤，而是由內部的 Client 處理重試
// 但為了滿足介面，我們可以檢查最後一個錯誤
func (f *FallbackClient) IsTransientError(err error) bool {
	// FallbackClient 是一個容器，它的錯誤通常意味著所有 Child 都失敗了
	// 因此視為非暫時性 (除非我們想對整個 Fallback Group 進行外部重試)
	return false
}
