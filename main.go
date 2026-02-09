package main

import (
	"context"
	"genesis/pkg/channels"
	_ "genesis/pkg/channels/autoload" // Auto-register Channels
	"genesis/pkg/config"
	"genesis/pkg/gateway"
	"genesis/pkg/handler"
	"genesis/pkg/llm"
	_ "genesis/pkg/llm/autoload" // Auto-register LLM Providers
	"genesis/pkg/monitor"
	"log"
	"os/signal"
	"syscall"
)

func main() {
	// --- 0. Setup Environment ---
	// Initialize logging, banner, and get the monitor instance
	m := monitor.SetupEnvironment()

	log.Println("==========================================")

	// --- 1. Load Configuration ---
	cfg, sysCfg, err := config.Load()
	if err != nil {
		// Fail Fast: mandatory config is missing or invalid
		log.Fatalf("❌ Critical Error: Failed to load configuration: %v\n", err)
	}

	// --- 2. LLM Setup ---
	client, err := llm.NewFromConfig(cfg.LLM, sysCfg)
	if err != nil {
		log.Fatalf("❌ Failed to init LLM client: %v\n", err)
	}

	// --- 2a. Chat History (State) ---
	chatHistory := llm.NewChatHistory()

	// --- 3. Gateway Initialization (using Builder pattern) ---
	gw, err := gateway.NewGatewayBuilder().
		WithSystemConfig(sysCfg).
		WithMonitor(m).
		WithChannelLoader(func(g *gateway.GatewayManager) {
			channels.LoadFromConfig(g, cfg.Channels, chatHistory, sysCfg)
		}).
		WithHandlerFactory(func(gw *gateway.GatewayManager) gateway.MessageHandler {
			return handler.NewMessageHandler(client, gw, cfg, sysCfg, chatHistory)
		}).
		Build()

	if err != nil {
		log.Fatalf("Failed to build gateway: %v\n", err)
	}

	// Create context listening for system signals
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Wait for signal
	<-ctx.Done()
	log.Println("\nReceived shutdown signal. Stopping services...")

	// Perform cleanup
	gw.StopAll()
	log.Println("Bye!")
}
