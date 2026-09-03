package memories_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"AgenticService/src/memory"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/memories"
)

func spaceManager(t *testing.T) *memory.Manager {
	t.Helper()
	repository, err := filestore.NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatalf("new memory repository: %v", err)
	}
	return memory.NewManager(repository, memory.Config{
		Enabled:     true,
		AutoRecall:  true,
		AllowWrites: true,
		Space:       memory.SpaceConfig{Enabled: true},
	})
}

func invocation(arguments map[string]any) tools.Invocation {
	return tools.Invocation{
		Session: domain.Session{ID: "session-1", AgentID: "agent-1", WorkspaceID: "workspace-1", ProjectID: "project-1"},
		Call:    domain.ToolCall{ID: "call-1", Arguments: arguments},
	}
}

// 寫入與檢索必須落在同一個 scope，否則模型找不到自己剛寫進去的記憶。
func TestRememberThenSearchSharesScope(t *testing.T) {
	manager := spaceManager(t)
	remember := memories.NewRememberTool(manager)
	search := memories.NewSearchTool(manager)

	result, err := remember.Execute(context.Background(), invocation(map[string]any{
		"content": "報表輸出一律使用 UTF-8 with BOM",
		"kind":    "preference",
	}), nil)
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if result.IsError {
		t.Fatalf("remember failed: %s", result.Content)
	}

	result, err = search.Execute(context.Background(), invocation(map[string]any{"query": "報表輸出編碼"}), nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if result.IsError {
		t.Fatalf("search failed: %s", result.Content)
	}
	var payload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("decode search result: %v", err)
	}
	if payload.Count != 1 {
		t.Fatalf("expected the freshly written memory to be findable, got %d", payload.Count)
	}
}

// 被擋下時要說清楚原因，否則模型只會換句話說再試一次。
func TestRememberReportsAdmissionReason(t *testing.T) {
	remember := memories.NewRememberTool(spaceManager(t))
	result, err := remember.Execute(context.Background(), invocation(map[string]any{
		"content": "這個檔案有 300 行",
		"kind":    "fact",
	}), nil)
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected fact memories to be rejected when the space is enabled")
	}
	if !strings.Contains(result.Content, "preference") {
		t.Fatalf("rejection should name the admitted kinds, got %q", result.Content)
	}
}
