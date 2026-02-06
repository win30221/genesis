package tools

import (
	"fmt"
	"runtime"
)

// OSTool 是一個包裝了 Controller 的 genesis 工具
type OSTool struct {
	controller Controller
}

// NewOSTool 建立一個新的 OS 操控工具
func NewOSTool(c Controller) *OSTool {
	return &OSTool{
		controller: c,
	}
}

func (t *OSTool) Name() string {
	return "os_control"
}

func (t *OSTool) Description() string {
	return fmt.Sprintf("操控作業系統 (目前環境: %s)，包含執行指令、截圖、模擬滑鼠鍵盤等。支援動作包含: %s", runtime.GOOS, fmt.Sprint(t.controller.Capabilities()))
}

func (t *OSTool) Parameters() map[string]any {
	return map[string]any{
		"action": map[string]any{
			"type":        "string",
			"description": "要執行的動作名稱。支援動作: 'run_command' (執行系統 Shell 指令), 'screenshot' (擷取螢幕截圖)",
		},
		"params": map[string]any{
			"type":        "object",
			"description": "動作所需的參數。注意：必須包含在 'params' 物件內。例如 {'command': 'dir'} 或 {'x': 100, 'y': 200}",
		},
	}
}

func (t *OSTool) RequiredParameters() []string {
	return []string{"action"}
}

func (t *OSTool) Execute(args map[string]any) (*ToolResult, error) {
	action, ok := args["action"].(string)
	if !ok {
		return nil, fmt.Errorf("missing string parameter 'action'")
	}

	params, _ := args["params"].(map[string]any)
	if params == nil {
		params = make(map[string]any)
	}

	resp, err := t.controller.Execute(ActionRequest{
		Action: action,
		Params: params,
	})
	if err != nil {
		return nil, err
	}

	if !resp.Success {
		return &ToolResult{
			Content: []ContentBlock{
				{Type: "text", Text: "Error executing action: " + resp.Error},
			},
		}, nil
	}

	// 根據動作類型包裝結果
	var blocks []ContentBlock
	switch action {
	case "screenshot":
		if b64, ok := resp.Data.(string); ok {
			blocks = append(blocks, ContentBlock{
				Type: "image",
				Data: b64,
			})
		} else {
			blocks = append(blocks, ContentBlock{Type: "text", Text: "Failed to get screenshot data"})
		}
	default:
		blocks = append(blocks, ContentBlock{
			Type: "text",
			Text: fmt.Sprintf("%v", resp.Data),
		})
	}

	return &ToolResult{
		Content: blocks,
		Details: map[string]any{
			"action": action,
		},
	}, nil
}
