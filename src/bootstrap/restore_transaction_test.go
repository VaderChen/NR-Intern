package bootstrap

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func restoreTestArchive(t *testing.T, names ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, name := range names {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(`{"restored":true}`)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestRestoreStagesAndAppliesOnlyBeforeStartupSmoke(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(root, "sessions", "old.json")
	if err := os.WriteFile(original, []byte(`{"original":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	r := &Runtime{Config: Config{DataDir: root}}
	result, err := r.Restore(context.Background(), restoreTestArchive(t, "sessions/new.json"))
	if err != nil || !result.Pending || !result.RestartRequired {
		t.Fatalf("stage: %+v %v", result, err)
	}
	if _, err := os.Stat(original); err != nil {
		t.Fatal("online data changed", err)
	}
	if _, err := r.Restore(context.Background(), restoreTestArchive(t, "sessions/other.json")); err == nil {
		t.Fatal("pending restore overwritten")
	}
	if applied, err := applyPendingRestore(root); err != nil || !applied {
		t.Fatalf("apply: %v %v", applied, err)
	}
	if _, err := os.Stat(filepath.Join(root, "sessions", "new.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatal("old snapshot was merged instead of replaced")
	}
	backups, _ := filepath.Glob(filepath.Join(root, "backups", "pre-restore-*.zip"))
	if len(backups) != 1 {
		t.Fatal("missing recovery backup")
	}
	if applied, err := applyPendingRestore(root); err != nil || applied {
		t.Fatalf("repeat: %v %v", applied, err)
	}
}

func TestRestoreRejectsWholeArchiveBeforeChangingDataSmoke(t *testing.T) {
	for _, names := range [][]string{{"sessions/good.json", "../escape"}, {"sessions/repeated.json", "sessions/repeated.json"}, {"sessions/good.json", "providers.json"}, {"service-settings.json/child.json"}, {"sessions"}, {"service-settings.json"}} {
		r := &Runtime{Config: Config{DataDir: t.TempDir()}}
		if _, err := r.Restore(context.Background(), restoreTestArchive(t, names...)); err == nil {
			t.Fatal("unsafe archive accepted", names)
		}
		entries, err := os.ReadDir(r.Config.DataDir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("partial restore left data: %v %v", entries, err)
		}
	}
}

func TestRestoreSettingsReloadUsesBaseAndEnvironmentSmoke(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"AI_AGENT_API_TOKEN", "AI_AGENT_LLM_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(name, "")
	}
	t.Setenv("AI_AGENT_DATA_DIR", root)
	t.Setenv("AI_AGENT_MAX_TOKENS", "131")
	base := DefaultConfig()
	base.DataDir = root
	base.NotificationsEnabled = true
	config := base
	config.settingsBase = &base
	config.NotificationsEnabled = false // 模擬上一份管理設定覆寫。
	if err := os.WriteFile(filepath.Join(root, serviceSettingsFilename), []byte(`{"max_tokens":53}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateRestoredSettings(root); err != nil {
		t.Fatal(err)
	}
	reloaded, err := reloadRestoredSettings(config)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.NotificationsEnabled || reloaded.MaxTokens != 131 {
		t.Fatalf("base/environment precedence lost: notifications=%v tokens=%d", reloaded.NotificationsEnabled, reloaded.MaxTokens)
	}
}

func TestRestoreRecoversInterruptedDirectorySwapSmoke(t *testing.T) {
	root := t.TempDir()
	r := &Runtime{Config: Config{DataDir: root}}
	if err := os.Mkdir(filepath.Join(root, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sessions", "old.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Restore(context.Background(), restoreTestArchive(t, "sessions/new.json")); err != nil {
		t.Fatal(err)
	}
	stages, _ := filepath.Glob(filepath.Join(root, ".restore-stage-*"))
	stage := stages[0]
	if err := os.Mkdir(filepath.Join(stage, "rollback"), 0700); err != nil {
		t.Fatal(err)
	}
	tx := restoreTransaction{Stage: filepath.Base(stage), Phase: "applying", Roots: []string{"sessions"}, Original: map[string]bool{"sessions": true}}
	if err := saveRestoreJournal(root, tx); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "sessions"), filepath.Join(stage, "rollback", "sessions")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(stage, "incoming", "sessions"), filepath.Join(root, "sessions")); err != nil {
		t.Fatal(err)
	}
	if _, err := applyPendingRestore(root); err == nil {
		t.Fatal("interrupted restore should stop startup after rollback")
	}
	if _, err := os.Stat(filepath.Join(root, "sessions", "old.json")); err != nil {
		t.Fatal("rollback failed", err)
	}
	if applied, err := applyPendingRestore(root); err != nil || !applied {
		t.Fatalf("retry: %v %v", applied, err)
	}
}
