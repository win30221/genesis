package tools

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
)

// Define constants to avoid Magic Numbers
const (
	ActionScreenshot = "screenshot"
	ActionRunCommand = "run_command"
)

// ---------- Action Spec ----------

// ActionSpec defines the internal configuration and logic for a specific OS action.
// It maps high-level tool calls to low-level controller requests and result formatting.
type ActionSpec struct {
	Name          string                                             // Machine-readable name of the action
	Description   string                                             // Human-readable documentation for LLM ingestion
	ParamSchema   map[string]any                                     // Properties for JSON Schema (tool definition)
	RequireParams bool                                               // Flag to mandate the presence of the "params" object
	Validate      func(params map[string]any) error                  // Logic for validating action-specific parameters
	FormatResult  func(resp *ActionResponse) ([]ContentBlock, error) // Logic to convert controller response to tool blocks
}

// osActionRegistry contains the definitions for all supported OS-level actions.
// New actions (e.g., browse, edit_file) can be added here following the ActionSpec pattern.
var osActionRegistry = map[string]ActionSpec{
	ActionScreenshot: {
		Name:          ActionScreenshot,
		Description:   "Capture a screenshot",
		RequireParams: false,
		ParamSchema:   map[string]any{},
		FormatResult: func(resp *ActionResponse) ([]ContentBlock, error) {
			b64, ok := resp.Data.(string)
			if !ok {
				return nil, fmt.Errorf("unexpected screenshot payload: %T", resp.Data)
			}
			return []ContentBlock{
				{Type: "image", Data: b64},
			}, nil
		},
	},
	ActionRunCommand: {
		Name:          ActionRunCommand,
		Description:   "Execute system shell command",
		RequireParams: true,
		ParamSchema: map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "Command to execute (e.g., 'dir', 'ls -la')",
			},
		},
		Validate: func(params map[string]any) error {
			cmd, ok := params["command"].(string)
			if !ok || cmd == "" {
				return fmt.Errorf("missing or invalid 'command' parameter")
			}
			// TODO: Add command blacklist check here (e.g., rm -rf /)
			return nil
		},
		FormatResult: func(resp *ActionResponse) ([]ContentBlock, error) {
			val := ""
			if resp.Data != nil {
				val = fmt.Sprintf("%v", resp.Data)
			}
			return []ContentBlock{
				{Type: "text", Text: val},
			}, nil
		},
	},
}

// ---------- Tool ----------

// OSTool implements the tools.Tool interface to expose OS-level capabilities
// (shell, screenshots) to the AI Agent. It acts as a bridge between the
// high-level tool registry and a platform-specific low-level Controller.
type OSTool struct {
	controller Controller // Primary engine for dispatching low-level actions
}

// NewOSTool initializes a fresh OSTool instance with a specified controller (worker).
func NewOSTool(c Controller) *OSTool {
	return &OSTool{controller: c}
}

func (t *OSTool) Name() string {
	return "os_control"
}

func (t *OSTool) Description() string {
	// Dynamically generate supported actions list
	var actions []string
	for name, spec := range osActionRegistry {
		actions = append(actions, fmt.Sprintf("'%s' (%s)", name, spec.Description))
	}
	sort.Strings(actions)

	return fmt.Sprintf(
		"Control the operating system (environment: %s). Supported actions: %s",
		runtime.GOOS,
		strings.Join(actions, ", "),
	)
}

func (t *OSTool) Parameters() map[string]any {
	return map[string]any{
		"action": map[string]any{
			"type":        "string",
			"description": "Name of the action to execute",
			"enum":        t.getActionNames(),
		},
		"command": map[string]any{
			"type":        "string",
			"description": "Command to execute (for 'run_command' action, e.g., 'dir', 'ls -la')",
		},
		"params": map[string]any{
			"type":        "object",
			"description": "[Deprecated] Action parameters object (use top-level fields like 'command' instead)",
		},
	}
}

func (t *OSTool) RequiredParameters() []string {
	return []string{"action"}
}

// getActionNames returns a sorted list of supported action names
func (t *OSTool) getActionNames() []string {
	keys := make([]string, 0, len(osActionRegistry))
	for k := range osActionRegistry {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---------- Execute ----------

func (t *OSTool) Execute(args map[string]any) (*ToolResult, error) {
	// 1. Parsing and validation
	spec, params, err := t.parseAndValidateArgs(args)
	if err != nil {
		return nil, err
	}

	// 2. Call Controller
	resp, err := t.controller.Execute(ActionRequest{
		Action: spec.Name,
		Params: params,
	})

	// 3. Handle underlying communication errors (System Error)
	if err != nil {
		return nil, fmt.Errorf("controller execution error: %w", err)
	}

	// 4. Handle business logic failures (Action Failure)
	if !resp.Success {
		return &ToolResult{
			Content: []ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Action '%s' failed: %s", spec.Name, resp.Error)},
			},
			Details: map[string]any{
				"action":  spec.Name,
				"success": false,
				"error":   resp.Error,
			},
		}, nil
	}

	// 5. Format result
	blocks, err := spec.FormatResult(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to format result: %w", err)
	}

	return &ToolResult{
		Content: blocks,
		Details: map[string]any{
			"action":  spec.Name,
			"success": true,
		},
	}, nil
}

// parseAndValidateArgs handles argument parsing logic
func (t *OSTool) parseAndValidateArgs(args map[string]any) (ActionSpec, map[string]any, error) {
	actionName, ok := args["action"].(string)
	if !ok || actionName == "" {
		return ActionSpec{}, nil, fmt.Errorf("missing or invalid parameter 'action'")
	}

	spec, exists := osActionRegistry[actionName]
	if !exists {
		return ActionSpec{}, nil, fmt.Errorf("unsupported action: %s", actionName)
	}

	// Extract params (with backward compatibility)
	// Priority: 1. Top-level arg; 2. Inside "params" object
	params := make(map[string]any)

	// Copy all top-level args except "action" into params
	for k, v := range args {
		if k != "action" && k != "params" {
			params[k] = v
		}
	}

	// Merge from "params" object if exists
	if raw, ok := args["params"]; ok {
		if p, ok := raw.(map[string]any); ok {
			for k, v := range p {
				params[k] = v
			}
		}
	}

	if spec.RequireParams && len(params) == 0 {
		return ActionSpec{}, nil, fmt.Errorf("action '%s' requires parameters (e.g. 'command')", actionName)
	}

	if spec.Validate != nil {
		if err := spec.Validate(params); err != nil {
			return ActionSpec{}, nil, err
		}
	}

	return spec, params, nil
}
