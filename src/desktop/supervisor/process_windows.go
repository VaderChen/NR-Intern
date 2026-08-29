//go:build windows

package supervisor

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// configureChildProcess 確保 GUI 桌面程式啟動內部後端時，不會額外建立或
// 閃現 Command Prompt。獨立啟動 nr-intern-server.exe 時仍保留 console。
func configureChildProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
