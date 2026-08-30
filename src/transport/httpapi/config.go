package httpapi

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
)

type Config struct {
	APIToken                string
	AllowedOrigins          []string
	MaxBodyBytes            int64
	Attachments             ports.AttachmentRepository
	MaxAttachmentBytes      int64
	Status                  func() domain.ServiceStatus
	ToolCatalog             func(context.Context, string) ([]domain.ToolCatalogEntry, error)
	Diagnostics             func(context.Context) (any, error)
	DiagnosticsExport       func(context.Context) ([]byte, error)
	Backup                  func(context.Context) ([]byte, error)
	Restore                 func(context.Context, []byte) (domain.RestoreResult, error)
	Permissions             func(context.Context) (domain.PermissionCenter, error)
	UpdateStatus            func(context.Context) (domain.UpdateStatus, error)
	CheckForUpdates         func(context.Context) (domain.UpdateStatus, error)
	ServiceSettings         func(context.Context) (domain.ServiceSettings, error)
	UpdateServiceSettings   func(context.Context, domain.UpdateServiceSettingsInput) (domain.ServiceSettings, error)
	ProviderSettings        func(context.Context) (domain.ProviderSettings, error)
	UpdateProviderSettings  func(context.Context, domain.UpdateProviderSettingsInput) (domain.ProviderSettings, error)
	MCPSettings             func(context.Context) (domain.MCPSettings, error)
	UpdateMCPSettings       func(context.Context, domain.UpdateMCPSettingsInput) (domain.MCPSettings, error)
	TestMCP                 func(context.Context, string) (domain.MCPTestResult, error)
	ReverseProxyStatus      func(context.Context) (domain.ReverseProxyStatus, error)
	UpdateReverseProxy      func(context.Context, domain.UpdateReverseProxyInput) (domain.ReverseProxyStatus, error)
	StartReverseProxy       func(context.Context, domain.StartReverseProxyInput) (domain.ReverseProxyStatus, error)
	StopReverseProxy        func(context.Context) (domain.ReverseProxyStatus, error)
	ProviderModels          func(context.Context, string) (domain.ProviderModels, error)
	ProviderUsage           func(context.Context, string) (domain.ProviderUsage, error)
	TestProvider            func(context.Context, string) (domain.ProviderTestResult, error)
	StartProviderOAuth      func(context.Context, string) (domain.ProviderOAuthStartResult, error)
	ProviderOAuthStatus     func(context.Context, string) (domain.ProviderOAuthStatus, error)
	DisconnectProviderOAuth func(context.Context, string) error
}
