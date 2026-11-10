package application

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"errors"
	"fmt"
	"strings"
)

// ApplyProviderSettings 在切換設定前遷移被刪除 Provider 的對話。
// 與建立／修改 Session、啟動 Run 共用鎖，避免檢查後又產生舊參照。
// commit 不得回呼會取得 startMu 的操作；失敗時復原已遷移的選擇欄位。
func (s *Service) ApplyProviderSettings(ctx context.Context, enabled map[string]bool, fallback domain.ProviderDescriptor, commit func() error) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !enabled[fallback.ID] || strings.TrimSpace(fallback.DefaultModel) == "" || commit == nil {
		return fmt.Errorf("%w: enabled default provider and model are required", domain.ErrInvalidInput)
	}
	type migration struct {
		engine  ports.AgentEngine
		session domain.Session
	}
	var migrations []migration
	for _, engine := range s.registry.Engines() {
		sessions, err := engine.ListSessions(ctx)
		if err != nil {
			return err
		}
		for _, session := range sessions {
			id := strings.TrimSpace(session.ProviderID)
			if id == "" {
				continue
			}
			isEnabled, exists := enabled[id]
			if exists {
				if !isEnabled {
					return fmt.Errorf("%w: provider %q must remain enabled while used by session %q", domain.ErrConflict, id, session.ID)
				}
				continue
			}
			if s.hasActiveSession(session.ID) {
				return fmt.Errorf("%w: 對話 %q 正在執行或等待工作，請待工作結束後再刪除 Provider", domain.ErrConflict, session.Title)
			}
			migrations = append(migrations, migration{engine, session})
		}
	}
	// Run 可覆寫 Session 的 Provider；也必須保護這種正在使用的參照。
	s.mu.Lock()
	activeIDs := make([]string, 0, len(s.active))
	for id := range s.active {
		activeIDs = append(activeIDs, id)
	}
	s.mu.Unlock()
	for _, id := range activeIDs {
		run, err := s.runs.Get(ctx, id)
		if err != nil {
			return err
		}
		if run.ProviderID != "" && !enabled[run.ProviderID] {
			return fmt.Errorf("%w: Provider %q 仍有執行中的工作，請待工作結束後再刪除或停用", domain.ErrConflict, run.ProviderID)
		}
	}
	rollback := func(cause error, changed int) error {
		for i := changed - 1; i >= 0; i-- {
			item := migrations[i]
			old := item.session
			_, err := item.engine.UpdateSession(context.WithoutCancel(ctx), old.ID, domain.UpdateSessionInput{ProviderID: &old.ProviderID, Model: &old.Model, ThinkingMode: &old.ThinkingMode})
			if err != nil {
				cause = errors.Join(cause, fmt.Errorf("restore session %q: %w", old.ID, err))
			}
		}
		return cause
	}
	thinking := ""
	for i, item := range migrations {
		if err := ctx.Err(); err != nil {
			return rollback(err, i)
		}
		_, err := item.engine.UpdateSession(ctx, item.session.ID, domain.UpdateSessionInput{ProviderID: &fallback.ID, Model: &fallback.DefaultModel, ThinkingMode: &thinking})
		if err != nil {
			return rollback(err, i+1)
		}
	}
	if err := ctx.Err(); err != nil {
		return rollback(err, len(migrations))
	}
	if err := commit(); err != nil {
		return rollback(err, len(migrations))
	}
	return nil
}
