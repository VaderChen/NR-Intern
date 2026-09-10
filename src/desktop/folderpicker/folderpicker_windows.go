//go:build windows

package folderpicker

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
)

const createNoWindow = 0x08000000

// Windows 的原生對話框透過 PowerShell 叫用，與 macOS 走 osascript 是同一個形狀：
// 用系統自己的腳本宿主，不必為了兩個對話框引入 cgo 與 COM 綁定。
//
// 一律加 -NoProfile：使用者的 profile 可能輸出橫幅或改變格式，那會混進我們要
// 解析的路徑裡。WinForms 的對話框必須在 STA 執行緒，所以要 -STA。
func powershellDialog(ctx context.Context, script string) (string, error) {
	command := exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA",
		"-ExecutionPolicy", "Bypass", "-Command", script)
	// 不讓 PowerShell 自己開 console：桌面版沒有 console，那個黑框會閃一下
	// 才輪到對話框出現。對話框本身是 GUI，不受這個旗標影響。
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

const chooseFolderScript = `
Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.FolderBrowserDialog
$dialog.Description = '選擇 Project Sandbox 目錄'
$dialog.ShowNewFolderButton = $true
if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $dialog.SelectedPath }
`

// pick 一次選一個目錄。
//
// WinForms 的 FolderBrowserDialog 不支援多選，而換成 IFileOpenDialog 就要處理
// COM；介面本來就允許重複按鈕逐一加入，先用能穩定運作的那個。
func pick(ctx context.Context) ([]string, error) {
	path, err := powershellDialog(ctx, chooseFolderScript)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, ErrCanceled
	}
	return []string{path}, nil
}
