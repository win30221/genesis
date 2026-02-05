package gateway

import "genesis/pkg/llm"

// Channel 定義通訊管道的生命週期介面
// 每個通訊方式 (web, telegram, line) 都必須實作此介面
type Channel interface {
	// ID 返回該 Channel 的唯一識別碼 (e.g., "web", "telegram")
	ID() string
	// Start 啟動接收訊息的 Loop。此方法應該是非阻塞的 (但在某些實作如 CLI 可能需要阻塞，需由 Manager 協調)
	// 當收到訊息時，應調用 ChannelContext.OnMessage
	Start(ctx ChannelContext) error
	// Stop 停止接收
	Stop() error
	// Send 主動發送訊息給特定的 Session
	Send(session SessionContext, message string) error
	// Stream 流式發送 ContentBlock
	Stream(session SessionContext, blocks <-chan llm.ContentBlock) error
}

// SignalingChannel 是一個選用的介面，若 Channel 支援控制信號 (如 thinking)，則實作此介面
type SignalingChannel interface {
	Channel
	// SendSignal 發送一個控制信號
	SendSignal(session SessionContext, signal string) error
}

// ChannelContext 提供 Channel 與 Gateway 核心互動的介面
type ChannelContext interface {
	// OnMessage 當 Channel 收到外部訊息時呼叫此方法
	OnMessage(channelID string, msg *UnifiedMessage)
}

// UnifiedMessage 定義標準化的內部訊息格式
type UnifiedMessage struct {
	Session SessionContext   // 來源 Session 資訊
	Content string           // 文字內容
	Files   []FileAttachment // 附加檔案（圖片等）
	Raw     any              // 原始 Payload (選用)
}

// FileAttachment 表示上傳的檔案
type FileAttachment struct {
	Filename string // 檔案名稱
	MimeType string // MIME 類型 (e.g., "image/jpeg", "image/png")
	Data     []byte // 檔案原始資料
}

// SessionContext 定義訊息的來源上下文，用於路由與辨識使用者
type SessionContext struct {
	ChannelID string // 來源 Channel ID
	UserID    string // 該平台上的 User ID
	ChatID    string // 該平台上的 Chat/Group ID (若為私訊則可能同 UserID)
	Username  string // 使用者名稱 Display Name
}
