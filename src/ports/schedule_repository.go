package ports

import (
	"AgenticService/src/domain"
	"context"
	"time"
)

// ScheduleRepository 保存 Workspace 層級的排程。排程是獨立實體，刪除 Project 或
// Session 都不會影響它；沙箱路徑在寫入前就要驗證，執行器只信任這裡讀出的值。
type ScheduleRepository interface {
	Create(context.Context, domain.CreateScheduleInput) (domain.Schedule, error)
	List(context.Context) ([]domain.Schedule, error)
	Get(context.Context, string) (domain.Schedule, error)
	Update(context.Context, string, domain.UpdateScheduleInput) (domain.Schedule, error)
	Delete(context.Context, string) error
	// MarkTriggered 只更新最近一次執行結果與下一次時間，不覆寫使用者的設定欄位，
	// 避免執行器與正在編輯排程的使用者互相蓋寫。
	MarkTriggered(context.Context, string, domain.ScheduleRunState) (domain.Schedule, error)
	// Reschedule 只改寫下一次執行時間，用在後端重新啟動後重新對齊時間軸，
	// 不會動到最近一次執行結果。
	Reschedule(context.Context, string, *time.Time) (domain.Schedule, error)
}
