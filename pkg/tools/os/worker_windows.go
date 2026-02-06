//go:build windows

package os

import (
	"bytes"
	"fmt"
	"genesis/pkg/tools"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// WindowsWorker 實作了 tools.Controller 介面，專注於 Windows 環境的操控
type WindowsWorker struct {
	workingDir string
}

func NewOSWorker() tools.Controller {
	cwd, _ := os.Getwd()
	return &WindowsWorker{
		workingDir: cwd,
	}
}

func (w *WindowsWorker) Capabilities() []string {
	return []string{
		"run_command",
		"screenshot",
	}
}

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

func (w *WindowsWorker) runCommand(cmdStr string) (string, error) {
	// 將 %VAR% 轉換為 PowerShell 格式 $env:VAR
	re := regexp.MustCompile(`%([^%]+)%`)
	expandedCmd := re.ReplaceAllString(cmdStr, `$env:$1`)

	// 強制 PowerShell 輸出為 UTF8 並執行核心指令
	// [Console]::OutputEncoding 影響輸出串流，$OutputEncoding 影響內部位元組轉換
	utf8Cmd := "[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; $OutputEncoding = [System.Text.Encoding]::UTF8; " + expandedCmd

	// 預設使用 powershell 執行，並在完成後返回目前的目錄 (pwd) 以更新 state
	// 使用 ; 分隔多個指令
	fullCmd := fmt.Sprintf("%s; $ExecutionContext.SessionState.Path.CurrentLocation.Path", utf8Cmd)

	log.Printf("[OS/Worker] 💻 Executing in [%s]: %s", w.workingDir, fullCmd)

	cmd := exec.Command("powershell", "-Command", fullCmd)
	cmd.Dir = w.workingDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()

	output := out.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) > 0 {
		// 最後一行應該是新的 PWD
		newCwd := strings.TrimSpace(lines[len(lines)-1])
		// 驗證路徑是否存在且為目錄
		if info, statErr := os.Stat(newCwd); statErr == nil && info.IsDir() {
			w.workingDir = newCwd
			// 從輸出中移除最後一行的 PWD 資訊，以免干擾 AI
			output = strings.Join(lines[:len(lines)-1], "\n")

			// 如果輸出為空（例如 cd 指令），則回傳新的目錄位置，讓 AI 知道環境變更
			if strings.TrimSpace(output) == "" {
				output = fmt.Sprintf("Current directory: %s", w.workingDir)
			}
		}
	}

	return output, err
}

func (w *WindowsWorker) takeScreenshot() (string, error) {
	// 使用 PowerShell 腳本擷取螢幕並存入臨時檔案，再讀取為 base64
	tempFile := "temp_screenshot.png"
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

	// 返回 Base64 編碼，這能讓 AI 助手（如果支援 Vision）直接解析
	return tools.Base64Encode(data), nil
}
