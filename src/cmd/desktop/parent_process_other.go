//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
	"time"
)

func parentProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EPERM)
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
