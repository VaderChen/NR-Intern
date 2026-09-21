package plans

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"AgenticService/src/tools"
	"context"
	"strings"
	"testing"
)

func TestPlanRejectsModelAuthoredUserConfirmationSmoke(t *testing.T) {
	repo, err := filestore.NewPlanRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := NewUpdateStepTool(repo)
	result, err := tool.Execute(context.Background(), tools.Invocation{Call: domain.ToolCall{Arguments: map[string]any{"user_confirmation": "使用者已確認", "status": "completed"}}}, nil)
	if err != nil || !result.IsError || !strings.Contains(result.Content, "不能代替使用者") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
