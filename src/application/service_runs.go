package application

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/textutil"
	"AgenticService/src/internal/valueutil"
	"AgenticService/src/ports"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

func (s *Service) ListRuns(ctx context.Context, sessionID string) ([]domain.Run, error) {
	return s.runs.List(ctx, strings.TrimSpace(sessionID))
}

func (s *Service) GetRun(ctx context.Context, runID string) (domain.Run, error) {
	return s.runs.Get(ctx, strings.TrimSpace(runID))
}

func (s *Service) ListRunEvents(ctx context.Context, runID string, afterSequence int64) ([]domain.Event, error) {
	runID = strings.TrimSpace(runID)
	if _, err := s.runs.Get(ctx, runID); err != nil {
		return nil, err
	}
	if afterSequence < 0 {
		return nil, fmt.Errorf("%w: after_sequence cannot be negative", domain.ErrInvalidInput)
	}
	return s.events.List(ctx, runID, afterSequence)
}

// StartRun 建立 durable Run 後立即返回。實際工作使用 service-owned context，
// 不會因建立 Run 的 HTTP request 結束或 SSE client 斷線而取消。
func (s *Service) StartRun(ctx context.Context, input domain.RunInput) (domain.Run, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	return s.startRunLocked(ctx, input, nil)
}

// 呼叫端持有 startMu；beforeSave 完成關聯資料持久化後，才允許啟動工作。
func (s *Service) startRunLocked(ctx context.Context, input domain.RunInput, beforeSave func(domain.Run) error) (domain.Run, error) {
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.UserInput = strings.TrimSpace(textutil.NormalizeFullwidthASCII(input.UserInput))
	input.AttachmentIDs = normalizedAttachmentIDs(input.AttachmentIDs)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.SessionID == "" || input.UserInput == "" {
		return domain.Run{}, fmt.Errorf("%w: session_id and input are required", domain.ErrInvalidInput)
	}
	if len(input.AttachmentIDs) > 16 {
		return domain.Run{}, fmt.Errorf("%w: no more than 16 attachments are allowed", domain.ErrInvalidInput)
	}
	engine, session, err := s.resolveSession(ctx, input.SessionID)
	if err != nil {
		return domain.Run{}, err
	}
	input.Metadata = valueutil.CloneMap(input.Metadata)
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	// sandbox_roots 是後端根據 Project 產生的保留欄位，不能由 Run 呼叫端自行擴權。
	delete(input.Metadata, "sandbox_roots")
	// attachments 同樣是後端解析 Attachment ID 後產生的可信 manifest；Client
	// 直接夾帶同名 metadata 不能注入任意主機路徑。
	delete(input.Metadata, "attachments")
	// ephemeral_project 決定這次 Run 要不要跳過人工核准，因此必須是後端自己判定的
	// 保留欄位。若讓 Client 夾帶，等於給了一個「宣告自己是記憶體專案就免審核」的後門。
	delete(input.Metadata, "ephemeral_project")
	// ThinkingMode 是明確的 Run override；為相容舊 Client，也接受 metadata 中的
	// thinking_mode。若兩者都沒有，才沿用 Session 設定；空值表示 Provider 預設。
	rawThinkingMode := strings.TrimSpace(input.ThinkingMode)
	if rawThinkingMode == "" {
		if value, exists := input.Metadata["thinking_mode"]; exists {
			legacy, ok := value.(string)
			if !ok && value != nil {
				return domain.Run{}, fmt.Errorf("%w: thinking_mode must be a string", domain.ErrInvalidInput)
			}
			rawThinkingMode = legacy
		} else {
			rawThinkingMode = session.ThinkingMode
		}
	}
	thinkingMode, err := domain.NormalizeThinkingMode(rawThinkingMode)
	if err != nil {
		return domain.Run{}, err
	}
	input.ThinkingMode = thinkingMode
	// 保存到 Run metadata，讓重試時不會因 Session 後來變更而改變原本的設定。
	input.Metadata["thinking_mode"] = thinkingMode
	sandboxRoots := []string{}
	ephemeralProjectID := ""
	// 後端內部流程（排程執行器）帶進來的沙箱優先，讓相對路徑以它為基準。
	for _, root := range input.SandboxRoots {
		if root = strings.TrimSpace(root); root != "" {
			sandboxRoots = appendUniqueString(sandboxRoots, root)
		}
	}
	if session.ProjectID != "" {
		project, projectErr := s.projects.Get(ctx, session.ProjectID)
		if projectErr != nil {
			return domain.Run{}, projectErr
		}
		if project.Ephemeral {
			if s.ephemeralProjects == nil {
				return domain.Run{}, fmt.Errorf("%w: memory-isolated project support is unavailable", domain.ErrConflict)
			}
			root, prepareErr := s.ephemeralProjects.Prepare(ctx, project.ID, project.RAMDiskSizeMB)
			if prepareErr != nil {
				return domain.Run{}, fmt.Errorf("prepare memory-isolated project: %w", prepareErr)
			}
			sandboxRoots = appendUniqueString(sandboxRoots, root)
			ephemeralProjectID = project.ID
		} else {
			for _, root := range project.SandboxRoots {
				sandboxRoots = appendUniqueString(sandboxRoots, root)
			}
		}
	}
	// Session 私有工作目錄由後端建立，用來容納對話附件與未綁定 Project 的工作。
	// Project 根目錄仍排第一，確保相對路徑維持以 Project 為基準。
	if workspaceRoot, _ := session.Metadata["workspace_root"].(string); ephemeralProjectID == "" && strings.TrimSpace(workspaceRoot) != "" {
		sandboxRoots = appendUniqueString(sandboxRoots, strings.TrimSpace(workspaceRoot))
	}
	if len(sandboxRoots) > 0 {
		input.Metadata["sandbox_roots"] = sandboxRoots
	}
	if ephemeralProjectID != "" {
		input.Metadata["ephemeral_project"] = true
	}
	// instructions 同樣是後端依 Workspace／Project 產生的保留欄位；
	// 呼叫端不能藉 metadata 自行往提示注入內容。
	delete(input.Metadata, "instructions")
	instructions, err := s.sessionInstructions(ctx, session)
	if err != nil {
		return domain.Run{}, err
	}
	if len(instructions) > 0 {
		input.Metadata["instructions"] = instructions
	}
	if len(input.AttachmentIDs) > 0 {
		if s.attachments == nil {
			return domain.Run{}, fmt.Errorf("%w: attachment storage is unavailable", domain.ErrConflict)
		}
		manifest := make([]domain.Attachment, 0, len(input.AttachmentIDs))
		for _, attachmentID := range input.AttachmentIDs {
			attachment, attachmentErr := s.attachments.Get(ctx, session.ID, attachmentID)
			if attachmentErr != nil {
				return domain.Run{}, attachmentErr
			}
			if ephemeralProjectID != "" {
				stagedPath, stageErr := s.ephemeralProjects.StageFile(ctx, ephemeralProjectID, attachment.Path, attachment.ID+"-"+attachment.Name)
				if stageErr != nil {
					return domain.Run{}, fmt.Errorf("stage attachment in memory-isolated project: %w", stageErr)
				}
				attachment.Path = stagedPath
			}
			manifest = append(manifest, attachment)
		}
		input.Metadata["attachments"] = manifest
	}
	input.ProviderID = valueutil.FirstNonEmpty(strings.TrimSpace(input.ProviderID), strings.TrimSpace(session.ProviderID))
	input.Model = valueutil.FirstNonEmpty(strings.TrimSpace(input.Model), strings.TrimSpace(session.Model))
	if session.WorkspaceID != "" && (input.ProviderID == "" || input.Model == "") {
		workspace, workspaceErr := s.workspaces.Get(ctx, session.WorkspaceID)
		if workspaceErr != nil {
			return domain.Run{}, workspaceErr
		}
		input.ProviderID = valueutil.FirstNonEmpty(input.ProviderID, workspace.DefaultProviderID)
		input.Model = valueutil.FirstNonEmpty(input.Model, workspace.Model)
	}
	if input.ProviderID == "" {
		input.ProviderID = s.providers.DefaultProviderID()
	}
	if err := s.validateSessionProvider(input.ProviderID); err != nil {
		return domain.Run{}, err
	}

	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return domain.Run{}, fmt.Errorf("%w: service is shutting down", domain.ErrConflict)
	}
	fingerprint, err := runInputFingerprint(input)
	if err != nil {
		return domain.Run{}, err
	}
	if input.IdempotencyKey != "" {
		existing, findErr := s.runs.FindByIdempotencyKey(ctx, input.SessionID, input.IdempotencyKey)
		if findErr == nil {
			if !sameIdempotentRun(existing, input, fingerprint) {
				return domain.Run{}, fmt.Errorf("%w: Idempotency-Key %q was already used with different run input", domain.ErrConflict, input.IdempotencyKey)
			}
			return existing, nil
		}
		if !errors.Is(findErr, domain.ErrNotFound) {
			return domain.Run{}, findErr
		}
	}

	now := s.now().UTC()
	run := domain.Run{
		// Run ID 沿用 Session 的 Project 歸屬，事件檔才找得到該放哪個 RAM disk。
		ID:                     domain.NewRunIDForSession(session.ID),
		AgentID:                session.AgentID,
		SessionID:              session.ID,
		Status:                 domain.RunStatusQueued,
		Input:                  input.UserInput,
		AttachmentIDs:          append([]string(nil), input.AttachmentIDs...),
		ProviderID:             input.ProviderID,
		Model:                  input.Model,
		IdempotencyKey:         input.IdempotencyKey,
		IdempotencyFingerprint: fingerprint,
		Metadata:               valueutil.CloneMap(input.Metadata),
		CreatedAt:              now,
	}
	if beforeSave != nil {
		if err := beforeSave(run); err != nil {
			return domain.Run{}, err
		}
	}
	if guard, ok := s.runs.(ports.RunRetentionGuard); ok {
		guard.ProtectRun(run.ID)
	}
	if err := s.runs.Save(ctx, run); err != nil {
		if guard, ok := s.runs.(ports.RunRetentionGuard); ok {
			guard.ReleaseRun(run.ID)
		}
		return domain.Run{}, err
	}
	input.RunID = run.ID
	input.Inputs = domain.NewRunInputQueue()
	input.ModelSelection = &domain.RunModelSelection{}
	runCtx, cancel := context.WithCancel(s.rootCtx)
	s.mu.Lock()
	s.active[run.ID] = activeRun{sessionID: session.ID, cancel: cancel, inputs: input.Inputs, modelSelection: input.ModelSelection, providerID: input.ProviderID}
	s.mu.Unlock()
	s.wg.Add(1)
	s.logger.Info("run queued", "run_id", run.ID, "session_id", session.ID, "agent_id", session.AgentID, "provider_id", run.ProviderID, "model", run.Model)
	go s.executeRun(runCtx, engine, session, input, run)
	return run, nil
}

