package bootstrap

import (
	"context"
	"log/slog"
	"time"

	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
)

const (
	// runEventRetention 是保留事件檔的 run 數量。
	//
	// 事件檔只有一個讀取路徑：重連進行中的 run 時用 Last-Event-ID 補回漏掉的事件。
	// Run 結束、transcript 寫完之後就沒有人再讀它，但單檔可以到好幾 MB。因此保留
	// 期比 run 紀錄短得多——留最近幾十筆已經遠超過任何重連情境需要的範圍。
	runEventRetention = 50
	// maintenanceInterval 是背景清理的間隔。
	//
	// 桌面程式可能連續開好幾天，只在啟動時清理等於長時間執行的那台機器永遠不清。
	maintenanceInterval = 30 * time.Minute
	// memoryAuditRetention 是失效記憶的保留期。
	//
	// forgotten 與 superseded 不會被召回，留著只為了稽核；記憶檔每次寫入整份重寫，
	// 所以留太久等於讓每次寫記憶都變慢。
	memoryAuditRetention = 30 * 24 * time.Hour
)

// startStorageMaintenance 定期把只增不減的儲存壓回上限。
//
// 這個 codebase 原本完全沒有保留期機制：run 紀錄、事件檔都只會長大。
// 前者讓每次寫入變慢（runs.json 每次 Save 整份重寫），後者純粹佔磁碟。
func (r *Runtime) startStorageMaintenance() {
	if r == nil || r.maintenanceCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.maintenanceContext = ctx
	r.maintenanceCancel = cancel
	go func() {
		r.sweepStorage(ctx)
		ticker := time.NewTicker(maintenanceInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.sweepStorage(ctx)
			}
		}
	}()
}

// sweepStorage 刪掉已經沒有讀取路徑的事件檔。
func (r *Runtime) sweepStorage(ctx context.Context) {
	runs, ok := r.Runs.(*filestore.RunRepository)
	events, eventsOK := r.Events.(*filestore.RunEventRepository)
	if !ok || !eventsOK {
		return
	}
	logger := slog.Default().With("service", r.Config.ServiceName)
	// 先處理 RunRepository 自己淘汰掉的 run：紀錄沒了，事件檔就沒有讀取路徑。
	for _, runID := range runs.TakePrunedRunIDs() {
		if err := events.Delete(runID); err != nil {
			logger.Warn("delete events of pruned run failed", "run_id", runID, "error", err)
		}
	}
	values, err := runs.List(ctx, "")
	if err != nil {
		logger.Warn("storage maintenance could not list runs", "error", err)
		return
	}
	// List 已依建立時間新到舊排序。保留最近 runEventRetention 筆，
	// 以及所有還沒結束的 run——那些正是可能被重連讀取的。
	keep := map[string]bool{}
	for index, run := range values {
		if index < runEventRetention || !finishedRun(run.Status) {
			keep[run.ID] = true
		}
	}
	stored, err := events.ListRunIDs()
	if err != nil {
		logger.Warn("storage maintenance could not list run events", "error", err)
		return
	}
	removed := 0
	for _, runID := range stored {
		if keep[runID] {
			continue
		}
		if err := events.Delete(runID); err != nil {
			logger.Warn("delete run events failed", "run_id", runID, "error", err)
			continue
		}
		removed++
	}
	if removed > 0 {
		logger.Info("run event files pruned", "removed", removed, "retained", len(keep))
	}
	r.purgeMemoryAuditTrail(ctx, logger)
}

// purgeMemoryAuditTrail 移除已過稽核保留期的失效記憶。
func (r *Runtime) purgeMemoryAuditTrail(ctx context.Context, logger *slog.Logger) {
	store, ok := r.Memory.(*filestore.MemoryRepository)
	if !ok {
		return
	}
	removed, err := store.PurgeInactive(ctx, time.Now().UTC().Add(-memoryAuditRetention))
	if err != nil {
		logger.Warn("purge inactive memories failed", "error", err)
		return
	}
	if removed > 0 {
		logger.Info("inactive memories purged", "removed", removed)
	}
}

// finishedRun 判斷 run 是否已經結束。未結束的 run 仍可能被重連讀取事件。
func finishedRun(status domain.RunStatus) bool {
	switch status {
	case domain.RunStatusCompleted, domain.RunStatusFailed, domain.RunStatusCanceled:
		return true
	default:
		return false
	}
}

// stopStorageMaintenance 停止背景清理。
func (r *Runtime) stopStorageMaintenance() {
	if r == nil || r.maintenanceCancel == nil {
		return
	}
	r.maintenanceCancel()
	r.maintenanceCancel = nil
	r.maintenanceContext = nil
}
