package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ollama/ollama/api"
)

func main() {
	ctx := context.Background()

	// 初始化 Ollama 客戶端
	client, err := api.ClientFromEnvironment()
	if err != nil {
		slog.Error("Fatal error", "error", err)
		return
	}

	model := "qwen3:30b" // 你想用的 Ollama 模型
	safeModel := strings.ReplaceAll(model, ":", "_")
	dir := "chunks_" + safeModel
	_ = os.Mkdir(dir, 0755)

	// === 讀取圖片 ===
	// imagePath := "D:\\Dowload\\unnamed.webp" // ← 改成你的圖片路徑
	// imageBytes, err := os.ReadFile(imagePath)
	// if err != nil {
	// 	log.Fatalf("讀取圖片失敗: %v", err)
	// }

	// Prompt
	prompt := "簡單解釋什麼是 Go Channel？簡短回答即可。"
	// prompt := "簡單解釋這張圖片。"
	messages := []api.Message{
		{
			Role:    "user",
			Content: prompt,
			// Images:  []api.ImageData{imageBytes},
		},
	}

	// 串流參數（可依需求修改）
	options := map[string]any{
		"temperature": 0.0,
		"num_predict": 10000,
	}

	fmt.Println("=== 開始串流並存檔 ===")
	chunkCount := 0

	// 建立 ChatRequest
	req := &api.ChatRequest{
		Model:    model,
		Messages: messages,
		Options:  options,
	}

	// 呼叫 Ollama 串流
	err = client.Chat(ctx, req, func(chunk api.ChatResponse) error {
		chunkCount++
		fileName := filepath.Join(dir, fmt.Sprintf("chunk_%03d.json", chunkCount))

		// 序列化為 JSON
		jsonData, _ := json.MarshalIndent(chunk, "", "  ")

		// 寫入檔案
		err = os.WriteFile(fileName, jsonData, 0644)
		if err != nil {
			slog.Error("Failed to write file", "error", err)
		} else {
			fmt.Printf("已存入: %s\n", fileName)
		}

		// 同步印出文字
		if chunk.Message.Content != "" {
			fmt.Print(chunk.Message.Content)
		}

		return nil
	})

	if err != nil {
		slog.Error("Chat error", "error", err)
		return
	}

	fmt.Printf("\n=== 完成！共收到 %d 個封包 ===\n", chunkCount)
}