// runInputFingerprint 只納入會決定實際工作的穩定欄位。後端產生的 instructions、
// sandbox manifest 與其他環境 metadata 不參與雜湊，避免相同 Client request 因為
// 後端重新載入設定而失去冪等性。
func runInputFingerprint(input domain.RunInput) (string, error) {
	payload := struct {
		SessionID     string   `json:"session_id"`
		Input         string   `json:"input"`
		AttachmentIDs []string `json:"attachment_ids,omitempty"`
		ProviderID    string   `json:"provider_id,omitempty"`
		Model         string   `json:"model,omitempty"`
		ThinkingMode  string   `json:"thinking_mode,omitempty"`
	}{
		SessionID:     input.SessionID,
		Input:         input.UserInput,
		AttachmentIDs: append([]string(nil), input.AttachmentIDs...),
		ProviderID:    input.ProviderID,
		Model:         input.Model,
		ThinkingMode:  input.ThinkingMode,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode idempotent run input: %w", err)
	}
	digest := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", digest[:]), nil
}

func sameIdempotentRun(run domain.Run, input domain.RunInput, fingerprint string) bool {
	if run.IdempotencyFingerprint != "" {
		return run.IdempotencyFingerprint == fingerprint
	}
	if run.SessionID != input.SessionID || run.Input != input.UserInput || run.ProviderID != input.ProviderID || run.Model != input.Model {
		return false
	}
	if thinkingMode, ok := run.Metadata["thinking_mode"].(string); ok && thinkingMode != input.ThinkingMode {
		return false
	}
	if len(run.AttachmentIDs) != len(input.AttachmentIDs) {
		return false
	}
	for index := range run.AttachmentIDs {
		if run.AttachmentIDs[index] != input.AttachmentIDs[index] {
			return false
		}
	}
	return true
}

