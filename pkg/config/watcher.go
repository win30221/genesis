package config

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// WatchConfig initializes a filesystem watcher for the specified files.
// It returns a channel that emits an empty struct when a change is detected
// and debounced. The watcher runs in a goroutine until the context is canceled.
func WatchConfig(ctx context.Context, files ...string) <-chan struct{} {
	reloadCh := make(chan struct{}, 1) // Buffer 1 so we don't block sender

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Error("Failed to create fsnotify watcher", "error", err)
		return reloadCh
	}

	for _, file := range files {
		absPath, err := filepath.Abs(file)
		if err != nil {
			slog.Warn("Could not resolve absolute path for watch file", "file", file)
			continue
		}
		if err := watcher.Add(absPath); err != nil {
			slog.Warn("Could not watch file", "file", file, "error", err)
		} else {
			slog.Debug("Watching configuration file", "file", file)
		}
	}

	go func() {
		defer watcher.Close()
		defer close(reloadCh)

		// Debounce timer logic
		var timer *time.Timer
		debounceDuration := 500 * time.Millisecond

		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// We only care about file modifications or recreations (like Vim/nano atomic saves)
				if event.Op.Has(fsnotify.Write) || event.Op.Has(fsnotify.Create) {
					// Stop the timer if it's already running
					if timer != nil {
						timer.Stop()
					}
					// Restart the timer
					timer = time.AfterFunc(debounceDuration, func() {
						slog.Info("Configuration change detected", "file", event.Name)
						// Non-blocking send
						select {
						case reloadCh <- struct{}{}:
						default:
						}
					})
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Error("Watcher encountered an error", "error", err)
			}
		}
	}()

	return reloadCh
}
