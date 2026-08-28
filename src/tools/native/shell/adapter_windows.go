//go:build windows

package shell

import (
	"fmt"
	"os/exec"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsAdapter struct{}
type windowsProcessGroup struct {
	job     windows.Handle
	process *exec.Cmd
}

func currentPlatformAdapter() platformAdapter { return windowsAdapter{} }

func (windowsAdapter) DefaultShell() string { return "cmd" }

func (windowsAdapter) ShellCommand(shellName, script string) (*exec.Cmd, error) {
	switch strings.ToLower(strings.TrimSpace(shellName)) {
	case "", "auto", "cmd":
		return exec.Command("cmd.exe", "/d", "/s", "/c", script), nil
	case "powershell":
		return exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script), nil
	case "pwsh":
		return exec.Command("pwsh.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script), nil
	default:
		return nil, fmt.Errorf("shell %q is not supported on this platform", shellName)
	}
}

func (windowsAdapter) Prepare(command *exec.Cmd) {
	command.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func (windowsAdapter) Attach(command *exec.Cmd) processGroup {
	group := &windowsProcessGroup{process: command}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return group
	}
	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	information.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return group
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return group
	}
	err = windows.AssignProcessToJobObject(job, process)
	_ = windows.CloseHandle(process)
	if err != nil {
		_ = windows.CloseHandle(job)
		return group
	}
	group.job = job
	return group
}

func (g *windowsProcessGroup) Terminate() error {
	if g.job != 0 {
		return windows.TerminateJobObject(g.job, 1)
	}
	if g.process == nil || g.process.Process == nil {
		return nil
	}
	return g.process.Process.Kill()
}

func (g *windowsProcessGroup) Close() error {
	if g.job == 0 {
		return nil
	}
	err := windows.CloseHandle(g.job)
	g.job = 0
	return err
}

func (g *windowsProcessGroup) Name() string {
	if g.job != 0 {
		return "job_object"
	}
	return "single_process"
}
