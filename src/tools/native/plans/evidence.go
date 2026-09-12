package plans

import (
	"AgenticService/src/domain"
	"AgenticService/src/tools"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (t *UpdateStepTool) verifyEvidence(ctx context.Context, invocation tools.Invocation, plan domain.Plan) ([]domain.PlanEvidence, error) {
	if t.Sessions == nil {
		return nil, fmt.Errorf("%w: 無法查證工具執行紀錄", domain.ErrInvalidInput)
	}
	var since *time.Time
	for _, step := range plan.Steps {
		if step.ID == stringArgument(invocation.Call.Arguments, "step_id") {
			since = step.VerificationStartedAt
		}
	}
	if since == nil {
		return nil, fmt.Errorf("%w: 請先進入 verifying 並執行客觀檢查；舊驗證狀態需先 blocked 再重新開始", domain.ErrInvalidInput)
	}
	data, err := json.Marshal(invocation.Call.Arguments["evidence_tool_call_ids"])
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil || len(ids) == 0 || len(ids) > 32 {
		return nil, fmt.Errorf("%w: evidence_tool_call_ids 必須包含 1 至 32 個實際工具 call ID", domain.ErrInvalidInput)
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" || wanted[id] {
			return nil, fmt.Errorf("%w: 證據 ID 不可空白或重複", domain.ErrInvalidInput)
		}
		wanted[id] = true
	}
	found := map[string]domain.PlanEvidence{}
	sequences := map[string]int64{}
	var lastMutation int64
	var cursor int64
	for {
		entries, more, err := t.Sessions.ListEntriesPage(ctx, invocation.Session.ID, cursor, 256)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			cursor = entry.Sequence
			message := entry.Message
			if message == nil || message.Role != "tool" || message.CreatedAt.Before(*since) || strings.HasPrefix(message.ToolName, "plan_") {
				continue
			}
			skipped, _ := message.Metadata["skipped"].(bool)
			readOnly, _ := message.Metadata["tool_read_only"].(bool)
			if !readOnly && !skipped && domain.ToolExecutionState(message.Metadata) != domain.ToolNotDispatched {
				lastMutation = entry.Sequence
			}
			if !wanted[message.ToolCallID] || message.IsError || domain.ToolExecutionState(message.Metadata) != domain.ToolSucceeded {
				continue
			}
			if skipped {
				continue
			}
			digest := sha256.Sum256([]byte(message.Content))
			found[message.ToolCallID] = domain.PlanEvidence{ToolCallID: message.ToolCallID, ToolName: message.ToolName, ResultSHA256: hex.EncodeToString(digest[:]), ObservedAt: message.CreatedAt}
			sequences[message.ToolCallID] = entry.Sequence
		}
		if !more || len(entries) == 0 {
			break
		}
	}
	proofs := make([]domain.PlanEvidence, 0, len(ids))
	var latestEvidence int64
	for _, id := range ids {
		proof, ok := found[id]
		if !ok {
			return nil, fmt.Errorf("%w: call %q 不是此驗證階段可採用的成功工具結果", domain.ErrInvalidInput, id)
		}
		proofs = append(proofs, proof)
		latestEvidence = max(latestEvidence, sequences[id])
	}
	if latestEvidence < lastMutation {
		return nil, fmt.Errorf("%w: 驗證後仍有可能改變外部狀態的工具操作，請重新查證並提供最新結果 ID", domain.ErrInvalidInput)
	}
	return proofs, nil
}
