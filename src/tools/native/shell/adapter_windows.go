//go:build windows

package shell

import (
	"errors"
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
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW | windows.CREATE_SUSPENDED,
	}
}

func (windowsAdapter) Attach(command *exec.Cmd) (processGroup, error) {
	group := &windowsProcessGroup{process: command}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return group, err
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
		return group, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return group, err
	}
	err = windows.AssignProcessToJobObject(job, process)
	_ = windows.CloseHandle(process)
	if err != nil {
		_ = windows.CloseHandle(job)
		return group, err
	}
	group.job = job
	// 子程序仍是 suspended，先納入 Job 才允許它執行與建立後代。
	if err := resumeSuspendedProcess(uint32(command.Process.Pid)); err != nil {
		return group, err
	}
	return group, nil
}

func resumeSuspendedProcess(processID uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	var threadID uint32
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID == processID {
			if threadID != 0 {
				return fmt.Errorf("suspended process unexpectedly has multiple threads")
			}
			threadID = entry.ThreadID
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return err
	}
	if threadID == 0 {
		return fmt.Errorf("suspended process thread not found")
	}
	thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, threadID)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(thread)
	previous, err := windows.ResumeThread(thread)
	if err != nil {
		return err
	}
	if previous != 1 {
		return fmt.Errorf("unexpected initial suspend count: %d", previous)
	}
	return nil
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
