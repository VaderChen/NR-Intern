package shell

import (
	"strings"
	"testing"
)

func TestSafeEnvironmentDoesNotExposeServiceSecrets(t *testing.T) {
	result := safeEnvironment([]string{
		"PATH=/usr/bin",
		"HOME=/tmp/home",
		"OPENAI_API_KEY=secret",
		"AI_AGENT_API_TOKEN=secret",
		"LC_ALL=C",
	})
	joined := strings.Join(result, "\n")
	if strings.Contains(joined, "secret") || strings.Contains(joined, "OPENAI_API_KEY") || strings.Contains(joined, "AI_AGENT_API_TOKEN") {
		t.Fatalf("safe environment leaked a service secret: %s", joined)
	}
	if !strings.Contains(joined, "PATH=/usr/bin") || !strings.Contains(joined, "LC_ALL=C") {
		t.Fatalf("required environment was removed: %s", joined)
	}
}
