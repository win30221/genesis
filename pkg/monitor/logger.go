package monitor

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

// LogConfig 定義日誌配置
type LogConfig struct {
	TimeFormat string    // 時間格式，例如 "2006-01-02 15:04:05"
	Prefix     string    // 日誌前綴，例如 "[Genesis] "
	Output     io.Writer // 輸出目標，預設為 os.Stderr
}

// DefaultLogConfig 返回預設配置
func DefaultLogConfig() LogConfig {
	return LogConfig{
		TimeFormat: "2006-01-02 15:04:05",
		Prefix:     "",
		Output:     os.Stderr,
	}
}

// customLogger 實作 io.Writer 來攔截和格式化日誌
type customLogger struct {
	config LogConfig
}

func (l *customLogger) Write(p []byte) (n int, err error) {
	timestamp := time.Now().Format(l.config.TimeFormat)
	// 使用 Fprintf 寫入格式化的日誌
	_, err = fmt.Fprintf(l.config.Output, "%s[%s] %s", l.config.Prefix, timestamp, p)
	return len(p), err
}

// PrintBanner 印出啟動 Banner
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

// SetupSystemLogger 設定全域系統日誌格式
func SetupSystemLogger(config LogConfig) {
	// 移除 log 套件預設的旗標（例如預設的時間戳）
	log.SetFlags(0)

	// 設定自訂的 writer
	logger := &customLogger{config: config}
	log.SetOutput(logger)
}

// Startup 執行監控系統的完整啟動流程 (印 Banner + 設定 Logger)
func Startup() {
	PrintBanner()
	SetupSystemLogger(DefaultLogConfig())
}