// ExecuteRun 提供同步等待介面；底層仍是非同步 Run。ctx 取消只停止等待，
// 若要取消工作本身必須明確呼叫 CancelRun。
func (s *Service) ExecuteRun(ctx context.Context, input domain.RunInput, sink EventSink) (domain.Run, error) {
	run, err := s.StartRun(ctx, input)
	if err != nil {
		return domain.Run{}, err
	}
	return s.WaitRun(ctx, run.ID, 0, sink)
}

func (s *Service) WaitRun(ctx context.Context, runID string, afterSequence int64, sink EventSink) (domain.Run, error) {
	for {
		updates, unsubscribe, err := s.SubscribeRunEvents(ctx, runID)
		if err != nil {
			return domain.Run{}, err
		}
		run, events, err := s.ReadRunEventBatch(ctx, runID, afterSequence)
		if err != nil {
			unsubscribe()
			return domain.Run{}, err
		}
		for _, event := range events {
			if sink != nil {
				if sinkErr := sink(event); sinkErr != nil {
					unsubscribe()
					current, _ := s.GetRun(context.WithoutCancel(ctx), runID)
					return current, sinkErr
				}
			}
			afterSequence = event.Sequence
		}
		if terminalRun(run.Status) {
			unsubscribe()
			return run, nil
		}
		select {
		case <-ctx.Done():
			unsubscribe()
			return run, ctx.Err()
		case <-updates:
			unsubscribe()
		}
	}
}

