package documents

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/tools"
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentToolsInspectAndReadOpenXML(t *testing.T) {
	root := t.TempDir()
	fixtures := []struct {
		name       string
		format     string
		entries    map[string]string
		arguments  map[string]any
		wantText   string
		wantDetail string
	}{
		{
			name:   "sample.docx",
			format: "docx",
			entries: map[string]string{
				"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
				"docProps/core.xml":   `<cp:coreProperties xmlns:cp="x" xmlns:dc="y"><dc:title>Word Title</dc:title><dc:creator>Ada</dc:creator></cp:coreProperties>`,
				"word/document.xml":   `<w:document xmlns:w="w"><w:body><w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>年度報告</w:t></w:r></w:p><w:p><w:r><w:t>營收成長 20%</w:t></w:r></w:p></w:body></w:document>`,
			},
			arguments:  map[string]any{"start_paragraph": 1, "end_paragraph": 2},
			wantText:   "營收成長 20%",
			wantDetail: "paragraph_count",
		},
		{
			name:   "sample.xlsx",
			format: "xlsx",
			entries: map[string]string{
				"[Content_Types].xml":        `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
				"xl/workbook.xml":            `<workbook xmlns="x" xmlns:r="r"><sheets><sheet name="Summary" sheetId="1" r:id="rId1"/></sheets></workbook>`,
				"xl/_rels/workbook.xml.rels": `<Relationships xmlns="x"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
				"xl/sharedStrings.xml":       `<sst xmlns="x"><si><t>項目</t></si><si><t>收入</t></si></sst>`,
				"xl/worksheets/sheet1.xml":   `<worksheet xmlns="x"><dimension ref="A1:B2"/><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row><row r="2"><c r="A2" t="inlineStr"><is><t>Q1</t></is></c><c r="B2"><f>SUM(40,2)</f><v>42</v></c></row></sheetData></worksheet>`,
			},
			arguments:  map[string]any{"section": "Summary", "start_row": 1, "end_row": 2},
			wantText:   `B2 formula="SUM(40,2)" value="42"`,
			wantDetail: "rows_read",
		},
		{
			name:   "sample.pptx",
			format: "pptx",
			entries: map[string]string{
				"[Content_Types].xml":             `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
				"ppt/presentation.xml":            `<p:presentation xmlns:p="p" xmlns:r="r"><p:sldIdLst><p:sldId id="256" r:id="rId1"/></p:sldIdLst></p:presentation>`,
				"ppt/_rels/presentation.xml.rels": `<Relationships xmlns="x"><Relationship Id="rId1" Target="slides/slide1.xml"/></Relationships>`,
				"ppt/slides/slide1.xml":           `<p:sld xmlns:p="p" xmlns:a="a"><p:cSld><a:p><a:r><a:t>產品藍圖</a:t></a:r></a:p><a:p><a:r><a:t>第一階段</a:t></a:r></a:p></p:cSld></p:sld>`,
			},
			arguments:  map[string]any{"start_page": 1, "end_page": 1},
			wantText:   "產品藍圖",
			wantDetail: "slide_count",
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.format, func(t *testing.T) {
			filePath := filepath.Join(root, fixture.name)
			writeOpenXMLFixture(t, filePath, fixture.entries)
			inspect := executeDocumentTool(t, NewInspectTool(2*1024*1024, 64*1024), root, "document_inspect", map[string]any{"path": fixture.name})
			if inspect.IsError || !strings.Contains(inspect.Content, `"format": "`+fixture.format+`"`) {
				t.Fatalf("inspect %s: error=%v content=%s", fixture.format, inspect.IsError, inspect.Content)
			}
			arguments := map[string]any{"path": fixture.name}
			for key, value := range fixture.arguments {
				arguments[key] = value
			}
			read := executeDocumentTool(t, NewReadTool(2*1024*1024, 64*1024), root, "document_read", arguments)
			if read.IsError || !strings.Contains(read.Content, fixture.wantText) {
				t.Fatalf("read %s: error=%v content=%s", fixture.format, read.IsError, read.Content)
			}
			if _, exists := read.Details[fixture.wantDetail]; !exists {
				t.Fatalf("read %s details missing %q: %#v", fixture.format, fixture.wantDetail, read.Details)
			}
		})
	}
}

func TestDocumentReadPDF(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "sample.pdf")
	writeMinimalPDF(t, filePath, "Hello PDF")
	inspect := executeDocumentTool(t, NewInspectTool(2*1024*1024, 64*1024), root, "document_inspect", map[string]any{"path": "sample.pdf"})
	if inspect.IsError || !strings.Contains(inspect.Content, `"page_count": 1`) {
		t.Fatalf("inspect PDF: error=%v content=%s", inspect.IsError, inspect.Content)
	}
	read := executeDocumentTool(t, NewReadTool(2*1024*1024, 64*1024), root, "document_read", map[string]any{
		"path": "sample.pdf", "start_page": 1, "end_page": 1,
	})
	if read.IsError || !strings.Contains(read.Content, "Hello PDF") {
		t.Fatalf("read PDF: error=%v content=%s", read.IsError, read.Content)
	}
}

func TestDocumentToolsRejectPathOutsideSandbox(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.docx")
	writeOpenXMLFixture(t, outside, map[string]string{
		"[Content_Types].xml": `<Types/>`,
		"word/document.xml":   `<document/>`,
	})
	result := executeDocumentTool(t, NewInspectTool(2*1024*1024, 64*1024), root, "document_inspect", map[string]any{"path": outside})
	if !result.IsError || !strings.Contains(result.Content, "outside the project sandbox") {
		t.Fatalf("outside sandbox result = %#v", result)
	}
}

func executeDocumentTool(t *testing.T, tool tools.NativeTool, root, name string, arguments map[string]any) domain.ToolExecution {
	t.Helper()
	result, err := tool.Execute(context.Background(), tools.Invocation{
		Session:        domain.Session{ID: "session_test"},
		Call:           domain.ToolCall{ID: "call_test", Name: name, Arguments: arguments},
		WorkspaceRoot:  root,
		WorkspaceRoots: []string{root},
	}, nil)
	if err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
	return result
}

func writeOpenXMLFixture(t *testing.T, filePath string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, content := range entries {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeMinimalPDF(t *testing.T, filePath, text string) {
	t.Helper()
	stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
	}
	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = output.Len()
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n", len(objects)+1)
	output.WriteString("0000000000 65535 f \n")
	for index := 1; index < len(offsets); index++ {
		fmt.Fprintf(&output, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	if err := os.WriteFile(filePath, output.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// 使用者說「轉成 Excel」時，產出必須是真的能打開、而且中文正確的 .xlsx。
// 這條路徑先前不在預設工具集裡，Agent 只能用 shell 寫 CSV，Excel 打開是亂碼。
func TestDocumentCreateWritesReadableChineseXLSX(t *testing.T) {
	root := t.TempDir()
	create := executeDocumentTool(t, NewCreateTool(2*1024*1024, 8*1024*1024), root, "document_create", map[string]any{
		"path":   "設備狀況.xlsx",
		"format": "xlsx",
		"title":  "設備狀況",
		"sheets": []any{map[string]any{
			"name":        "設備",
			"header_rows": 1,
			"rows": []any{
				[]any{"部門", "設備代碼", "設備名稱", "稼動"},
				[]any{"雷雕部門", "MA-01", "雷雕機一號", 1},
				[]any{"組裝區", "CNC01_01", "銑床", 1},
			},
		}},
	})
	if create.IsError {
		t.Fatalf("document_create failed: %s", create.Content)
	}

	filePath := filepath.Join(root, "設備狀況.xlsx")
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat created workbook: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("created workbook is empty")
	}
	// 真的是 OpenXML 容器，不是改了副檔名的 CSV。
	archive, err := zip.OpenReader(filePath)
	if err != nil {
		t.Fatalf("created file is not a valid xlsx container: %v", err)
	}
	_ = archive.Close()

	// 讀回來確認中文沒有壞掉。
	read := executeDocumentTool(t, NewReadTool(2*1024*1024, 64*1024), root, "document_read", map[string]any{
		"path": "設備狀況.xlsx", "section": "設備", "start_row": 1, "end_row": 3,
	})
	if read.IsError {
		t.Fatalf("document_read failed: %s", read.Content)
	}
	for _, want := range []string{"部門", "設備代碼", "雷雕機一號", "MA-01"} {
		if !strings.Contains(read.Content, want) {
			t.Fatalf("%q missing from the workbook: %s", want, read.Content)
		}
	}
}

// 端對端：模型把 sheets 整個字串化送進來時，document_create 仍要能產出檔案。
// 這是實測 transcript 裡連續失敗的形狀。
func TestDocumentCreateAcceptsStringifiedSheets(t *testing.T) {
	root := t.TempDir()
	registry, err := tools.NewRegistry(tools.RegistryConfig{
		AllowElevated: true,
		Permissions:   domain.PermissionPolicy{DefaultProfile: domain.DefaultPermissionProfile, ElevatedProfiles: []string{domain.DefaultPermissionProfile}},
		Logger:        logging.Discard(),
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if err := registry.Register(NewCreateTool(2*1024*1024, 8*1024*1024)); err != nil {
		t.Fatalf("Register: %v", err)
	}
	session := domain.Session{ID: "session_test", PermissionProfile: domain.DefaultPermissionProfile,
		Metadata: map[string]any{"workspace_root": root}}

	result, err := registry.Execute(context.Background(), session, domain.ToolCall{
		ID:   "call_1",
		Name: "document_create",
		Arguments: map[string]any{
			"path":   "設備狀況.xlsx",
			"format": "xlsx",
			"sheets": `[{"name":"設備","rows":[["部門","代碼"],["CNC","MC0002"]]}]`,
		},
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("document_create rejected stringified sheets: %s", result.Content)
	}
	if _, err := os.Stat(filepath.Join(root, "設備狀況.xlsx")); err != nil {
		t.Fatalf("workbook was not written: %v", err)
	}
}

// 模型描述新表格時很自然會用儲存格的語言，而不是 sheets/rows。
// 實測就是這樣連續失敗到被 loop guard 擋下，最後回使用者「XLSX 工具呼叫失敗」。
func TestDocumentCreateBuildsSheetsFromCellUpdates(t *testing.T) {
	root := t.TempDir()
	create := executeDocumentTool(t, NewCreateTool(2*1024*1024, 8*1024*1024), root, "document_create", map[string]any{
		"path":   "設備狀況.xlsx",
		"format": "xlsx",
		"cell_updates": []any{
			map[string]any{"sheet": "設備清單", "cell": "A1", "value": "設備狀況總覽表"},
			map[string]any{"sheet": "設備清單", "cell": "A2", "value": "部門"},
			map[string]any{"sheet": "設備清單", "cell": "B2", "value": "設備代碼"},
			map[string]any{"sheet": "設備清單", "cell": "A3", "value": "CNC"},
			map[string]any{"sheet": "設備清單", "cell": "B3", "value": "MC0002"},
			map[string]any{"sheet": "統計", "cell": "A1", "value": "總計"},
			map[string]any{"sheet": "統計", "cell": "B1", "value": 23},
		},
	})
	if create.IsError {
		t.Fatalf("document_create rejected cell_updates: %s", create.Content)
	}

	read := executeDocumentTool(t, NewReadTool(2*1024*1024, 64*1024), root, "document_read", map[string]any{
		"path": "設備狀況.xlsx", "section": "設備清單", "start_row": 1, "end_row": 3,
	})
	if read.IsError {
		t.Fatalf("document_read failed: %s", read.Content)
	}
	for _, want := range []string{"設備狀況總覽表", "設備代碼", "MC0002"} {
		if !strings.Contains(read.Content, want) {
			t.Fatalf("%q missing from the first sheet: %s", want, read.Content)
		}
	}
	// 第二張工作表也要建出來。
	second := executeDocumentTool(t, NewReadTool(2*1024*1024, 64*1024), root, "document_read", map[string]any{
		"path": "設備狀況.xlsx", "section": "統計", "start_row": 1, "end_row": 1,
	})
	if second.IsError || !strings.Contains(second.Content, "總計") {
		t.Fatalf("the second sheet was not created: error=%v content=%s", second.IsError, second.Content)
	}
}

// 模型會用 Excel 的完整寫法 Sheet1!A1。實測因為只接受 A1 而被拒絕，
// 模型改對之後又被 loop guard 擋下，最後用 shell 寫出一個空殼檔案交差。
func TestDocumentCreateAcceptsQualifiedCellReferences(t *testing.T) {
	root := t.TempDir()
	create := executeDocumentTool(t, NewCreateTool(2*1024*1024, 8*1024*1024), root, "document_create", map[string]any{
		"path": "設備狀況.xlsx", "format": "xlsx",
		"cell_updates": []any{
			map[string]any{"cell": "設備清單!A1", "value": "設備狀況總覽"},
			map[string]any{"cell": "'設備清單'!B1", "value": "部門"},
			map[string]any{"cell": "A2", "sheet": "設備清單", "value": "CNC"},
		},
	})
	if create.IsError {
		t.Fatalf("qualified references were rejected: %s", create.Content)
	}
	read := executeDocumentTool(t, NewReadTool(2*1024*1024, 64*1024), root, "document_read", map[string]any{
		"path": "設備狀況.xlsx", "section": "設備清單", "start_row": 1, "end_row": 2,
	})
	if read.IsError {
		t.Fatalf("document_read failed: %s", read.Content)
	}
	for _, want := range []string{"設備狀況總覽", "部門", "CNC"} {
		if !strings.Contains(read.Content, want) {
			t.Fatalf("%q missing: %s", want, read.Content)
		}
	}
}

// 儲存格參照錯誤要明確回報，不能默默產出一份空表。
func TestDocumentCreateRejectsBadCellReference(t *testing.T) {
	root := t.TempDir()
	result := executeDocumentTool(t, NewCreateTool(2*1024*1024, 8*1024*1024), root, "document_create", map[string]any{
		"path": "壞的.xlsx", "format": "xlsx",
		"cell_updates": []any{map[string]any{"sheet": "S", "cell": "第一格", "value": 1}},
	})
	if !result.IsError || !strings.Contains(result.Content, "A1") {
		t.Fatalf("a bad cell reference was not reported clearly: error=%v content=%s", result.IsError, result.Content)
	}
}

// 模型不一定用 A1 標記法。實測看過自創的 "sheet0/1/1"，也可能改用數字欄位。
// 兩種都要能建出正確的表，否則就是又一輪重試到被擋下。
func TestDocumentCreateAcceptsNumericAndSlashCellAddressing(t *testing.T) {
	root := t.TempDir()
	create := executeDocumentTool(t, NewCreateTool(2*1024*1024, 8*1024*1024), root, "document_create", map[string]any{
		"path": "設備.xlsx", "format": "xlsx",
		"cell_updates": []any{
			map[string]any{"cell": "設備清單/1/1", "value": "設備狀況報告"},
			map[string]any{"cell": "設備清單/1/2", "value": "設備分佈總覽"},
			map[string]any{"sheet": "設備清單", "row": 2, "column": 1, "value": "CNC"},
			map[string]any{"sheet": "設備清單", "row": 2, "column": 2, "value": "MC0002"},
		},
	})
	if create.IsError {
		t.Fatalf("numeric or slash addressing was rejected: %s", create.Content)
	}
	read := executeDocumentTool(t, NewReadTool(2*1024*1024, 64*1024), root, "document_read", map[string]any{
		"path": "設備.xlsx", "section": "設備清單", "start_row": 1, "end_row": 2,
	})
	if read.IsError {
		t.Fatalf("document_read failed: %s", read.Content)
	}
	for _, want := range []string{"設備狀況報告", "設備分佈總覽", "CNC", "MC0002"} {
		if !strings.Contains(read.Content, want) {
			t.Fatalf("%q missing: %s", want, read.Content)
		}
	}
}

// 實測 20 次 document_create 呼叫裡反覆出現的寫法：一筆描述一整張表，
// value 是「儲存格參照 → 內容」的對照表。對 23 列的表要求上百筆單格項目並不合理。
func TestDocumentCreateAcceptsCellReferenceMap(t *testing.T) {
	root := t.TempDir()
	create := executeDocumentTool(t, NewCreateTool(2*1024*1024, 8*1024*1024), root, "document_create", map[string]any{
		"path": "設備總覽.xlsx", "format": "xlsx", "title": "設備狀況",
		"cell_updates": []any{
			map[string]any{"sheet": "設備總覽", "value": map[string]any{
				"A1": "部門", "B1": "設備數量", "C1": "設備代碼",
				"A2": "BOM 整合測試部", "B2": 4, "C2": "TST-BOMCASE-MC-PACK",
				"A3": "CNC", "B3": 6, "C3": "MC0002",
			}},
		},
	})
	if create.IsError {
		t.Fatalf("the cell-reference map was rejected: %s", create.Content)
	}
	read := executeDocumentTool(t, NewReadTool(2*1024*1024, 64*1024), root, "document_read", map[string]any{
		"path": "設備總覽.xlsx", "section": "設備總覽", "start_row": 1, "end_row": 3,
	})
	if read.IsError {
		t.Fatalf("document_read failed: %s", read.Content)
	}
	for _, want := range []string{"部門", "設備數量", "BOM 整合測試部", "MC0002"} {
		if !strings.Contains(read.Content, want) {
			t.Fatalf("%q missing from the workbook: %s", want, read.Content)
		}
	}
}

// 一般的巢狀資料不能被誤判成儲存格對照表。
func TestDocumentCreateRejectsNonCellMaps(t *testing.T) {
	root := t.TempDir()
	result := executeDocumentTool(t, NewCreateTool(2*1024*1024, 8*1024*1024), root, "壞的.xlsx", map[string]any{
		"path": "壞的.xlsx", "format": "xlsx",
		"cell_updates": []any{map[string]any{"sheet": "S", "value": map[string]any{"columns": []any{"部門"}}}},
	})
	if !result.IsError {
		t.Fatal("a nested data structure was silently treated as a cell map")
	}
	if !strings.Contains(result.Content, "A1") {
		t.Fatalf("the error must teach the accepted shapes: %s", result.Content)
	}
}

// 四種位置寫法混在同一次呼叫裡也要各走各的路：逐筆偵測形狀，不是全域二選一。
func TestDocumentCreateRoutesMixedCellShapes(t *testing.T) {
	root := t.TempDir()
	create := executeDocumentTool(t, NewCreateTool(2*1024*1024, 8*1024*1024), root, "document_create", map[string]any{
		"path": "混用.xlsx", "format": "xlsx",
		"cell_updates": []any{
			map[string]any{"sheet": "設備", "value": map[string]any{"A1": "部門", "B1": "數量"}}, // 儲存格對照表
			map[string]any{"sheet": "設備", "cell": "A2", "value": "CNC"},                    // A1
			map[string]any{"cell": "設備!B2", "value": 6},                                    // Sheet!A1
			map[string]any{"sheet": "設備", "row": 3, "column": 1, "value": "雷雕"},            // row/column
			map[string]any{"cell": "設備/3/2", "value": 3},                                   // 工作表/列/欄
		},
	})
	if create.IsError {
		t.Fatalf("mixed cell shapes were rejected: %s", create.Content)
	}
	read := executeDocumentTool(t, NewReadTool(2*1024*1024, 64*1024), root, "document_read", map[string]any{
		"path": "混用.xlsx", "section": "設備", "start_row": 1, "end_row": 3,
	})
	if read.IsError {
		t.Fatalf("document_read failed: %s", read.Content)
	}
	for _, want := range []string{"部門", "數量", "CNC", "雷雕"} {
		if !strings.Contains(read.Content, want) {
			t.Fatalf("%q missing; a shape was not routed correctly: %s", want, read.Content)
		}
	}
}

// 實測：模型把整張表的 rows 放進了 cell_updates。內容完全正確，只是放錯欄位。
func TestDocumentCreateAcceptsSheetRowsInsideCellUpdates(t *testing.T) {
	root := t.TempDir()
	create := executeDocumentTool(t, NewCreateTool(2*1024*1024, 8*1024*1024), root, "document_create", map[string]any{
		"path": "設備.xlsx", "format": "xlsx",
		"cell_updates": []any{
			map[string]any{"sheet": "設備分佈總覽", "rows": []any{
				[]any{"設備名稱", "設備代碼", "部門"},
				[]any{"BOM 測試包裝設備", "TST-BOMCASE-MC-PACK", "BOM 整合測試部"},
			}},
		},
	})
	if create.IsError {
		t.Fatalf("sheet rows inside cell_updates were rejected: %s", create.Content)
	}
	read := executeDocumentTool(t, NewReadTool(2*1024*1024, 64*1024), root, "document_read", map[string]any{
		"path": "設備.xlsx", "section": "設備分佈總覽", "start_row": 1, "end_row": 2,
	})
	if read.IsError {
		t.Fatalf("document_read failed: %s", read.Content)
	}
	for _, want := range []string{"設備名稱", "TST-BOMCASE-MC-PACK", "BOM 整合測試部"} {
		if !strings.Contains(read.Content, want) {
			t.Fatalf("%q missing: %s", want, read.Content)
		}
	}
}
