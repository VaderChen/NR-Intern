package main

import (
	"os"
	"path/filepath"
	"testing"
)

// 從 Finder、Dock 或開始選單啟動時工作目錄是根目錄，而 macOS 的根目錄唯讀。
// data_dir 預設是相對路徑，於是後端建不出資料夾就退出，畫面只顯示
// 「後端未啟動」。這一條釘住「不可寫就換一個可寫的地方」。
func TestEnsureWritableWorkingDirectoryMovesOffAReadOnlyDirectory(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })

	readOnly := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(readOnly, 0o500); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })
	if err := os.Chdir(readOnly); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	if directoryIsWritable(".") {
		t.Skip("這個環境對唯讀目錄仍可寫入（例如以 root 執行），無法驗證")
	}
	if err := ensureWritableWorkingDirectory(); err != nil {
		t.Fatalf("ensureWritableWorkingDirectory: %v", err)
	}
	moved, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if moved == readOnly {
		t.Fatal("仍留在不可寫的目錄")
	}
	if !directoryIsWritable(".") {
		t.Fatalf("換過去的目錄仍不可寫：%s", moved)
	}
}

// 可寫的工作目錄不能被改掉：從專案目錄直接執行時要繼續用該目錄下的 data/ai-agent。
func TestEnsureWritableWorkingDirectoryKeepsAWritableDirectory(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })

	writable := t.TempDir()
	if err := os.Chdir(writable); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	before, _ := os.Getwd()
	if err := ensureWritableWorkingDirectory(); err != nil {
		t.Fatalf("ensureWritableWorkingDirectory: %v", err)
	}
	after, _ := os.Getwd()
	if before != after {
		t.Fatalf("可寫的工作目錄被改成 %s", after)
	}
}

// 測試留下的暫存檔要清乾淨，否則寫入測試本身會汙染使用者目錄。
func TestDirectoryIsWritableLeavesNothingBehind(t *testing.T) {
	directory := t.TempDir()
	if !directoryIsWritable(directory) {
		t.Fatal("暫存目錄應可寫入")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("寫入測試留下了檔案：%v", entries)
	}
}
