//go:build linux

package os

import (
	"fmt"
	"genesis/pkg/tools"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// LinuxWorker implements tools.Controller for Linux
type LinuxWorker struct {
	workingDir string
}

func NewOSWorker() tools.Controller {
	cwd, _ := os.Getwd()
	return &LinuxWorker{
		workingDir: cwd,
	}
}

func (w *LinuxWorker) Capabilities() []string {
	return []string{
		"run_command",
		"screenshot",
	}
}

func (w *LinuxWorker) Execute(req tools.ActionRequest) (*tools.ActionResponse, error) {
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

func (w *LinuxWorker) runCommand(cmdStr string) (string, error) {
	slog.Info("Executing command", "dir", w.workingDir, "command", cmdStr)

	// Use bash for Linux
	fullCmd := fmt.Sprintf("cd %q && %s && pwd", w.workingDir, cmdStr)

	cmd := exec.Command("/bin/bash", "-c", fullCmd)
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
			output = strings.Join(lines[:len(lines)-1], "\n")
		}
	}

	return output, nil
}

func (w *LinuxWorker) takeScreenshot() (string, error) {
	tempFile := "/tmp/screenshot.png"
	// Try gnome-screenshot first
	// -f: filename
	cmd := exec.Command("gnome-screenshot", "-f", tempFile)
	if err := cmd.Run(); err != nil {
		// Fallback to scrot
		slog.Warn("gnome-screenshot failed, trying scrot", "error", err)
		cmd = exec.Command("scrot", tempFile)
		if err = cmd.Run(); err != nil {
			return "", fmt.Errorf("screenshot failed (tried gnome-screenshot and scrot): %w", err)
		}
	}
	defer os.Remove(tempFile)

	data, err := os.ReadFile(tempFile)
	if err != nil {
		return "", fmt.Errorf("failed to read screenshot file: %w", err)
	}

	return tools.Base64Encode(data), nil
}
