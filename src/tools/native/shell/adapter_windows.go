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
	// CREATE_NO_WINDOW 與 HideWindow 一起設：桌面版是 GUI subsystem 程式，
	// 沒有自己的 console，每次執行工具都會另外建立一個並且一閃而過。
	// 使用者看到的是畫面上不斷閃黑框，而那些視窗沒有任何用途——輸出是用
	// 管道收的，不靠 console 顯示。
	command.SysProcAttr = &windows.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
	}
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
