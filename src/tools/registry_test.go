package tools

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"testing"
)

type stubTool struct {
	definition domain.ToolDefinition
	calls      int
}

func (t *stubTool) Definition() domain.ToolDefinition { return t.definition }

func (t *stubTool) Execute(context.Context, Invocation, ports.ToolUpdateSink) (domain.ToolExecution, error) {
	t.calls++
	return domain.ToolExecution{Content: "ran"}, nil
}

func elevatedTool() *stubTool {
	return &stubTool{definition: domain.ToolDefinition{Name: "shell_exec", Description: "shell", RequiresPermission: true}}
}

func readOnlyTool() *stubTool {
	return &stubTool{definition: domain.ToolDefinition{Name: "file_read", Description: "read", ReadOnly: true}}
}

func sessionWithProfile(profile string) domain.Session {
	return domain.Session{
		ID:                "session_test",
		PermissionProfile: profile,
		Metadata:          map[string]any{"workspace_root": "/tmp/workspace"},
	}
}

func trustedPolicy() domain.PermissionPolicy {
	return domain.PermissionPolicy{DefaultProfile: "default", ElevatedProfiles: []string{"trusted"}}
}

// TestElevatedToolFailsClosedWithoutConfiguredProfiles 覆蓋改動前的 fail-open 行為：
// 未設定 profile 集合時會退回一組寫死的名稱，任何 Session 只要宣告其中之一就取得高權限工具。
func TestElevatedToolFailsClosedWithoutConfiguredProfiles(t *testing.T) {
	tool := elevatedTool()
	registry, err := NewRegistry(RegistryConfig{AllowElevated: true}, tool)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	for _, profile := range []string{"trusted", "auto", "bypass", "danger-full-access"} {
		definitions, err := registry.Definitions(context.Background(), sessionWithProfile(profile))
		if err != nil {
			t.Fatalf("Definitions: %v", err)
		}
		if len(definitions) != 0 {
			t.Errorf("profile %q was offered %d elevated tools; want none", profile, len(definitions))
		}
	}
}

func TestElevatedToolRequiresConfiguredProfile(t *testing.T) {
	tool := elevatedTool()
	registry, err := NewRegistry(RegistryConfig{AllowElevated: true, Permissions: trustedPolicy()}, tool)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	definitions, err := registry.Definitions(context.Background(), sessionWithProfile("default"))
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	if len(definitions) != 0 {
		t.Fatalf("default profile was offered %d elevated tools; want none", len(definitions))
	}

	definitions, err = registry.Definitions(context.Background(), sessionWithProfile("trusted"))
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	if len(definitions) != 1 {
		t.Fatalf("trusted profile got %d tools; want 1", len(definitions))
	}
}

func TestExecuteRefusesElevatedToolForUnprivilegedSession(t *testing.T) {
	tool := elevatedTool()
	registry, err := NewRegistry(RegistryConfig{AllowElevated: true, Permissions: trustedPolicy()}, tool)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	result, err := registry.Execute(context.Background(), sessionWithProfile("default"), domain.ToolCall{ID: "call_1", Name: "shell_exec"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Error("expected an error result for an unprivileged session")
	}
	if tool.calls != 0 {
		t.Errorf("tool ran %d times; it must not run at all", tool.calls)
	}
}

func TestBackendSwitchDisablesElevatedToolsEntirely(t *testing.T) {
	registry, err := NewRegistry(RegistryConfig{AllowElevated: false, Permissions: trustedPolicy()}, elevatedTool())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	definitions, err := registry.Definitions(context.Background(), sessionWithProfile("trusted"))
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	if len(definitions) != 0 {
		t.Fatalf("got %d tools; allow_elevated_tools=false must win over any profile", len(definitions))
	}
}

func TestAllowlistFiltersTools(t *testing.T) {
	registry, err := NewRegistry(RegistryConfig{AllowedNames: []string{"file_read"}}, readOnlyTool(), elevatedTool())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	names := registry.ListToolNames()
	if len(names) != 1 || names[0] != "file_read" {
		t.Fatalf("names = %v, want only file_read", names)
	}
}

func TestCatalogExplainsWhyElevatedToolsAreUnavailable(t *testing.T) {
	registry, err := NewRegistry(RegistryConfig{AllowElevated: true, Permissions: trustedPolicy()}, elevatedTool())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	session := sessionWithProfile("default")

	entries := registry.Catalog(&session)
	if len(entries) != 1 {
		t.Fatalf("catalog has %d entries, want 1", len(entries))
	}
	if entries[0].Available {
		t.Error("elevated tool should not be available to the default profile")
	}
	if entries[0].UnavailableReason == "" {
		t.Error("catalog should explain why the tool is unavailable")
	}
}
