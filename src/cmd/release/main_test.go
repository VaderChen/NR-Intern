package main

import (
	"strings"
	"testing"
)

func TestBuildLinkerFlagsUsesWindowsGUISubsystemOnlyForDesktop(t *testing.T) {
	windows := target{os: "windows", arch: "amd64"}
	if flags := buildLinkerFlags("./src/cmd/desktop", "1.26.0829 build 1200", windows); !strings.Contains(flags, "-H=windowsgui") {
		t.Fatalf("Windows desktop flags = %q", flags)
	}
	if flags := buildLinkerFlags("./src/cmd/server", "1.26.0829 build 1200", windows); strings.Contains(flags, "-H=windowsgui") {
		t.Fatalf("Windows server unexpectedly uses GUI subsystem: %q", flags)
	}
	if flags := buildLinkerFlags("./src/cmd/desktop", "1.26.0829 build 1200", target{os: "darwin", arch: "arm64"}); strings.Contains(flags, "-H=windowsgui") {
		t.Fatalf("macOS desktop unexpectedly uses Windows GUI subsystem: %q", flags)
	}
}
