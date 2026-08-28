package ports

import (
	"AgenticService/src/domain"
	"context"
)

// ProjectRepository 保存 UI 與 API 共用的正式 Project 結構，不把分組資訊塞進 Session metadata。
type ProjectRepository interface {
	Create(context.Context, domain.CreateProjectInput) (domain.Project, error)
	List(context.Context) ([]domain.Project, error)
	Get(context.Context, string) (domain.Project, error)
	Update(context.Context, string, domain.UpdateProjectInput) (domain.Project, error)
	Delete(context.Context, string) error
}