// SubscribeRunEvents 只提供喚醒通知；事件內容一律重新讀 durable log，
// 因此通知合併或 client 暫時離線都不會造成事件缺口。
func (s *Service) SubscribeRunEvents(ctx context.Context, runID string) (<-chan struct{}, func(), error) {
	runID = strings.TrimSpace(runID)
	if _, err := s.runs.Get(ctx, runID); err != nil {
		return nil, nil, err
	}
	channel := make(chan struct{}, 1)
	s.mu.Lock()
	s.nextWatcher++
	id := s.nextWatcher
	if s.watchers[runID] == nil {
		s.watchers[runID] = map[uint64]chan struct{}{}
	}
	s.watchers[runID][id] = channel
	s.mu.Unlock()
	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.watchers[runID], id)
			if len(s.watchers[runID]) == 0 {
				delete(s.watchers, runID)
			}
			s.mu.Unlock()
		})
	}
	return channel, unsubscribe, nil
}

func (s *Service) CancelRun(ctx context.Context, runID string) (domain.Run, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	return s.cancelRunLocked(ctx, runID)
}

// 呼叫端持有 startMu，供 LOOP 在同一個啟停交易中取消 Run。
func (s *Service) cancelRunLocked(ctx context.Context, runID string) (domain.Run, error) {
	run, err := s.runs.Get(ctx, strings.TrimSpace(runID))
	if err != nil {
		return domain.Run{}, err
	}
	s.mu.Lock()
	active, ok := s.active[run.ID]
	s.mu.Unlock()
	if !ok {
		if terminalRun(run.Status) {
			return run, nil
		}
		return run, fmt.Errorf("%w: run is not active", domain.ErrConflict)
	}
	// 先記錄取消意圖，再中止 context；執行緒可能剛好位於完成轉換的邊界，
	// 這個旗標讓它不會在取消請求之後把舊快照寫回 completed。
	s.mu.Lock()
	if current, exists := s.active[run.ID]; exists {
		current.cancelRequested = true
		s.active[run.ID] = current
	}
	s.mu.Unlock()
	active.cancel()
	return s.persistImmediateCancellation(run)
}

