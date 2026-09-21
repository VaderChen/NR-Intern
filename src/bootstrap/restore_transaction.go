package bootstrap

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const restoreJournalName = ".pending-restore.json"

type restoreTransaction struct {
	Stage    string          `json:"stage"`
	Phase    string          `json:"phase"`
	Roots    []string        `json:"roots"`
	Original map[string]bool `json:"original,omitempty"`
}

// Restore 只準備還原，不干擾運行中的 Run、repository 或背景維護。
func (r *Runtime) Restore(ctx context.Context, data []byte) (domain.RestoreResult, error) {
	r.restoreMu.Lock()
	defer r.restoreMu.Unlock()
	r.configMu.RLock()
	root := r.Config.DataDir
	r.configMu.RUnlock()
	if strings.TrimSpace(root) == "" {
		return domain.RestoreResult{}, fmt.Errorf("%w: data directory unavailable", domain.ErrConflict)
	}
	if _, err := os.Lstat(filepath.Join(root, restoreJournalName)); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return domain.RestoreResult{}, err
		}
		return domain.RestoreResult{}, fmt.Errorf("%w: 已有待套用的還原，請先重新啟動後端", domain.ErrConflict)
	}
	stage, err := os.MkdirTemp(root, ".restore-stage-")
	if err != nil {
		return domain.RestoreResult{}, err
	}
	prepared := false
	defer func() {
		if !prepared {
			_ = os.RemoveAll(stage)
		}
	}()
	incoming := filepath.Join(stage, "incoming")
	if err := os.Mkdir(incoming, 0o700); err != nil {
		return domain.RestoreResult{}, err
	}
	result, err := extractRestoreArchive(ctx, data, incoming)
	if err != nil {
		return domain.RestoreResult{}, err
	}
	if err := validateRestoredSettings(incoming); err != nil {
		return domain.RestoreResult{}, err
	}
	roots := map[string]bool{}
	for _, entry := range result.Restored {
		roots[strings.SplitN(entry, "/", 2)[0]] = true
	}
	if len(roots) == 0 {
		return domain.RestoreResult{}, fmt.Errorf("%w: backup contains no restorable data", domain.ErrInvalidInput)
	}
	tx := restoreTransaction{Stage: filepath.Base(stage), Phase: "prepared"}
	for name := range roots {
		tx.Roots = append(tx.Roots, name)
	}
	sort.Strings(tx.Roots)
	if err := ctx.Err(); err != nil {
		return domain.RestoreResult{}, err
	}
	if err := saveRestoreJournal(root, tx); err != nil {
		return domain.RestoreResult{}, err
	}
	prepared = true
	result.Pending = true
	return result, nil
}

