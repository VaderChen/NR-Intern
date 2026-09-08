//go:build darwin

package folderpicker

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// 檔名以 argv 傳入，不做字串插值。設定包與匯出檔的名稱來自使用者自訂的 id，
// 直接拼進 AppleScript 會讓引號或換行改寫整段腳本。
const chooseSaveNameScript = `
on run argv
  set chosenFile to choose file name with prompt "選擇儲存位置" default name (item 1 of argv)
  return POSIX path of chosenFile
end run
`

func save(ctx context.Context, suggestedName string) (string, error) {
	command := exec.CommandContext(ctx, "osascript", "-e", chooseSaveNameScript, suggestedName)
	output, err := command.Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			detail := strings.ToLower(string(exitError.Stderr))
			if strings.Contains(detail, "user canceled") || strings.Contains(detail, "-128") {
				return "", ErrCanceled
			}
		}
		return "", err
	}
	path := strings.TrimSpace(string(output))
	if path == "" {
		return "", ErrCanceled
	}
	return path, nil
}
