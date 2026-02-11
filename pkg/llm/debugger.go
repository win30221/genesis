package llm

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// StreamDebugger handles the creation and writing of debug logs for LLM streams.
// It centralizes the logic for directory creation, file naming, and safe writing.
type StreamDebugger struct {
	file     *os.File
	debugDir string
	filename string
	enabled  bool
}

// NewStreamDebugger creates a new debugger instance.
// It prepares the path information but does NOT open the file yet (lazy init).
func NewStreamDebugger(ctx context.Context, provider string, enabled bool) *StreamDebugger {
	if !enabled {
		return &StreamDebugger{enabled: false}
	}

	// Base debug dir
	debugDir := filepath.Join("debug", "chunks", provider)

	// If session ID is in context, nest under it
	if val := ctx.Value(DebugDirContextKey); val != nil {
		if dirStr, ok := val.(string); ok && dirStr != "" {
			debugDir = filepath.Join("debug", "chunks", dirStr, provider)
		}
	}

	timestamp := time.Now().Format("20060102_150405")
	filename := filepath.Join(debugDir, fmt.Sprintf("%s.log", timestamp))

	return &StreamDebugger{
		debugDir: debugDir,
		filename: filename,
		enabled:  true,
	}
}

// ensureFileOpened performs the actual directory and file creation if not already done.
func (d *StreamDebugger) ensureFileOpened() error {
	if !d.enabled || d.file != nil {
		return nil
	}

	if err := os.MkdirAll(d.debugDir, 0755); err != nil {
		slog.Error("Failed to create debug directory", "dir", d.debugDir, "error", err)
		d.enabled = false
		return err
	}

	f, err := os.OpenFile(d.filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		slog.Error("Failed to open debug file", "file", d.filename, "error", err)
		d.enabled = false
		return err
	}

	d.file = f
	slog.Debug("Lazy debug log opened", "file", d.filename)
	return nil
}

// Write appends raw data to the debug file if enabled.
// It includes a newline after the data.
func (d *StreamDebugger) Write(data []byte) {
	if !d.enabled {
		return
	}
	if err := d.ensureFileOpened(); err != nil || d.file == nil {
		return
	}
	if _, err := d.file.Write(data); err != nil {
		slog.Warn("Failed to write to debug file", "error", err)
	}
	d.file.WriteString("\n")
}

// WriteString appends a string to the debug file if enabled.
func (d *StreamDebugger) WriteString(s string) {
	if !d.enabled {
		return
	}
	if err := d.ensureFileOpened(); err != nil || d.file == nil {
		return
	}
	if _, err := d.file.WriteString(s); err != nil {
		slog.Warn("Failed to write to debug file", "error", err)
	}
	d.file.WriteString("\n")
}

// Close closes the debug file handle.
func (d *StreamDebugger) Close() {
	if d.file != nil {
		d.file.Close()
		d.file = nil
	}
}
