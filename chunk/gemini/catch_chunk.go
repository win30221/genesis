package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"google.golang.org/genai"
)

func main() {
	ctx := context.Background()
	apiKey := ""
	model := "gemini-2.5-pro"
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		slog.Error("Fatal error", "error", err)
		return
	}

	// 建立 debug 資料夾
	dir := "chunks_" + model
	_ = os.Mkdir(dir, 0755)

	config := &genai.GenerateContentConfig{
		ThinkingConfig: &genai.ThinkingConfig{
			IncludeThoughts: true,
		},
	}

	prompt := "簡單解釋什麼是 Go Channel？簡短回答即可。"
	// prompt := "簡單解釋這張圖片。"
	// imagePath := "D:\\Dowload\\示意圖1.jpg" // ← 改成你的圖片路徑
	// imageBytes, err := os.ReadFile(imagePath)
	// if err != nil {
	// 	log.Fatalf("讀取圖片失敗: %v", err)
	// }

	// mimeType := http.DetectContentType(imageBytes)

	contents := []*genai.Content{
		genai.NewContentFromText(prompt, genai.RoleUser),
		// genai.NewContentFromBytes(imageBytes, mimeType, genai.RoleUser),
	}

	// 使用你記憶中的模型名稱
	stream := client.Models.GenerateContentStream(ctx, model, contents, config)

	fmt.Println("=== 開始串流並存檔 ===")
	chunkCount := 0
	for chunk, err := range stream {
		if err != nil {
			slog.Error("Stream error", "error", err)
			return
		}

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

		// 同步在控制台印出文字，讓你知道進度
		if len(chunk.Candidates) > 0 {
			for _, part := range chunk.Candidates[0].Content.Parts {
				fmt.Print(part.Text)
			}
		}
	}
	fmt.Printf("\n=== 完成！共收到 %d 個封包 ===\n", chunkCount)
}
