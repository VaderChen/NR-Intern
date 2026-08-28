//go:build darwin

package folderpicker

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

const chooseFoldersScript = `
set chosenFolders to choose folder with prompt "選擇 Project Sandbox 目錄" with multiple selections allowed
set pathLines to {}
repeat with chosenFolder in chosenFolders
  set end of pathLines to POSIX path of chosenFolder
end repeat
set AppleScript's text item delimiters to linefeed
return pathLines as text
`

func pick(ctx context.Context) ([]string, error) {
	command := exec.CommandContext(ctx, "osascript", "-e", chooseFoldersScript)
	output, err := command.Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			detail := strings.ToLower(string(exitError.Stderr))
			if strings.Contains(detail, "user canceled") || strings.Contains(detail, "-128") {
				return nil, ErrCanceled
			}
		}
		return nil, err
	}
	values := []string{}
	for _, value := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return nil, ErrCanceled
	}
	return values, nil
}
