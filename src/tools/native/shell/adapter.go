package shell

import (
	"os/exec"
)

type processGroup interface {
	Terminate() error
	Close() error
	Name() string
}

type platformAdapter interface {
	ShellCommand(string, string) (*exec.Cmd, error)
	Prepare(*exec.Cmd)
	Attach(*exec.Cmd) processGroup
	DefaultShell() string
}
