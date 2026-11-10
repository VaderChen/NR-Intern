package tools

import (
	"context"
	"fmt"
	"testing"

	"AgenticService/src/domain"
)

func BenchmarkFunctionResolveDefinition(b *testing.B) {
	values := make([]NativeTool, 256)
	for i := range values {
		values[i] = &stubTool{definition: domain.ToolDefinition{
			Name: fmt.Sprintf("synthetic_%03d", i), Description: "合成唯讀工具", ReadOnly: true,
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}},
		}}
	}
	registry, err := NewRegistry(RegistryConfig{}, values...)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		definition, err := registry.ResolveDefinition(ctx, domain.Session{}, "synthetic_128")
		if err != nil || definition.Name != "synthetic_128" {
			b.Fatalf("工具解析失敗：%v", err)
		}
	}
}
