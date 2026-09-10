//go:build windows

package folderpicker

import (
	"context"
	"strings"
)

const chooseSaveNameScript = `
Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.SaveFileDialog
$dialog.Title = '選擇儲存位置'
$dialog.FileName = '%NAME%'
$dialog.OverwritePrompt = $true
if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $dialog.FileName }
`

// save 以原生存檔面板詢問位置。
//
// 檔名用單引號字串代入並把內含的單引號跳脫成兩個，這是 PowerShell 逐字字串
// 的唯一跳脫形式；名稱來自使用者自訂的 id，不能直接拼進腳本。
func save(ctx context.Context, suggestedName string) (string, error) {
	escaped := strings.ReplaceAll(suggestedName, "'", "''")
	path, err := powershellDialog(ctx, strings.Replace(chooseSaveNameScript, "%NAME%", escaped, 1))
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", ErrCanceled
	}
	return path, nil
}
