package documents

import (
	"AgenticService/src/domain"
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