// applyPendingRestore 只能由啟動流程呼叫。交易中斷時先回滾；不以混合資料開機。
func applyPendingRestore(root string) (bool, error) {
	if strings.TrimSpace(root) == "" {
		return false, nil
	}
	if exists, err := restoreNodeExists(root, true); err != nil || !exists {
		return false, err
	}
	if err := ensureSafeParents(root, restoreJournalName); err != nil {
		return false, err
	}
	journal := filepath.Join(root, restoreJournalName)
	if exists, err := restoreNodeExists(journal, false); err != nil || !exists {
		return false, err
	}
	data, err := os.ReadFile(journal)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var tx restoreTransaction
	if err := json.Unmarshal(data, &tx); err != nil {
		return false, fmt.Errorf("read restore transaction: %w", err)
	}
	if err := validateRestoreTransaction(root, tx); err != nil {
		return false, err
	}
	stage := filepath.Join(root, tx.Stage)
	if tx.Phase == "applying" {
		if err := rollbackRestore(root, tx); err != nil {
			return false, fmt.Errorf("restore rollback: %w", err)
		}
		tx.Phase = "prepared"
		if err := saveRestoreJournal(root, tx); err != nil {
			return false, err
		}
		return false, fmt.Errorf("偵測到中斷的還原，已回復原始資料；請再次啟動以重新套用")
	}
	if tx.Phase == "committed" {
		for _, name := range tx.Roots {
			if exists, err := restoreNodeExists(filepath.Join(root, name), name != serviceSettingsFilename); err != nil {
				return false, err
			} else if !exists {
				return false, fmt.Errorf("已提交的還原缺少現行資料 %q", name)
			}
			if exists, err := restoreNodeExists(filepath.Join(stage, "incoming", name), name != serviceSettingsFilename); err != nil {
				return false, err
			} else if exists {
				return false, fmt.Errorf("已提交的還原仍含有待套用資料 %q", name)
			}
		}
		// 舊資料仍保存在專屬 staging/rollback，供人工救援；只移除交易指標。
		return true, os.Remove(journal)
	}
	if tx.Phase != "prepared" {
		return false, fmt.Errorf("unknown restore transaction phase")
	}
	rollback := filepath.Join(stage, "rollback")
	if err := os.MkdirAll(rollback, 0o700); err != nil {
		return false, err
	}
	tx.Original = map[string]bool{}
	for _, name := range tx.Roots {
		if _, err := restoreNodeExists(filepath.Join(root, name), name != serviceSettingsFilename); err != nil {
			return false, err
		}
		if exists, err := restoreNodeExists(filepath.Join(stage, "incoming", name), name != serviceSettingsFilename); err != nil {
			return false, err
		} else if !exists {
			return false, fmt.Errorf("還原來源缺少 %q", name)
		}
		if exists, err := restoreNodeExists(filepath.Join(rollback, name), name != serviceSettingsFilename); err != nil {
			return false, err
		} else if exists {
			return false, fmt.Errorf("尚有未處理的還原回滾資料 %q", name)
		}
		_, err := os.Lstat(filepath.Join(root, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		tx.Original[name] = err == nil
	}
	if err := validateRestoredSettings(filepath.Join(stage, "incoming")); err != nil {
		return false, err
	}
	// 此時尚未建立任何 repository，快照不會與 Run 寫入競爭，且不包含 credentials。
	backup, err := (&Runtime{Config: Config{DataDir: root}}).Backup(context.Background())
	if err != nil {
		return false, err
	}
	backupDir := filepath.Join(root, "backups")
	if _, err := restoreNodeExists(backupDir, true); err != nil {
		return false, err
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return false, err
	}
	if err := writeRestoreFile(filepath.Join(backupDir, "pre-restore-"+domain.NewID("snapshot")+".zip"), backup); err != nil {
		return false, err
	}
	tx.Phase = "applying"
	if err := saveRestoreJournal(root, tx); err != nil {
		return false, err
	}
	for _, name := range tx.Roots {
		target := filepath.Join(root, name)
		if tx.Original[name] {
			err = os.Rename(target, filepath.Join(rollback, name))
		}
		if err == nil {
			err = os.Rename(filepath.Join(stage, "incoming", name), target)
		}
		if err != nil {
			break
		}
	}
	if err != nil {
		rollbackErr := rollbackRestore(root, tx)
		if rollbackErr == nil {
			tx.Phase = "prepared"
			rollbackErr = saveRestoreJournal(root, tx)
		}
		return false, errors.Join(err, rollbackErr)
	}
	tx.Phase = "committed"
	if err := saveRestoreJournal(root, tx); err != nil {
		return false, err
	}
	return true, os.Remove(journal)
}

func validateRestoredSettings(incoming string) error {
	if _, err := restoreNodeExists(filepath.Join(incoming, serviceSettingsFilename), false); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(incoming, serviceSettingsFilename))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !json.Valid(data) {
		return fmt.Errorf("%w: invalid restored service settings JSON", domain.ErrInvalidInput)
	}
	// 只驗證待還原的非秘密設定，不載入使用者的 Provider、OAuth 或 MCP 憑證。
	config := DefaultConfig()
	config.DataDir = incoming
	if err := loadPersistedServiceSettings(&config); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	}
	if err := validateConfig(&config); err != nil {
		return fmt.Errorf("%w: restored service settings: %v", domain.ErrInvalidInput, err)
	}
	return nil
}

