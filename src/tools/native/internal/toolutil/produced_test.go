package toolutil

import "testing"

// details 只送到 UI，桌面端的開檔橋接只接受絕對路徑：相對路徑進來就得丟掉，
// 否則前端會渲染一個點了必定失敗的晶片。
func TestProducedFilesKeepsOnlyAbsolutePaths(t *testing.T) {
	details := ProducedFiles(map[string]any{"path": "report.md"}, "/tmp/out/report.md", "report.md", "  ")
	values, ok := details["produced_paths"].([]string)
	if !ok {
		t.Fatalf("produced_paths missing or wrong type: %#v", details["produced_paths"])
	}
	if len(values) != 1 || values[0] != "/tmp/out/report.md" {
		t.Fatalf("got %#v, want only the absolute path", values)
	}
	if details["path"] != "report.md" {
		t.Fatal("existing details must be preserved")
	}
}

func TestProducedFilesOmitsTheKeyWhenNothingQualifies(t *testing.T) {
	details := ProducedFiles(nil, "relative/only.txt")
	if _, exists := details["produced_paths"]; exists {
		t.Fatalf("produced_paths should be absent when no absolute path was produced: %#v", details)
	}
}