// persistImmediateCancellation 讓控制 API 立即反映使用者的停止意圖，
// 不把完成時間綁在第三方 Provider 是否正確遵守 context 取消上。
func (s *Service) persistImmediateCancellation(run domain.Run) (domain.Run, error) {
	lock := s.eventLock(run.ID)
	lock.Lock()
	defer lock.Unlock()
	defer s.notifyRun(run.ID)
	completedAt := s.now().UTC()
	run.Status = domain.RunStatusCanceled
	run.PendingApproval = nil
	run.Error = &domain.RunError{Code: "run_canceled", Message: "run canceled", Retryable: true}
	run.CompletedAt = &completedAt
	// 停止後不再接受插入；尚未寫進對話的訊息隨終止事件退回。
	s.mu.Lock()
	returned := s.closeRunInputsLocked(run.ID)
	s.mu.Unlock()
	current, changed, err := s.saveTerminalRun(run)
	if err != nil || !changed {
		return current, err
	}
	payload := map[string]any{"status": current.Status, "error": current.Error}
	if len(returned) > 0 {
		payload["returned_inputs"] = returned
	}
	if current.Usage != nil {
		payload["usage"] = *current.Usage
	}
	value, _ := s.eventSequences.Load(run.ID)
	sequence, _ := value.(int64)
	if sequence == 0 {
		sequence = s.latestRunSequence(context.Background(), run.ID)
	}
	if err := s.appendEventLocked(current, &sequence, "run."+string(current.Status), payload); err != nil {
		// 狀態已落盤，事件失敗仍保留可診斷資訊，不讓重送取消產生第二個終止事件。
		s.logger.Error("run cancellation event write failed", "run_id", current.ID, "error", err)
		s.notifyRun(current.ID)
	}
	s.notifyRunFinished(current)
	return current, nil
}

// AnswerQuestion 把使用者的抉擇送回正在等待的工具。
//
// 取消也走這裡：使用者沒有義務回答，取消是一種合法答案而不是錯誤。
func (s *Service) AnswerQuestion(_ context.Context, questionID string, answer domain.UserQuestionAnswer) error {
	if s.questions == nil {
		return fmt.Errorf("%w: question workflow is unavailable", domain.ErrConflict)
	}
	answer.QuestionID = strings.TrimSpace(questionID)
	return s.questions.Answer(answer)
}