func reloadRestoredSettings(config Config) (Config, error) {
	if base := config.settingsBase; base != nil {
		config = *base
		config.settingsBase = base
		if err := loadPersistedProviderSettings(&config); err != nil {
			return Config{}, err
		}
		if err := loadPersistedMCPSettings(&config); err != nil {
			return Config{}, err
		}
	}
	if err := loadPersistedServiceSettings(&config); err != nil {
		return Config{}, err
	}
	// 維持 LoadConfig 的優先序；還原不能蓋掉部署環境的限制。
	applyEnvironment(&config)
	return config, nil
}

func validateRestoreTransaction(root string, tx restoreTransaction) error {
	if !strings.HasPrefix(tx.Stage, ".restore-stage-") || strings.ContainsAny(tx.Stage, `/\`) || filepath.Base(tx.Stage) != tx.Stage || len(tx.Roots) == 0 {
		return fmt.Errorf("invalid restore transaction")
	}
	if tx.Phase != "prepared" && tx.Phase != "applying" && tx.Phase != "committed" {
		return fmt.Errorf("unknown restore transaction phase")
	}
	allowed := map[string]bool{"service-settings.json": true}
	for _, name := range backupDirectories {
		allowed[name] = true
	}
	seen := map[string]bool{}
	for _, name := range tx.Roots {
		if !allowed[name] || seen[name] {
			return fmt.Errorf("invalid restore root %q", name)
		}
		seen[name] = true
		if _, ok := tx.Original[name]; !ok && tx.Phase != "prepared" {
			return fmt.Errorf("還原交易缺少原始狀態 %q", name)
		}
	}
	for name := range tx.Original {
		if !seen[name] {
			return fmt.Errorf("還原交易含有未知原始項目 %q", name)
		}
	}
	for _, name := range []string{tx.Stage, tx.Stage + "/incoming", tx.Stage + "/rollback"} {
		if err := ensureSafeParents(root, name+"/entry"); err != nil {
			return err
		}
	}
	return nil
}

func rollbackRestore(root string, tx restoreTransaction) error {
	if err := validateRestoreTransaction(root, tx); err != nil {
		return err
	}
	// 先驗證整筆交易，再開始回搬；矛盾狀態不得靠 Rename 覆蓋來猜測。
	type state struct{ live, old, incoming bool }
	states := make(map[string]state, len(tx.Roots))
	for _, name := range tx.Roots {
		original, recorded := tx.Original[name]
		if !recorded {
			return fmt.Errorf("還原交易缺少原始狀態 %q", name)
		}
		var s state
		for _, node := range []struct {
			path   string
			exists *bool
		}{
			{filepath.Join(root, name), &s.live},
			{filepath.Join(root, tx.Stage, "rollback", name), &s.old},
			{filepath.Join(root, tx.Stage, "incoming", name), &s.incoming},
		} {
			exists, err := restoreNodeExists(node.path, name != serviceSettingsFilename)
			if err != nil {
				return err
			}
			*node.exists = exists
		}
		valid := !s.old && s.live != s.incoming
		if original {
			valid = (s.old && s.live != s.incoming) || (!s.old && s.live && s.incoming)
		}
		if !valid {
			return fmt.Errorf("還原狀態矛盾，已保留所有資料：%q", name)
		}
		states[name] = s
	}
	for i := len(tx.Roots) - 1; i >= 0; i-- {
		name := tx.Roots[i]
		target := filepath.Join(root, name)
		original := filepath.Join(root, tx.Stage, "rollback", name)
		incoming := filepath.Join(root, tx.Stage, "incoming", name)
		s := states[name]
		if s.old || !tx.Original[name] {
			if !s.incoming {
				if err := os.Rename(target, incoming); err != nil {
					return err
				}
			}
			if s.old {
				if err := os.Rename(original, target); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// 不開啟未知節點，避免跟隨 symlink 或阻塞在 FIFO 等特殊檔案。
func restoreNodeExists(name string, directory bool) (bool, error) {
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return false, fmt.Errorf("還原項目型別不安全：%q", filepath.Base(name))
	}
	return true, nil
}

func saveRestoreJournal(root string, tx restoreTransaction) error {
	data, err := json.Marshal(tx)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(root, ".restore-journal-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	err = errors.Join(err, tmp.Close())
	if err != nil {
		return err
	}
	return replaceConfigFile(tmp.Name(), filepath.Join(root, restoreJournalName))
}

func writeRestoreFile(target string, data []byte) error {
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}
