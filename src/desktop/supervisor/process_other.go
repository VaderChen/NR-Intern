//go:build !windows

package supervisor

import "os/exec"

func configureChildProcess(_ *exec.Cmd) {}
