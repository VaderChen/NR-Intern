package bootstrap

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// purgeEphemeralProjectSessions 在 Repository 對外服務前清除上次程序留下的隔離對話。
// Project 本身刻意保留，使用者重啟後仍能以同一組設定開始新的揮發性工作。
func purgeEphemeralProjectSessions(
	ctx context.Context,
	projects []domain.Project,
	sessions *filestore.SessionRepository,
	plans *filestore.PlanRepository,
	runs *filestore.RunRepository,
	events *filestore.RunEventRepository,
	notifications *filestore.NotificationRepository,
	logger *slog.Logger,
) error {
	// 先掃掉 dataDir 裡的殘留：舊版本在 RAM disk 未掛載時會把隔離對話寫到這裡，
	// 而現在的根目錄解析會直接失敗，所以 List 看不到它們，得另外清。
	if residue, err := sessions.PurgeVolatileResidue(); err != nil {
		return fmt.Errorf("purge memory-isolated session residue: %w", err)
	} else if residue > 0 {
		logger.Info("cleared memory-isolated session residue from data directory", "session_count", residue)
	}
	ephemeralProjects := make(map[string]struct{})
	for _, project := range projects {
		if project.Ephemeral {
			ephemeralProjects[project.ID] = struct{}{}
		}
	}
	if len(ephemeralProjects) == 0 {
		return nil
	}
	storedSessions, err := sessions.List(ctx, "")
	if err != nil {
		return fmt.Errorf("list memory-isolated sessions: %w", err)
	}
	removed := 0
	for _, session := range storedSessions {
		if _, exists := ephemeralProjects[session.ProjectID]; !exists {
			continue
		}
		sessionRuns, err := runs.List(ctx, session.ID)
		if err != nil {
			return fmt.Errorf("list memory-isolated session runs: %w", err)
		}
		// 清理對「本來就不在」要寬容：RAM disk 未掛載時根目錄解析回 ErrNotFound，
		// 那代表這份資料已經隨磁碟消失，正是清理想要的結果，不該讓啟動失敗。
		for _, run := range sessionRuns {
			if err := events.Delete(run.ID); err != nil && !errors.Is(err, domain.ErrNotFound) {
				return fmt.Errorf("delete memory-isolated run events: %w", err)
			}
		}
		if _, err := runs.DeleteSession(ctx, session.ID); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("delete memory-isolated session runs: %w", err)
		}
		if err := notifications.DeleteSession(ctx, session.ID); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("delete memory-isolated session notifications: %w", err)
		}
		if err := plans.DeleteSession(ctx, session.ID); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("delete memory-isolated session plans: %w", err)
		}
		// Session 目錄包含 transcript、附件與私有工作目錄，最後刪除可讓前面的失敗在下次啟動重試。
		if err := sessions.Delete(ctx, session.ID); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("delete memory-isolated session: %w", err)
		}
		removed++
	}
	if removed > 0 {
		logger.Info("cleared memory-isolated project conversations", "session_count", removed)
	}
	return nil
}
