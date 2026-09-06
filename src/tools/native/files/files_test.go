package files

import (
	"AgenticService/src/domain"
	"AgenticService/src/tools"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fileInvocation(root, name string, arguments map[string]any) tools.Invocation {
	return tools.Invocation{
		WorkspaceRoot: root,
		Call:          domain.ToolCall{ID: "call_test", Name: name, Arguments: arguments},
	}
}

func TestWriteToolRefusesImplicitOverwriteAndPreservesFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(path, []byte("original"), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	result, err := NewWriteTool(1024).Execute(context.Background(), fileInvocation(root, "file_write", map[string]any{
		"path": "existing.txt", "content": "replacement",
	}), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Fatalf("implicit overwrite succeeded: %+v", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "original" {
		t.Fatalf("existing file changed to %q", data)
	}
}

func TestWriteToolReportsResultMetricsForLifecycleVerification(t *testing.T) {
	root := t.TempDir()
	result, err := NewWriteTool(1024).Execute(context.Background(), fileInvocation(root, "file_write", map[string]any{
		"path": "metrics.txt", "content": "甲乙\nABC",
	}), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if result.Details["unicode_characters"] != 6 || result.Details["lines"] != 2 || result.Details["bytes"] != 10 {
		t.Fatalf("metrics = %+v", result.Details)
	}
	if !strings.Contains(result.Content, "6 Unicode characters") {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestEditToolPreconditionFailureDoesNotPartiallyWrite(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "edit.txt")
	if err := os.WriteFile(path, []byte("one one"), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	result, err := NewEditTool(1024).Execute(context.Background(), fileInvocation(root, "file_edit", map[string]any{
		"path": "edit.txt", "old_text": "one", "new_text": "two",
		"replace_all": true, "expected_replacements": 1,
	}), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "precondition") {
		t.Fatalf("result = %+v", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "one one" {
		t.Fatalf("file was partially edited: %q", data)
	}
}

func TestReadToolRejectsWorkspaceTraversal(t *testing.T) {
	root := t.TempDir()
	result, err := NewReadTool(1024).Execute(context.Background(), fileInvocation(root, "file_read", map[string]any{
		"path": "../outside.txt",
	}), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "escapes") {
		t.Fatalf("traversal result = %+v", result)
	}
}

// 模型把 end_line 當成「要讀幾行」是實測到的行為（連續三次 start_line=400、
// end_line=170）。錯誤訊息要說出正確的意思與這次該填什麼，否則只能猜著重試——
// 每次工具呼叫都要先失敗一輪。
func TestFileReadRejectsLineRangeWithActionableMessage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("a\nb\nc\n"), 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}
	result, err := NewReadTool(64*1024).Execute(context.Background(), fileInvocation(root, "file_read", map[string]any{
		"path": "sample.txt", "start_line": 400, "end_line": 170,
	}), nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.IsError {
		t.Fatal("end_line 小於 start_line 應該失敗")
	}
	// 收到什麼、正確的意思是什麼、這次該填什麼——三件事都要在訊息裡。
	for _, want := range []string{"400", "170", "絕對行號", "end_line=569"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("錯誤訊息應包含 %q 讓模型能直接改正：%s", want, result.Content)
		}
	}
}
