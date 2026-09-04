package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 這是已完成的能力，不像回憶空間那樣仍在設計中，因此預設開啟。
func TestMemoryIsolatedProjectsDefaultsToEnabled(t *testing.T) {
	if !DefaultConfig().MemoryIsolatedProjects {
		t.Fatal("記憶體隔離專案應預設開啟")
	}
}

// 升級既有安裝時，設定檔沒有這個欄位不該讓使用者突然少一項能力。
func TestMemoryIsolatedProjectsSurvivesOlderSettingsFile(t *testing.T) {
	dataDir := t.TempDir()
	// 舊版設定檔：有 memory_space，沒有 memory_isolated_projects。
	stored := map[string]any{"memory_space": true}
	data, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, serviceSettingsFilename), data, 0o640); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	config := DefaultConfig()
	config.DataDir = dataDir
	if err := loadPersistedServiceSettings(&config); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !config.MemoryIsolatedProjects {
		t.Fatal("舊設定檔缺少欄位時應沿用預設的開啟")
	}
	if !config.MemorySpace {
		t.Fatal("既有欄位仍要正確載入")
	}
}

// 明確關閉必須被保存，不能被預設值蓋掉。
func TestMemoryIsolatedProjectsHonoursExplicitFalse(t *testing.T) {
	dataDir := t.TempDir()
	data, err := json.Marshal(map[string]any{"memory_isolated_projects": false})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, serviceSettingsFilename), data, 0o640); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	config := DefaultConfig()
	config.DataDir = dataDir
	if err := loadPersistedServiceSettings(&config); err != nil {
		t.Fatalf("load: %v", err)
	}
	if config.MemoryIsolatedProjects {
		t.Fatal("明確設為 false 時不能被預設值蓋掉")
	}
}
