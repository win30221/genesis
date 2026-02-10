//go:build windows

package os

import (
	"bytes"
	"fmt"
	"genesis/pkg/tools"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// WindowsWorker implements the tools.Controller interface specifically for
// Windows environments. It maintains stateful session data like the
// current working directory to support sequential shell commands (e.g., 'cd').
type WindowsWorker struct {
	workingDir string // Tracks the persistent location for command execution context
}

func NewOSWorker() tools.Controller {
	cwd, _ := os.Getwd()
	return &WindowsWorker{
		workingDir: cwd,
	}
}

// Capabilities returns a list of OS-native primitives supported on Windows.
func (w *WindowsWorker) Capabilities() []string {
	return []string{
		"run_command", // Execute PowerShell/Shell commands
		"screenshot",  // Capture primary screen area
	}
}

// Execute dispatches the generic ActionRequest to specialized Windows-native
// implementations like PowerShell runners or GDI+ screen capture routines.
func (w *WindowsWorker) Execute(req tools.ActionRequest) (*tools.ActionResponse, error) {
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

// runCommand executes a string-based shell command via PowerShell.
// It manages environment variable expansion (converting %VAR% to $env:VAR)
// and handles UTF-8 encoding synchronization between Go and PowerShell.
//
// Key features:
// - Stateful: Appends a PWD command to track directory changes (e.g., after 'cd').
// - Resilient: Merges Stdout and Stderr for comprehensive logging.
// - Transparent: Strips the internal PWD metadata from the output before returning.
func (w *WindowsWorker) runCommand(cmdStr string) (string, error) {
	// Convert %VAR% to PowerShell format $env:VAR
	re := regexp.MustCompile(`%([^%]+)%`)
	expandedCmd := re.ReplaceAllString(cmdStr, `$env:$1`)

	// Force PowerShell output to UTF8 and execute the core command
	// [Console]::OutputEncoding affects the output stream, $OutputEncoding affects internal byte conversion
	utf8Cmd := "[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; $OutputEncoding = [System.Text.Encoding]::UTF8; " + expandedCmd

	// Default to powershell execution, and return current directory (pwd) to update state
	// Use ; to separate multiple commands
	fullCmd := fmt.Sprintf("%s; $ExecutionContext.SessionState.Path.CurrentLocation.Path", utf8Cmd)

	slog.Info("Executing command", "dir", w.workingDir, "command", fullCmd)

	cmd := exec.Command("powershell", "-Command", fullCmd)
	cmd.Dir = w.workingDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()

	output := out.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) > 0 {
		// Last line should be the new PWD
		newCwd := strings.TrimSpace(lines[len(lines)-1])
		// Verify if path exists and is a directory
		if info, statErr := os.Stat(newCwd); statErr == nil && info.IsDir() {
			w.workingDir = newCwd
			// Remove the PWD info from output to avoid interfering with AI
			output = strings.Join(lines[:len(lines)-1], "\n")

			// If output is empty (e.g., cd command), return the new directory to inform AI
			if strings.TrimSpace(output) == "" {
				output = fmt.Sprintf("Current directory: %s", w.workingDir)
			}
		}
	}

	return output, err
}

// takeScreenshot captures the primary display content using the .NET
// System.Drawing library via a dynamic PowerShell script.
// It saves the image to a temporary file, reads it into memory as a
// base64-encoded string, and performs cleanup.
// This allows cross-process screen capture without external dependencies.
func (w *WindowsWorker) takeScreenshot() (string, error) {
	// Use PowerShell script to capture screen and save to temp file, then read as base64
	tempFile := filepath.Join(os.TempDir(), "genesis_screenshot.png")
	psScript := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$Screen = [System.Windows.Forms.Screen]::PrimaryScreen
$Width = $Screen.Bounds.Width
$Height = $Screen.Bounds.Height
$Left = $Screen.Bounds.Left
$Top = $Screen.Bounds.Top
$Bitmap = New-Object System.Drawing.Bitmap($Width, $Height)
$Graphics = [System.Drawing.Graphics]::FromImage($Bitmap)
$Graphics.CopyFromScreen($Left, $Top, 0, 0, $Bitmap.Size)
$Bitmap.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png)
$Graphics.Dispose()
$Bitmap.Dispose()
`, tempFile)

	_, err := w.runCommand(psScript)
	if err != nil {
		return "", fmt.Errorf("failed to take screenshot via powershell: %w", err)
	}
	defer os.Remove(tempFile)

	data, err := os.ReadFile(tempFile)
	if err != nil {
		return "", fmt.Errorf("failed to read screenshot file: %w", err)
	}

	// Return Base64 encoding, which allows AI assistants (if they support Vision) to parse directly
	return tools.Base64Encode(data), nil
}
