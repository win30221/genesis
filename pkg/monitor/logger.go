package monitor

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

// LogConfig defines the behavioral parameters for the global system logger,
// including time formatting and output destination.
type LogConfig struct {
	TimeFormat string    // Layout for timestamps (Go time.Format style)
	Prefix     string    // Static string prepended to every log line
	Output     io.Writer // Destination for log bytes (defaults to os.Stderr)
}

// DefaultLogConfig returns default configuration
func DefaultLogConfig() LogConfig {
	return LogConfig{
		TimeFormat: "2006-01-02 15:04:05",
		Prefix:     "",
		Output:     os.Stderr,
	}
}

// customLogger implements io.Writer to intercept and format logs
type customLogger struct {
	config LogConfig
}

func (l *customLogger) Write(p []byte) (n int, err error) {
	timestamp := time.Now().Format(l.config.TimeFormat)
	// Use Fprintf to write formatted logs
	_, err = fmt.Fprintf(l.config.Output, "%s[%s] %s", l.config.Prefix, timestamp, p)
	return len(p), err
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

// SetupSystemLogger configures global system logging format
func SetupSystemLogger(config LogConfig) {
	// Remove default flags (e.g., default timestamp)
	log.SetFlags(0)

	// Set custom writer
	logger := &customLogger{config: config}
	log.SetOutput(logger)
}

// Startup orchestrates the standard system initialization sequence,
// including printing the ASCII banner and setting up the global logger.
func Startup() {
	PrintBanner()
	SetupSystemLogger(DefaultLogConfig())
}
