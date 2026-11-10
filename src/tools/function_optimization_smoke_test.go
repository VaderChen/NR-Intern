package tools

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"AgenticService/src/domain"
)

type definitionCountingTool struct {
	stubTool
	definitions int
}

func (t *definitionCountingTool) Definition() domain.ToolDefinition {
	t.definitions++
	return t.definition
}

func TestResolveDefinitionCompatibilitySmoke(t *testing.T) {
	ctx := context.Background()
	for _, allowed := range [][]string{nil, {"file_read", "http_fetch"}} {
		toggle := &toggleableStub{stubTool: stubTool{definition: domain.ToolDefinition{Name: "http_fetch"}}}
		registry, err := NewRegistry(RegistryConfig{AllowedNames: allowed, AllowElevated: true, Permissions: trustedPolicy()}, readOnlyTool(), elevatedTool(), toggle)
		if err != nil {
			t.Fatal(err)
		}
		for _, enabled := range []bool{false, true} {
			toggle.enabled = enabled
			for _, profile := range []string{"default", "trusted"} {
				session := sessionWithProfile(profile)
				definitions, _ := registry.Definitions(ctx, session)
				want := map[string]domain.ToolDefinition{}
				for _, definition := range definitions {
					want[definition.Name] = definition
				}
				for _, name := range []string{"file_read", "shell_exec", "http_fetch", "absent", " file_read "} {
					got, err := registry.ResolveDefinition(ctx, session, name)
					if definition, exists := want[name]; exists {
						if err != nil || !reflect.DeepEqual(got, definition) {
							t.Fatalf("定義／契約不同：%s %v", name, err)
						}
					} else if !errors.Is(err, domain.ErrNotFound) {
						t.Fatalf("不應允許的工具：%s %v", name, err)
					}
				}
			}
		}
	}
}

func TestResolveDefinitionDoesNotCacheContractsSmoke(t *testing.T) {
	values := make([]NativeTool, 32)
	for i := range values {
		values[i] = &definitionCountingTool{stubTool: stubTool{definition: domain.ToolDefinition{Name: fmt.Sprintf("tool_%d", i), ReadOnly: true}}}
	}
	r, err := NewRegistry(RegistryConfig{AllowElevated: true, Permissions: trustedPolicy()}, values...)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		value.(*definitionCountingTool).definitions = 0
	}
	ctx, session := context.Background(), sessionWithProfile("default")
	first, err := r.ResolveDefinition(ctx, session, "tool_0")
	if err != nil {
		t.Fatal(err)
	}
	for i, value := range values {
		want := 0
		if i == 0 {
			want = 1
		}
		if value.(*definitionCountingTool).definitions != want {
			t.Fatal("單工具解析仍讀取無關工具")
		}
	}
	target := values[0].(*definitionCountingTool)
	target.definition.Description = "已更新的合成契約"
	second, err := r.ResolveDefinition(ctx, session, "tool_0")
	if err != nil || first.ContractID == second.ContractID {
		t.Fatal("契約更新未生效", err)
	}
	result, err := r.Execute(ctx, session, domain.ToolCall{ID: "synthetic", Name: "tool_0", ExpectedContractID: first.ContractID}, nil)
	if err != nil || !result.IsError || target.calls != 0 {
		t.Fatal("舊契約被派送", err)
	}
	target.definition.RequiresPermission = true
	if _, err := r.ResolveDefinition(ctx, session, "tool_0"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("權限更新未生效", err)
	}
}
