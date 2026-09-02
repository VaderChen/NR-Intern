package main

import (
	"os"
	"testing"
	"time"
)

func TestParentProcessAlive(t *testing.T) {
	if !parentProcessAlive(os.Getpid()) {
		t.Fatal("current process was reported as dead")
	}
	if parentProcessAlive(1<<30 - 1) {
		t.Fatal("nonexistent process was reported as alive")
	}
}

func TestWatchParentProcessDetectsMissingParent(t *testing.T) {
	parentLost := make(chan struct{})
	done := make(chan struct{})
	watchParentProcess(done, 1<<30-1, parentLost)

	select {
	case <-parentLost:
	case <-time.After(2 * time.Second):
		t.Fatal("parent watcher did not detect the missing process")
	}
	close(done)
}
