package ports

import (
	"AgenticService/src/domain"
	"context"
)

// ApprovalCoordinator 把 Harness 的等待與 HTTP 決策解耦。Begin 必須先於
// approval-required 事件，避免前端極快回覆時決策早於 waiter 註冊而遺失。
type ApprovalCoordinator interface {
	Required(toolName string) bool
	Begin(domain.ToolApprovalRequest) error
	Wait(context.Context, string) (domain.ToolApprovalDecision, error)
	Decide(string, domain.ToolApprovalDecisionInput) error
	Cancel(string)
}