func (s *Service) DecideRun(ctx context.Context, runID string, input domain.ToolApprovalDecisionInput) (domain.Run, error) {
	if s.approvals == nil {
		return domain.Run{}, fmt.Errorf("%w: approval workflow is unavailable", domain.ErrConflict)
	}
	run, err := s.runs.Get(ctx, strings.TrimSpace(runID))
	if err != nil {
		return domain.Run{}, err
	}
	input.ApprovalID = strings.TrimSpace(input.ApprovalID)
	if run.Status != domain.RunStatusWaitingApproval || run.PendingApproval == nil {
		return run, fmt.Errorf("%w: run is not waiting for approval", domain.ErrConflict)
	}
	if input.ApprovalID == "" || input.ApprovalID != run.PendingApproval.ID {
		return run, fmt.Errorf("%w: approval_id does not match the pending approval", domain.ErrConflict)
	}
	if input.Permanent && input.Decision != domain.ToolApprovalApprove {
		return run, fmt.Errorf("%w: permanent approval requires an approve decision", domain.ErrInvalidInput)
	}
	if input.Permanent && run.PendingApproval.OneTimeOnly {
		return run, fmt.Errorf("%w: 結果未知的操作只允許逐次核准，不能設為永久授權", domain.ErrInvalidInput)
	}
	var permanentEngine ports.AgentEngine
	permanentChanged := false
	if input.Permanent {
		engine, session, resolveErr := s.resolveSession(ctx, run.SessionID)
		if resolveErr != nil {
			return run, resolveErr
		}
		permanentEngine = engine
		if !session.PermanentToolApproval {
			if _, updateErr := engine.SetPermanentToolApproval(ctx, run.SessionID, true); updateErr != nil {
				return run, updateErr
			}
			permanentChanged = true
		}
	}
	if err := s.approvals.Decide(run.ID, input); err != nil {
		// 決策未送達 waiter 時回復剛才的持久化變更，避免一次失敗的 API
		// 呼叫意外替 Session 開啟永久核准。
		if permanentChanged && permanentEngine != nil {
			_, _ = permanentEngine.SetPermanentToolApproval(context.WithoutCancel(ctx), run.SessionID, false)
		}
		return run, err
	}
	// 決策喚醒 Harness 後，狀態會由 durable run.approval_resolved 事件切回 running。
	// 這裡重新讀取可取得已經完成切換的狀態；若 goroutine 尚未排程則仍回 waiting。
	if current, getErr := s.runs.Get(ctx, run.ID); getErr == nil {
		return current, nil
	}
	return run, nil
}

// RetryRun 建立新的 durable Run，保留原 Run 作為不可變稽核紀錄。呼叫端應提供
// Idempotency-Key，讓 HTTP 回應遺失時重送不會再建立第三份工作。
func (s *Service) RetryRun(ctx context.Context, runID, idempotencyKey string) (domain.Run, error) {
	original, err := s.runs.Get(ctx, strings.TrimSpace(runID))
	if err != nil {
		return domain.Run{}, err
	}
	if original.Status != domain.RunStatusFailed && original.Status != domain.RunStatusCanceled {
		return original, fmt.Errorf("%w: only failed or canceled runs can be retried", domain.ErrConflict)
	}
	if original.Error == nil || !original.Error.Retryable {
		return original, fmt.Errorf("%w: run is not retryable", domain.ErrConflict)
	}
	metadata := valueutil.CloneMap(original.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	delete(metadata, "termination")
	delete(metadata, "budget_exceeded")
	metadata["retry_of"] = original.ID
	return s.StartRun(ctx, domain.RunInput{
		SessionID:      original.SessionID,
		UserInput:      original.Input,
		AttachmentIDs:  append([]string(nil), original.AttachmentIDs...),
		ProviderID:     original.ProviderID,
		Model:          original.Model,
		Metadata:       metadata,
		IdempotencyKey: strings.TrimSpace(idempotencyKey),
	})
}

func normalizedAttachmentIDs(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func approvalRequestFromPayload(payload map[string]any) (domain.ToolApprovalRequest, bool) {
	if payload == nil {
		return domain.ToolApprovalRequest{}, false
	}
	switch value := payload["approval"].(type) {
	case domain.ToolApprovalRequest:
		return value, strings.TrimSpace(value.ID) != ""
	case *domain.ToolApprovalRequest:
		if value != nil {
			return *value, strings.TrimSpace(value.ID) != ""
		}
	}
	return domain.ToolApprovalRequest{}, false
}
