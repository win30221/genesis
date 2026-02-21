package main

import (
	"context"
	"fmt"
	"genesis/pkg/agent"
	"genesis/pkg/api"
	"genesis/pkg/channels"
	_ "genesis/pkg/channels/autoload" // Auto-register Channels
	"genesis/pkg/config"
	"genesis/pkg/gateway"
	"genesis/pkg/handler"
	"genesis/pkg/llm"
	_ "genesis/pkg/llm/autoload" // Auto-register LLM Providers
	"genesis/pkg/monitor"
	"genesis/pkg/tools"
	ostools "genesis/pkg/tools/os" // Aliased to avoid conflict with "os"
	"log/slog"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	// Create context listening for system signals
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Initial configuration load to get log level before loop
	// This acts as a fallback or initial console setup.
	_, sysCfg, err := config.Load()
	if err == nil {
		monitor.SetupEnvironment(sysCfg.LogLevel)
	}

	reloadCh := config.WatchConfig(ctx, "config.json", "system.json")

	for {
		err := runAgent(ctx, reloadCh)

		if err != nil {
			slog.Error("System crashed or failed to load config", "error", err)
			slog.Info("Waiting 5 seconds before retrying...")
			// Wait for 5 seconds, or for a file change, or user interrupt
			select {
			case <-ctx.Done():
				return
			case <-reloadCh:
				slog.Info("Configuration change detected while waiting. Retrying immediately...")
			case <-time.After(5 * time.Second):
			}
		} else {
			// Normal exit from runAgent (either manual exit or config reloaded)
			select {
			case <-ctx.Done():
				return // User requested exit
			default:
				slog.Info("==== Configuration Reloaded ====")
			}
		}
	}
}

// runAgent executes a single lifecycle of the agent
func runAgent(ctx context.Context, reloadCh <-chan struct{}) error {
	// --- 0. Load Configuration ---
	cfg, sysCfg, err := config.Load()
	if err != nil {
		monitor.PrintBanner()
		monitor.SetupSlog("info")
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// --- 0a. Setup Environment (logger + monitor) ---
	m := monitor.SetupEnvironment(sysCfg.LogLevel)
	slog.Info("==========================================")

	// --- 2. Core Services ---
	// --- 2a. Session Management ---
	sessionsDir := filepath.Join("data", "sessions")
	sessionManager := llm.NewSessionManager(sessionsDir)

	// --- 2b. LLM Client ---
	client, err := llm.NewFromConfig(cfg.LLM, sysCfg)
	if err != nil {
		return fmt.Errorf("failed to init LLM client: %w", err)
	}

	// --- 2c. Pre-build Components ---
	chs := channels.NewSource(cfg.Channels, sessionManager, sysCfg).Load()
	tls := []api.Tool{
		tools.NewOSTool(ostools.NewOSWorker()),
	}

	// --- 2d. Tools, Engine & Handler ---
	engine := agent.NewAgentEngine(client, cfg, sysCfg, sessionManager)
	engine.RegisterTool(tls...)
	h := handler.NewChatHandler(engine, sessionManager)

	// --- 3. Gateway Initialization ---
	gw, err := gateway.NewGatewayBuilder().
		WithSystemConfig(sysCfg).
		WithMonitor(m).
		WithChannel(chs...).
		WithAgentEngine(engine).
		WithHandler(h).
		Build()

	if err != nil {
		return fmt.Errorf("failed to build gateway: %w", err)
	}

	// Wait for shutdown signal or reload signal
	select {
	case <-ctx.Done():
		slog.Info("Received shutdown signal. Stopping services...")
		gw.StopAll()
		slog.Info("Bye!")
		return nil
	case <-reloadCh:
		slog.Info("Configuration changes detected, stopping services...")
		gw.StopAll()

		slog.Info("Draining connections before restart...")
		time.Sleep(1 * time.Second)

		// Let runAgent return nil to trigger outer loop restart
		return nil
	}
}
