package monitor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// CustomHandler implements slog.Handler to provide [TIME] [LEVEL] format
type CustomHandler struct {
	w     io.Writer
	opts  slog.HandlerOptions
	attrs []slog.Attr
}

func NewCustomHandler(w io.Writer, opts slog.HandlerOptions) *CustomHandler {
	return &CustomHandler{
		w:    w,
		opts: opts,
	}
}

func (h *CustomHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.opts.Level.Level()
}

func (h *CustomHandler) Handle(ctx context.Context, r slog.Record) error {
	buf := bytes.NewBuffer(nil)

	// Extract DebugID from context if available
	debugID := ""
	if ctx != nil {
		if val := ctx.Value("llm_debug_dir"); val != nil {
			if id, ok := val.(string); ok && id != "" {
				debugID = id
			}
		}
	}

	// Format: [2006-01-02 15:04:05] [LEVEL] [DEBUG_ID] Message
	// Or:    [2006-01-02 15:04:05] [LEVEL] Message (if no debugID)
	fmt.Fprintf(buf, "[%s] [%s]",
		r.Time.Format("2006-01-02 15:04:05"),
		r.Level,
	)

	if debugID != "" {
		fmt.Fprintf(buf, " [%s]", debugID)
	}

	fmt.Fprintf(buf, " %s", r.Message)

	// Append attributes
	// 1. Stored attributes (from WithAttrs)
	for _, a := range h.attrs {
		h.appendAttr(buf, a)
	}

	// 2. Record attributes
	r.Attrs(func(a slog.Attr) bool {
		h.appendAttr(buf, a)
		return true
	})

	buf.WriteString("\n")

	h.w.Write(buf.Bytes())
	return nil
}

func (h *CustomHandler) appendAttr(buf *bytes.Buffer, a slog.Attr) {
	buf.WriteString(" ")
	buf.WriteString(a.Key)
	buf.WriteString("=")

	// Simple value formatting
	val := a.Value.Resolve()
	switch val.Kind() {
	case slog.KindString:
		fmt.Fprintf(buf, "%q", val.String())
	case slog.KindTime:
		buf.WriteString(val.Time().Format(time.RFC3339))
	default:
		fmt.Fprintf(buf, "%v", val.Any())
	}
}

func (h *CustomHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &CustomHandler{
		w:     h.w,
		opts:  h.opts,
		attrs: append(h.attrs, attrs...),
	}
}

func (h *CustomHandler) WithGroup(name string) slog.Handler {
	// Grouping not fully supported in this simple implementation
	return h
}

// SetupSlog initializes the global slog logger with the CustomHandler.
func SetupSlog(levelStr string) {
	var level slog.Level
	switch strings.ToLower(levelStr) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	handler := NewCustomHandler(os.Stderr, slog.HandlerOptions{
		Level: level,
	})

	slog.SetDefault(slog.New(handler))
}

// PrintBanner prints the startup banner
func PrintBanner() {
	banner := `
 ██████╗ ███████╗███╗   ██╗███████╗███████╗██╗███████╗
██╔════╝ ██╔════╝████╗  ██║██╔════╝██╔════╝██║██╔════╝
██║  ███╗█████╗  ██╔██╗ ██║█████╗  ███████╗██║███████╗
██║   ██║██╔══╝  ██║╚██╗██║██╔══╝  ╚════██║██║╚════██║
╚██████╔╝███████╗██║ ╚████║███████╗███████║██║███████║
 ╚═════╝ ╚══════╝╚═╝  ╚═══╝╚══════╝╚══════╝╚═╝╚══════╝
`
	fmt.Println(banner)
}
