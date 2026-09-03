package ports

import (
	"AgenticService/src/domain"
	"context"
)

type MemoryRepository interface {
	Remember(context.Context, domain.RememberMemoryInput) (domain.Memory, error)
	Get(context.Context, string, string) (domain.Memory, error)
	Search(context.Context, domain.MemoryQuery) ([]domain.Memory, error)
	Forget(context.Context, string, string, string) (domain.Memory, error)
	// ListScope 回傳單一 scope 內所有仍生效的記憶。
	//
	// 與 Search 分開是因為用途不同：Search 是相關度排序後的前幾筆，去重與淘汰
	// 要看的卻是整個 scope 的全貌，不能被搜尋的筆數上限截斷。
	ListScope(context.Context, string) ([]domain.Memory, error)
}
