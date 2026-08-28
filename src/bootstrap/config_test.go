package bootstrap

import "testing"

func TestDefaultConfigAllowsSandboxedWriteTools(t *testing.T) {
	config := DefaultConfig()
	if config.ServiceName != "聰明的實習生" {
		t.Fatalf("default service name = %q", config.ServiceName)
	}
	if !config.AllowElevatedTools {
		t.Fatal("default config must expose sandboxed write and shell tools")
	}
	policy := config.Permissions.Normalize()
	if !policy.IsElevated(policy.DefaultProfile) {
		t.Fatalf("default profile %q must be elevated when local write tools are enabled", policy.DefaultProfile)
	}
}

func TestPlanningToolsAreAvailableForDefaultAndLegacyAllowlists(t *testing.T) {
	for _, values := range [][]string{DefaultConfig().AllowedTools, ensurePlanningTools([]string{"shell_exec"})} {
		for _, name := range []string{"plan_get", "plan_create", "plan_step_update"} {
			if !containsString(values, name) {
				t.Fatalf("planning tool %q is missing from %v", name, values)
			}
		}
	}
	if values := ensurePlanningTools(nil); values != nil {
		t.Fatalf("empty allowlist must remain unrestricted, got %v", values)
	}
}
