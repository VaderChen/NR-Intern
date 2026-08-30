package ssh

import (
	"testing"
	"time"
)

func TestRemoteCheckMatchesExpectedOutputAndExitCode(t *testing.T) {
	result := remoteCommandResult{stdout: "  42\n", exitCode: 0}
	if !remoteCheckMatches(result, 0, "42", "") {
		t.Fatal("matching output was rejected")
	}
	if remoteCheckMatches(result, 0, "43", "") {
		t.Fatal("non-matching output was accepted")
	}
	if !remoteCheckMatches(remoteCommandResult{stdout: "ready", exitCode: 7, exitError: true}, 7, "", "ready") {
		t.Fatal("expected non-zero exit code was rejected")
	}
}

func TestSSHWaitDefinitionRequiresReadOnlyCheck(t *testing.T) {
	definition := NewWaitTool(nil, 64*1024, time.Minute).Definition()
	if definition.Name != "ssh_wait" || !definition.ReadOnly || !definition.RequiresPermission {
		t.Fatalf("definition = %+v", definition)
	}
	if definition.InputSchema["required"].([]string)[1] != "command" {
		t.Fatalf("required fields = %v", definition.InputSchema["required"])
	}
}
