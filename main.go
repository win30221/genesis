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
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// --- 0. Load Configuration (needed for log level) ---
	cfg, sysCfg, err := config.Load()
	if err != nil {
		// Banner + default logger for fatal startup error
		monitor.PrintBanner()
		monitor.SetupSlog("info")
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// --- 0a. Setup Environment (logger + monitor) ---
	m := monitor.SetupEnvironment(sysCfg.LogLevel)

	slog.Info("==========================================")

	// --- 2. LLM Setup ---
	client, err := llm.NewFromConfig(cfg.LLM, sysCfg)
	if err != nil {
		slog.Error("Failed to init LLM client", "error", err)
		os.Exit(1)
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
		slog.Error("Failed to build gateway", "error", err)
		os.Exit(1)
	}

	// Create context listening for system signals
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Wait for signal
	<-ctx.Done()
	slog.Info("Received shutdown signal. Stopping services...")

	// Perform cleanup
	gw.StopAll()
	slog.Info("Bye!")
}
