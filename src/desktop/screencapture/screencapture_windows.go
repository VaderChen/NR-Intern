//go:build windows

package screencapture

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

func capture(_ context.Context) (Result, error) {
	command := exec.Command("explorer.exe", "ms-screenclip:")
	if err := command.Start(); err != nil {
		return Result{}, fmt.Errorf("開啟 Windows 畫面擷取: %w", err)
	}
	go func() { _ = command.Wait() }()
	return Result{Status: StatusLaunched}, nil
}

func copyPNGToClipboard(ctx context.Context, value []byte) error {
	file, err := os.CreateTemp("", "nr-intern-clipboard-*.png")
	if err != nil {
		return fmt.Errorf("建立剪貼簿暫存檔: %w", err)
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.Write(value); err != nil {
		_ = file.Close()
		return fmt.Errorf("寫入剪貼簿暫存檔: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("關閉剪貼簿暫存檔: %w", err)
	}
	script := `$ErrorActionPreference='Stop'; Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; $image=[System.Drawing.Image]::FromFile($args[0]); try { [System.Windows.Forms.Clipboard]::SetImage($image) } finally { $image.Dispose() }`
	if output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-STA", "-Command", script, path).CombinedOutput(); err != nil {
		return fmt.Errorf("更新 Windows 系統剪貼簿: %w: %s", err, string(output))
	}
	return nil
}
