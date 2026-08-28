package launcher

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
)

func OpenURL(value string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", value)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", value)
	default:
		command = exec.Command("xdg-open", value)
	}
	return start(command, "open browser")
}

// OpenPath asks the operating system to open a file with its default
// application, or a directory with the platform file manager.
func OpenPath(value string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", value)
	case "windows":
		command = exec.Command("explorer.exe", value)
	default:
		command = exec.Command("xdg-open", value)
	}
	return start(command, "open path")
}

// RevealPath shows a file in the platform file manager. Linux desktop
// environments do not share a portable select-file protocol, so the parent
// directory is opened there. Directories are opened directly on every OS.
func RevealPath(value string, directory bool) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		if directory {
			command = exec.Command("open", value)
		} else {
			command = exec.Command("open", "-R", value)
		}
	case "windows":
		if directory {
			command = exec.Command("explorer.exe", value)
		} else {
			command = exec.Command("explorer.exe", "/select,", value)
		}
	default:
		target := value
		if !directory {
			target = filepath.Dir(value)
		}
		command = exec.Command("xdg-open", target)
	}
	return start(command, "reveal path")
}

func start(command *exec.Cmd, operation string) error {
	if err := command.Start(); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if command.Process != nil {
		_ = command.Process.Release()
	}
	return nil
}
