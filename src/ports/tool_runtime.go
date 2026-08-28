package ports

import (
	"AgenticService/src/domain"
	"context"
)

type ToolUpdateSink func(domain.ToolExecution) error

type ToolRuntime interface {
	Definitions(context.Context, domain.Session) ([]domain.ToolDefinition, error)
	Execute(context.Context, domain.Session, domain.ToolCall, ToolUpdateSink) (domain.ToolExecution, error)
}
