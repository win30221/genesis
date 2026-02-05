package main

import (
	"context"
	"genesis/pkg/channels"
	_ "genesis/pkg/channels/autoload" // 自動註冊 Channels
	"genesis/pkg/config"
	"genesis/pkg/gateway"
	"genesis/pkg/handler"
	"genesis/pkg/llm"
	_ "genesis/pkg/llm/autoload" // 自動註冊 LLM Providers
	"genesis/pkg/monitor"
	"log"
	"os"
	"os/signal"
	"syscall"

	jsoniter "github.com/json-iterator/go"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

func main() {
	// 啟動監控環境
	monitor.Startup()

	log.Println("==========================================")

	// --- 0. 讀取設定檔 ---
	cfg, err := config.Load("config.json")
	if err != nil {
		log.Printf("⚠️ Warning: Failed to load config.json: %v\n", err)
		log.Printf("Using default/empty config.\n")
		cfg = &config.Config{} // Empty config fallback
	}

	// --- 1. LLM 設定 ---
	client, err := llm.NewFromConfig(cfg.LLM, cfg.System)
	if err != nil {
		log.Fatalf("❌ Failed to init LLM client: %v\n", err)
	}

	// --- 1a. 歷史紀錄管理 ---
	chatHistory := llm.NewChatHistory()
	// 預先載入系統提示詞作為歷史起點 (可選，這裡我們先讓 Handler 處理)

	// --- 2. Gateway 初始化（使用 Builder 模式）---
	gw, err := gateway.NewGatewayBuilder().
		WithSystemConfig(cfg.System).
		WithMonitor(monitor.NewCLIMonitor()).
		WithChannelConfigs(cfg.Channels).
		WithChannelLoader(func(g *gateway.GatewayManager, configs map[string]jsoniter.RawMessage) {
			channels.LoadFromConfig(g, configs, chatHistory, cfg.System)
		}).
		WithHandlerFactory(func(gw *gateway.GatewayManager) gateway.MessageHandler {
			return handler.NewMessageHandler(client, gw, cfg, chatHistory)
		}).
		Build()

	if err != nil {
		log.Fatalf("Failed to build gateway: %v\n", err)
	}

	// 阻塞主執行緒
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 監聽系統信號
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 等待信號
	<-sigChan
	log.Println("\nReceived shutdown signal. Stopping services...")

	// 執行清理
	gw.StopAll()
	log.Println("Bye!")
}
