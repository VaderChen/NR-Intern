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

// ToolContractResolver 在核准前解析當下契約；Execute 必須再次比對 ExpectedContractID。
type ToolContractResolver interface {
	ResolveDefinition(context.Context, domain.Session, string) (domain.ToolDefinition, error)
}
