package documents_test

import (
	"archive/zip"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"AgenticService/src/domain"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/documents"
)

// 走完整的 Registry 路徑，因為參數校正發生在那裡。直接呼叫 Execute 會跳過它，
// 之前就是因此漏掉了這個案例。
func createThroughRegistry(t *testing.T, root string, arguments map[string]any) domain.ToolExecution {
	t.Helper()
	registry, err := tools.NewRegistry(tools.RegistryConfig{
		AllowElevated: true,
		Permissions:   domain.PermissionPolicy{DefaultProfile: "default", ElevatedProfiles: []string{"trusted"}},
	}, documents.NewCreateTool(1<<20, 32<<20))
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	result, err := registry.Execute(context.Background(),
		domain.Session{ID: "session-1", PermissionProfile: "trusted", Metadata: map[string]any{"workspace_root": root}},
		domain.ToolCall{ID: "call-1", Name: "document_create", Arguments: arguments}, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return result
}

func docxText(t *testing.T, path string) string {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open docx: %v", err)
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name != "word/document.xml" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatalf("open document.xml: %v", err)
		}
		defer reader.Close()
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read document.xml: %v", err)
		}
		return string(data)
	}
	t.Fatal("document.xml is missing")
	return ""
}

// 「數量」欄寫成 264 而不是 "264" 是最自然的寫法。原本整個 document_create 會失敗，
// 而模型讀完錯誤訊息的結論是「這個工具不支援」，改用別的工具繞路。
func TestCreateDOCXAcceptsMixedCellTypes(t *testing.T) {
	root := t.TempDir()
	result := createThroughRegistry(t, root, map[string]any{
		"path":  "notice.docx",
		"title": "緊急變更通知單",
		"blocks": []any{
			map[string]any{"type": "paragraph", "text": "以下批號的顏色已變更。"},
			map[string]any{"type": "table", "rows": []any{
				[]any{"批號", "數量", "已生效"},
				[]any{"A-001", 264, true},
				[]any{"A-002", 3.5, nil},
			}},
		},
	})
	if result.IsError {
		t.Fatalf("mixed cell types must be accepted: %s", result.Content)
	}
	body := docxText(t, filepath.Join(root, "notice.docx"))
	for _, expected := range []string{"緊急變更通知單", "批號", "A-001", "264", "true", "3.5"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("the document is missing %q", expected)
		}
	}
}

// 結構寫錯不能靠型別轉換救，但錯誤訊息要讓模型知道該寫成什麼。
func TestCreateDOCXExplainsAStructuralMistake(t *testing.T) {
	root := t.TempDir()
	result := createThroughRegistry(t, root, map[string]any{
		"path":  "notice.docx",
		"title": "t",
		"blocks": []any{map[string]any{
			"type": "table",
			"rows": []any{map[string]any{"批號": "A-001", "數量": "264"}},
		}},
	})
	if !result.IsError {
		t.Fatal("a row written as an object should still fail")
	}
	// 訊息必須包含可以照抄的正確寫法，而不是 Go 的型別名稱。
	for _, expected := range []string{"rows", `[["批號","數量"]`, "不要把一列寫成物件"} {
		if !strings.Contains(result.Content, expected) {
			t.Fatalf("the error should show the correct shape, got: %s", result.Content)
		}
	}
	if strings.Contains(result.Content, "cannot unmarshal") {
		t.Fatalf("the raw Go decoding error should not reach the model: %s", result.Content)
	}
}

// XLSX 一直都收得下數字，DOCX 卻不行——同一個概念兩種嚴格度，模型無從得知。
func TestCreateXLSXAndDOCXAgreeOnCellTypes(t *testing.T) {
	root := t.TempDir()
	rows := []any{[]any{"批號", "數量"}, []any{"A-001", 264}}
	if result := createThroughRegistry(t, root, map[string]any{
		"path": "book.xlsx", "sheets": []any{map[string]any{"name": "明細", "rows": rows}},
	}); result.IsError {
		t.Fatalf("xlsx: %s", result.Content)
	}
	if result := createThroughRegistry(t, root, map[string]any{
		"path": "doc.docx", "title": "t",
		"blocks": []any{map[string]any{"type": "table", "rows": rows}},
	}); result.IsError {
		t.Fatalf("docx must accept what xlsx accepts: %s", result.Content)
	}
}
