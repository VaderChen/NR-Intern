package documents

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"AgenticService/src/domain"
	"AgenticService/src/tools"
)

func createText(t *testing.T, root string, arguments map[string]any) domain.ToolExecution {
	t.Helper()
	tool := NewCreateTool(1<<20, 32<<20)
	result, err := tool.Execute(context.Background(), tools.Invocation{
		Session:       domain.Session{ID: "session-1"},
		Call:          domain.ToolCall{ID: "call-1", Arguments: arguments},
		WorkspaceRoot: root,
	}, nil)
	if err != nil {
		t.Fatalf("execute document_create: %v", err)
	}
	return result
}

func reportBlocks() []any {
	return []any{
		map[string]any{"type": "heading", "text": "產線狀況", "level": 1},
		map[string]any{"type": "paragraph", "text": "本週 CNC 部門共完成 264 筆製令。"},
		map[string]any{"type": "bullet", "text": "第一批：已完成"},
		map[string]any{"type": "bullet", "text": "第二批：等待中"},
		map[string]any{"type": "table", "rows": []any{
			[]any{"批號", "狀態"},
			[]any{"A-001", "Finish"},
			[]any{"A-002", "Waiting"},
		}},
	}
}

// Markdown 是「做一份報告」最常見的落點，原本只能走 Shell heredoc。
func TestCreateMarkdown(t *testing.T) {
	root := t.TempDir()
	result := createText(t, root, map[string]any{
		"path": "report.md", "title": "生產週報", "blocks": reportBlocks(),
	})
	if result.IsError {
		t.Fatalf("create markdown failed: %s", result.Content)
	}
	data, err := os.ReadFile(filepath.Join(root, "report.md"))
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	body := string(data)
	for _, expected := range []string{
		"# 生產週報",
		"## 產線狀況",
		"- 第一批：已完成",
		"| 批號 | 狀態 |",
		"| --- | --- |",
		"| A-001 | Finish |",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("markdown does not contain %q:\n%s", expected, body)
		}
	}
}

// 表格內容含 | 或換行時不能破壞表格結構。
func TestCreateMarkdownEscapesTableCells(t *testing.T) {
	root := t.TempDir()
	result := createText(t, root, map[string]any{
		"path": "table.md", "title": "t", "blocks": []any{
			map[string]any{"type": "table", "rows": []any{
				[]any{"欄位", "值"},
				[]any{"路徑", "a|b"},
				[]any{"備註", "第一行\n第二行"},
			}},
		},
	})
	if result.IsError {
		t.Fatalf("create markdown failed: %s", result.Content)
	}
	data, _ := os.ReadFile(filepath.Join(root, "table.md"))
	body := string(data)
	if !strings.Contains(body, `a\|b`) {
		t.Fatalf("pipe in a cell must be escaped:\n%s", body)
	}
	if !strings.Contains(body, "第一行<br>第二行") {
		t.Fatalf("newline in a cell must not break the table:\n%s", body)
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "|") && strings.Count(line, "|")-strings.Count(line, `\|`) != 3 {
			t.Fatalf("table row has the wrong column count: %q", line)
		}
	}
}

// CSV 走跟 XLSX 一樣的 sheets 結構，逗號與引號要由編碼器處理。
func TestCreateCSV(t *testing.T) {
	root := t.TempDir()
	result := createText(t, root, map[string]any{
		"path": "batches.csv", "sheets": []any{map[string]any{
			"name": "批號", "rows": []any{
				[]any{"批號", "備註", "數量"},
				[]any{"A-001", "含逗號, 與\"引號\"", 264},
				[]any{"A-002", "正常", 3.5},
			},
		}},
	})
	if result.IsError {
		t.Fatalf("create csv failed: %s", result.Content)
	}
	file, err := os.Open(filepath.Join(root, "batches.csv"))
	if err != nil {
		t.Fatalf("open csv: %v", err)
	}
	defer file.Close()
	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatalf("the generated CSV does not parse: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("got %d records, want 3", len(records))
	}
	if records[1][1] != `含逗號, 與"引號"` {
		t.Fatalf("quoting round-trip failed: %q", records[1][1])
	}
	// 整數不能變成 264 以外的東西，小數也不能變成科學記號。
	if records[1][2] != "264" || records[2][2] != "3.5" {
		t.Fatalf("numbers were reformatted: %q / %q", records[1][2], records[2][2])
	}
}

// CSV 只有一張表；給多張時要明確失敗，不能默默丟掉。
func TestCreateCSVRejectsMultipleSheets(t *testing.T) {
	root := t.TempDir()
	result := createText(t, root, map[string]any{
		"path": "many.csv", "sheets": []any{
			map[string]any{"name": "一", "rows": []any{[]any{"a"}}},
			map[string]any{"name": "二", "rows": []any{[]any{"b"}}},
		},
	})
	if !result.IsError {
		t.Fatal("expected multiple sheets to be rejected for CSV")
	}
	if !strings.Contains(result.Content, "xlsx") {
		t.Fatalf("the error should point at the format that holds many sheets: %s", result.Content)
	}
}

