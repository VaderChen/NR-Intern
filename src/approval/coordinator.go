package approval

import (
	"AgenticService/src/domain"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type pendingApproval struct {
	request  domain.ToolApprovalRequest
	decision chan domain.ToolApprovalDecision
}

// Coordinator 是 process 內的等待協調器；Run 本身與 pending approval 另由
// application.Service 寫入 durable repository，這裡只持有不可序列化的等待 channel。
type Coordinator struct {
	mu       sync.Mutex
	required map[string]bool
	pending  map[string]*pendingApproval
	byRun    map[string]string
}

func NewCoordinator(requiredTools []string) *Coordinator {
	required := make(map[string]bool, len(requiredTools))
	for _, name := range requiredTools {
		if name = strings.TrimSpace(name); name != "" {
			required[name] = true
		}
	}
	return &Coordinator{
		required: required,
		pending:  map[string]*pendingApproval{},
		byRun:    map[string]string{},
	}
}

func (c *Coordinator) Required(toolName string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	toolName = strings.TrimSpace(toolName)
	if c.required[toolName] {
		return true
	}
	for pattern := range c.required {
		if strings.HasSuffix(pattern, "*") && strings.HasPrefix(toolName, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}

func (c *Coordinator) Begin(request domain.ToolApprovalRequest) error {
	if c == nil {
		return fmt.Errorf("%w: approval coordinator is unavailable", domain.ErrConflict)
	}
	request.ID = strings.TrimSpace(request.ID)
	request.RunID = strings.TrimSpace(request.RunID)
	if request.ID == "" || request.RunID == "" || strings.TrimSpace(request.ToolCallID) == "" {
		return fmt.Errorf("%w: approval id, run id and tool call id are required", domain.ErrInvalidInput)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.pending[request.ID]; exists {
		return fmt.Errorf("%w: approval %q already exists", domain.ErrConflict, request.ID)
	}
	if _, exists := c.byRun[request.RunID]; exists {
		return fmt.Errorf("%w: run %q already has a pending approval", domain.ErrConflict, request.RunID)
	}
	c.pending[request.ID] = &pendingApproval{request: request, decision: make(chan domain.ToolApprovalDecision, 1)}
	c.byRun[request.RunID] = request.ID
	return nil
}

func (c *Coordinator) Wait(ctx context.Context, approvalID string) (domain.ToolApprovalDecision, error) {
	approvalID = strings.TrimSpace(approvalID)
	c.mu.Lock()
	pending := c.pending[approvalID]
	c.mu.Unlock()
	if pending == nil {
		return domain.ToolApprovalDecision{}, fmt.Errorf("%w: approval %q", domain.ErrNotFound, approvalID)
	}
	select {
	case <-ctx.Done():
		c.Cancel(approvalID)
		return domain.ToolApprovalDecision{}, ctx.Err()
	case decision := <-pending.decision:
		c.Cancel(approvalID)
		return decision, nil
	}
}

func (c *Coordinator) Decide(runID string, input domain.ToolApprovalDecisionInput) error {
	if c == nil {
		return fmt.Errorf("%w: approval coordinator is unavailable", domain.ErrConflict)
	}
	runID = strings.TrimSpace(runID)
	input.ApprovalID = strings.TrimSpace(input.ApprovalID)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Decision != domain.ToolApprovalApprove && input.Decision != domain.ToolApprovalDeny {
		return fmt.Errorf("%w: decision must be approve or deny", domain.ErrInvalidInput)
	}
	if input.Permanent && input.Decision != domain.ToolApprovalApprove {
		return fmt.Errorf("%w: permanent approval requires an approve decision", domain.ErrInvalidInput)
	}
	c.mu.Lock()
	approvalID := c.byRun[runID]
	pending := c.pending[approvalID]
	if approvalID == "" || pending == nil || approvalID != input.ApprovalID {
		c.mu.Unlock()
		return fmt.Errorf("%w: pending approval for run %q", domain.ErrNotFound, runID)
	}
	decision := domain.ToolApprovalDecision{
		ApprovalID: approvalID,
		RunID:      runID,
		Decision:   input.Decision,
		Reason:     input.Reason,
		Permanent:  input.Permanent,
		DecidedAt:  time.Now().UTC(),
	}
	select {
	case pending.decision <- decision:
		delete(c.byRun, runID)
	default:
		c.mu.Unlock()
		return fmt.Errorf("%w: approval %q already has a decision", domain.ErrConflict, approvalID)
	}
	c.mu.Unlock()
	return nil
}

func (c *Coordinator) Cancel(approvalID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	pending := c.pending[strings.TrimSpace(approvalID)]
	if pending != nil {
		delete(c.byRun, pending.request.RunID)
		delete(c.pending, pending.request.ID)
	}
	c.mu.Unlock()
}
