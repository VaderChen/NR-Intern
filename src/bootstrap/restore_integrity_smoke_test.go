package bootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreMissingDataDirectorySmoke(t *testing.T) {
	if applied, err := applyPendingRestore(filepath.Join(t.TempDir(), "new-data")); err != nil || applied {
		t.Fatalf("首次啟動不應需要已有資料目錄：%v %v", applied, err)
	}
}

func TestRestoreCommittedValidatesBeforeRemovingJournalSmoke(t *testing.T) {
	for _, scenario := range []string{"complete", "missing-live", "pending-incoming"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			tx := restoreTransaction{Stage: ".restore-stage-smoke", Phase: "committed", Roots: []string{"sessions"}, Original: map[string]bool{"sessions": false}}
			if err := os.MkdirAll(filepath.Join(root, tx.Stage, "incoming"), 0700); err != nil {
				t.Fatal(err)
			}
			if scenario != "missing-live" {
				if err := os.Mkdir(filepath.Join(root, "sessions"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "pending-incoming" {
				if err := os.Mkdir(filepath.Join(root, tx.Stage, "incoming", "sessions"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := saveRestoreJournal(root, tx); err != nil {
				t.Fatal(err)
			}
			applied, err := applyPendingRestore(root)
			if scenario == "complete" {
				if err != nil || !applied {
					t.Fatalf("%v %v", applied, err)
				}
				if _, err := os.Stat(filepath.Join(root, restoreJournalName)); !os.IsNotExist(err) {
					t.Fatal("完成交易未清除", err)
				}
			} else {
				if err == nil || applied {
					t.Fatal("矛盾的提交狀態被接受")
				}
				if _, err := os.Stat(filepath.Join(root, restoreJournalName)); err != nil {
					t.Fatal("救援紀錄遺失", err)
				}
			}
		})
	}
}

func TestRestoreRollbackPreflightsAllRootsSmoke(t *testing.T) {
	root := t.TempDir()
	tx := restoreTransaction{Stage: ".restore-stage-smoke", Phase: "applying", Roots: []string{serviceSettingsFilename, "sessions"}, Original: map[string]bool{serviceSettingsFilename: true, "sessions": false}}
	// sessions 可回滾，但前面的 settings 缺件；必須在任何移動前拒絕整筆交易。
	for _, dir := range []string{"sessions", tx.Stage + "/incoming", tx.Stage + "/rollback"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := rollbackRestore(root, tx); err == nil {
		t.Fatal("缺件交易被接受")
	}
	if _, err := os.Stat(filepath.Join(root, "sessions")); err != nil {
		t.Fatal("驗證前就移動資料", err)
	}
}

func TestRestorePreparedRejectsSymlinkAndStaleRollbackSmoke(t *testing.T) {
	for _, scenario := range []string{"incoming", "backups", "rollback"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			r := &Runtime{Config: Config{DataDir: root}}
			if _, err := r.Restore(context.Background(), restoreTestArchive(t, "sessions/new.json")); err != nil {
				t.Fatal(err)
			}
			stages, err := filepath.Glob(filepath.Join(root, ".restore-stage-*"))
			if err != nil || len(stages) != 1 {
				t.Fatal("缺少 staging", err)
			}
			stage := stages[0]
			switch scenario {
			case "incoming":
				p := filepath.Join(stage, "incoming", "sessions")
				if err := os.Rename(p, filepath.Join(stage, "saved-sessions")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), p); err != nil {
					t.Skipf("symlink 不支援：%v", err)
				}
			case "backups":
				if err := os.Symlink(t.TempDir(), filepath.Join(root, "backups")); err != nil {
					t.Skipf("symlink 不支援：%v", err)
				}
			case "rollback":
				if err := os.MkdirAll(filepath.Join(stage, "rollback", "sessions"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if applied, err := applyPendingRestore(root); err == nil || applied {
				t.Fatal("不安全準備狀態被接受")
			}
			if _, err := os.Lstat(filepath.Join(root, "sessions")); !os.IsNotExist(err) {
				t.Fatal("現行資料被更動", err)
			}
			if _, err := os.Stat(filepath.Join(root, restoreJournalName)); err != nil {
				t.Fatal("交易指標遺失", err)
			}
		})
	}
}

// 列舉所有存在狀態，包含前向交換與回滾自身中斷；不碰真實使用者資料。
func TestRestoreRollbackStateMatrixSmoke(t *testing.T) {
	for _, name := range []string{"sessions", serviceSettingsFilename} {
		for _, hadOriginal := range []bool{false, true} {
			for mask := 0; mask < 8; mask++ {
				t.Run(fmt.Sprintf("%s/original=%v/state=%d", name, hadOriginal, mask), func(t *testing.T) {
					root := t.TempDir()
					tx := restoreTransaction{Stage: ".restore-stage-smoke", Phase: "applying", Roots: []string{name}, Original: map[string]bool{name: hadOriginal}}
					for _, dir := range []string{"incoming", "rollback"} {
						if err := os.MkdirAll(filepath.Join(root, tx.Stage, dir), 0700); err != nil {
							t.Fatal(err)
						}
					}
					paths := []string{filepath.Join(root, name), filepath.Join(root, tx.Stage, "rollback", name), filepath.Join(root, tx.Stage, "incoming", name)}
					payload := func(p string) string {
						if name == "sessions" {
							return filepath.Join(p, "marker")
						}
						return p
					}
					for i, p := range paths {
						if mask&(1<<i) == 0 {
							continue
						}
						if name == "sessions" {
							if err := os.Mkdir(p, 0700); err != nil {
								t.Fatal(err)
							}
						}
						if err := os.WriteFile(payload(p), []byte(fmt.Sprint(i)), 0600); err != nil {
							t.Fatal(err)
						}
					}
					valid := (!hadOriginal && (mask == 1 || mask == 4)) || (hadOriginal && (mask == 3 || mask == 5 || mask == 6))
					err := rollbackRestore(root, tx)
					if (err == nil) != valid {
						t.Fatalf("valid=%v err=%v", valid, err)
					}
					if !valid {
						for i, p := range paths {
							data, err := os.ReadFile(payload(p))
							if mask&(1<<i) != 0 {
								if err != nil || string(data) != fmt.Sprint(i) {
									t.Fatalf("資料被改動：%s %v", p, err)
								}
							} else if !os.IsNotExist(err) {
								t.Fatalf("非預期新增：%s %v", p, err)
							}
						}
						return
					}
					if err := rollbackRestore(root, tx); err != nil {
						t.Fatalf("回滾不可重入：%v", err)
					}
					if exists, _ := restoreNodeExists(paths[0], name == "sessions"); exists != hadOriginal {
						t.Fatal("原始存在狀態未恢復")
					}
					if _, err := os.Stat(payload(paths[2])); err != nil {
						t.Fatal("待還原資料遺失", err)
					}
				})
			}
		}
	}
}

func TestRestoreRejectsIncompleteJournalSmoke(t *testing.T) {
	for _, phase := range []string{"applying", "committed", "unknown"} {
		root := t.TempDir()
		tx := restoreTransaction{Stage: ".restore-stage-smoke", Phase: phase, Roots: []string{"sessions"}}
		if err := saveRestoreJournal(root, tx); err != nil {
			t.Fatal(err)
		}
		if applied, err := applyPendingRestore(root); err == nil || applied {
			t.Fatalf("不完整交易被接受：%s", phase)
		}
		if _, err := os.Stat(filepath.Join(root, restoreJournalName)); err != nil {
			t.Fatal("交易紀錄被移除", err)
		}
	}
}

func TestRestoreRejectsUnsafeNodesSmoke(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "synthetic.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{restoreJournalName, serviceSettingsFilename, "backups"} {
		p := filepath.Join(root, name)
		if err := os.Symlink(outside, p); err != nil {
			t.Skipf("無法建立 symlink：%v", err)
		}
		if _, err := restoreNodeExists(p, name == "backups"); err == nil {
			t.Fatal("symlink 被接受")
		}
	}
	if _, err := applyPendingRestore(root); err == nil {
		t.Fatal("symlink journal 被讀取")
	}
	if err := validateRestoredSettings(root); err == nil {
		t.Fatal("symlink settings 被讀取")
	}
}
