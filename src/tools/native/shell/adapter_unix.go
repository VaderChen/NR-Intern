//go:build !windows

package shell

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

type unixAdapter struct{}
type unixProcessGroup struct{ processID int }

func currentPlatformAdapter() platformAdapter { return unixAdapter{} }

func (unixAdapter) DefaultShell() string { return "sh" }

func (unixAdapter) ShellCommand(shellName, script string) (*exec.Cmd, error) {
	switch strings.ToLower(strings.TrimSpace(shellName)) {
	case "", "auto", "sh":
		return exec.Command("/bin/sh", "-c", script), nil
	case "bash":
		return exec.Command("bash", "-c", script), nil
	case "zsh":
		return exec.Command("zsh", "-c", script), nil
	default:
		return nil, fmt.Errorf("shell %q is not supported on this platform", shellName)
	}
}

func (unixAdapter) Prepare(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func (unixAdapter) Attach(command *exec.Cmd) processGroup {
	return &unixProcessGroup{processID: command.Process.Pid}
}

func (g *unixProcessGroup) Terminate() error {
	err := syscall.Kill(-g.processID, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (g *unixProcessGroup) Close() error { return g.Terminate() }
func (g *unixProcessGroup) Name() string { return "process_group" }
