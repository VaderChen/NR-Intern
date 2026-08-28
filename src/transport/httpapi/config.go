package httpapi

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
)

type Config struct {
	APIToken               string
	AllowedOrigins         []string
	MaxBodyBytes           int64
	Attachments            ports.AttachmentRepository
	MaxAttachmentBytes     int64
	Status                 func() domain.ServiceStatus
	ToolCatalog            func(context.Context, string) ([]domain.ToolCatalogEntry, error)
	Diagnostics            func(context.Context) (any, error)
	ServiceSettings        func(context.Context) (domain.ServiceSettings, error)
	UpdateServiceSettings  func(context.Context, domain.UpdateServiceSettingsInput) (domain.ServiceSettings, error)
	ProviderSettings       func(context.Context) (domain.ProviderSettings, error)
	UpdateProviderSettings func(context.Context, domain.UpdateProviderSettingsInput) (domain.ProviderSettings, error)
	ProviderModels         func(context.Context, string) (domain.ProviderModels, error)
	TestProvider           func(context.Context, string) (domain.ProviderTestResult, error)
}
