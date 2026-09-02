//go:build windows

package main

import (
	"time"

	"golang.org/x/sys/windows"
)

const windowsStillActive = 259

func parentProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false
	}
	return exitCode == windowsStillActive
}

func watchParentProcess(done <-chan struct{}, pid int, parentLost chan<- struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if !backendParentAlive(pid) {
				close(parentLost)
				return
			}
		}
	}
}