func TestCreateHTML(t *testing.T) {
	root := t.TempDir()
	result := createText(t, root, map[string]any{
		"path": "report.html", "title": "生產週報", "author": "產線", "blocks": reportBlocks(),
	})
	if result.IsError {
		t.Fatalf("create html failed: %s", result.Content)
	}
	data, _ := os.ReadFile(filepath.Join(root, "report.html"))
	body := string(data)
	for _, expected := range []string{
		`<meta charset="utf-8">`, "<title>生產週報</title>", "<h1>生產週報</h1>",
		"<h2>產線狀況</h2>", "<ul>", "<li>第一批：已完成</li>", "</ul>", "<th>批號</th>", "<td>A-001</td>",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("html does not contain %q:\n%s", expected, body)
		}
	}
	// 連續的清單項目要收在同一個 <ul> 裡，不能每一項各開一個。
	if strings.Count(body, "<ul>") != 1 {
		t.Fatalf("adjacent bullets should share one list:\n%s", body)
	}
}

// HTML 必須跳脫，否則內容裡的標籤會變成真的標籤。
func TestCreateHTMLEscapesContent(t *testing.T) {
	root := t.TempDir()
	result := createText(t, root, map[string]any{
		"path": "escape.html", "title": "<script>alert(1)</script>",
		"blocks": []any{map[string]any{"type": "paragraph", "text": "5 < 6 && \"引號\""}},
	})
	if result.IsError {
		t.Fatalf("create html failed: %s", result.Content)
	}
	data, _ := os.ReadFile(filepath.Join(root, "escape.html"))
	body := string(data)
	if strings.Contains(body, "<script>") {
		t.Fatalf("content must be escaped:\n%s", body)
	}
	if !strings.Contains(body, "5 &lt; 6") {
		t.Fatalf("angle brackets must be escaped:\n%s", body)
	}
}

// 純文字表格用顯示寬度對齊：中文一個字三個位元組，用位元組排版會歪掉。
func TestCreatePlainTextAlignsCJKTable(t *testing.T) {
	root := t.TempDir()
	result := createText(t, root, map[string]any{
		"path": "notes.txt", "title": "備註", "blocks": []any{
			map[string]any{"type": "table", "rows": []any{
				[]any{"批號", "狀態"},
				[]any{"A-1", "已完成"},
			}},
		},
	})
	if result.IsError {
		t.Fatalf("create text failed: %s", result.Content)
	}
	data, _ := os.ReadFile(filepath.Join(root, "notes.txt"))
	lines := strings.Split(string(data), "\n")
	var first, second string
	for _, line := range lines {
		if strings.HasPrefix(line, "批號") {
			first = line
		}
		if strings.HasPrefix(line, "A-1") {
			second = line
		}
	}
	if first == "" || second == "" {
		t.Fatalf("table rows missing:\n%s", data)
	}
	if displayWidth(strings.Split(first, "狀態")[0]) != displayWidth(strings.Split(second, "已完成")[0]) {
		t.Fatalf("columns are not aligned by display width:\n%q\n%q", first, second)
	}
}

// 純文字格式沒有範本可保留，錯誤要說清楚原因。
func TestCreateTextRejectsTemplate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "base.md"), []byte("# base\n"), 0o640); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	result := createText(t, root, map[string]any{
		"path": "out.md", "template_path": "base.md", "title": "t",
	})
	if !result.IsError {
		t.Fatal("expected template_path to be rejected for text formats")
	}
	if !strings.Contains(result.Content, "純文字") {
		t.Fatalf("the error should explain why: %s", result.Content)
	}
}

// 副檔名與 format 不一致時要擋下，別把 HTML 寫進 .md。
func TestCreateTextChecksExtension(t *testing.T) {
	root := t.TempDir()
	result := createText(t, root, map[string]any{"path": "report.md", "format": "html", "title": "t"})
	if !result.IsError {
		t.Fatal("expected a format/extension mismatch to be rejected")
	}
	// 常見別名仍要通行。
	if out := createText(t, root, map[string]any{"path": "page.htm", "format": "html", "title": "t"}); out.IsError {
		t.Fatalf(".htm should be accepted for html: %s", out.Content)
	}
	if out := createText(t, root, map[string]any{"path": "doc.markdown", "format": "markdown", "title": "t"}); out.IsError {
		t.Fatalf(".markdown should be accepted: %s", out.Content)
	}
}
