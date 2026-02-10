//go:build darwin

package os

import (
	"fmt"
	"genesis/pkg/tools"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// DarwinWorker implements tools.Controller for macOS
type DarwinWorker struct {
	workingDir string
}

func NewOSWorker() tools.Controller {
	cwd, _ := os.Getwd()
	return &DarwinWorker{
		workingDir: cwd,
	}
}

func (w *DarwinWorker) Capabilities() []string {
	return []string{
		"run_command",
		"screenshot",
	}
}

func (w *DarwinWorker) Execute(req tools.ActionRequest) (*tools.ActionResponse, error) {
	switch req.Action {
	case "run_command":
		cmdStr, ok := req.Params["command"].(string)
		if !ok {
			return nil, fmt.Errorf("missing string parameter 'command'")
		}
		output, err := w.runCommand(cmdStr)
		if err != nil {
			return &tools.ActionResponse{Success: false, Error: err.Error()}, nil
		}
		return &tools.ActionResponse{Success: true, Data: output}, nil

	case "screenshot":
		data, err := w.takeScreenshot()
		if err != nil {
			return &tools.ActionResponse{Success: false, Error: err.Error()}, nil
		}
		return &tools.ActionResponse{Success: true, Data: data}, nil

	default:
		return nil, fmt.Errorf("unsupported action: %s", req.Action)
	}
}

func (w *DarwinWorker) runCommand(cmdStr string) (string, error) {
	slog.Info("Executing command", "dir", w.workingDir, "command", cmdStr)

	// Use zsh for macOS
	// We want to persist directory changes, but since each command is isolated,
	// we try to chain it with 'pwd' to get the new directory if changed.
	// A robust way is to run: cd <workingDir> && <cmd> && pwd
	fullCmd := fmt.Sprintf("cd %q && %s && pwd", w.workingDir, cmdStr)

	cmd := exec.Command("/bin/zsh", "-c", fullCmd)
	outputBytes, err := cmd.CombinedOutput()
	output := string(outputBytes)

	if err != nil {
		return output, err
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) > 0 {
		possibleCwd := lines[len(lines)-1]
		if info, statErr := os.Stat(possibleCwd); statErr == nil && info.IsDir() {
			w.workingDir = possibleCwd
			// Remove the PWD from output
			output = strings.Join(lines[:len(lines)-1], "\n")
		}
	}

	return output, nil
}

func (w *DarwinWorker) takeScreenshot() (string, error) {
	tempFile := "/tmp/screenshot.png"
	// -x: do not play sound, -t png: format, target file
	cmd := exec.Command("screencapture", "-x", "-t", "png", tempFile)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("screencapture failed: %w", err)
	}
	defer os.Remove(tempFile)

	data, err := os.ReadFile(tempFile)
	if err != nil {
		return "", fmt.Errorf("failed to read screenshot file: %w", err)
	}

	return tools.Base64Encode(data), nil
}
