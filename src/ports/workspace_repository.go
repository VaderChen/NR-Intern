package ports

import (
	"AgenticService/src/domain"
	"context"
)

type WorkspaceRepository interface {
	Create(context.Context, domain.CreateWorkspaceInput) (domain.Workspace, error)
	List(context.Context) ([]domain.Workspace, error)
	Get(context.Context, string) (domain.Workspace, error)
	Update(context.Context, string, domain.UpdateWorkspaceInput) (domain.Workspace, error)
	Delete(context.Context, string) error
}
