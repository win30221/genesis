package tools

// ActionRequest 代表一個操控請求
type ActionRequest struct {
	Action string         `json:"action"` // 動作名稱，例如 "click", "screenshot", "run_command"
	Params map[string]any `json:"params"` // 動作所需的參數
}

// ActionResponse 代表動作執行的結果
type ActionResponse struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Controller 是通用的外掛操控介面
// 支援跨平台操作，採用「動作分發 (Action Dispatching)」模式
type Controller interface {
	// Execute 執行一個指定的動作
	Execute(req ActionRequest) (*ActionResponse, error)

	// Capabilities 返回該控制器支援的所有動作列表
	Capabilities() []string
}
