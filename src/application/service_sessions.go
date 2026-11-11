package application

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/valueutil"
	"AgenticService/src/ports"
	"context"
	"fmt"
	"strings"
)

func (s *Service) CreateSession(ctx context.Context, agentID string, input domain.CreateSessionInput) (domain.Session, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	engine, err := s.registry.Get(agentID)
	if err != nil {
		return domain.Session{}, err
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	thinkingMode, err := domain.NormalizeThinkingMode(input.ThinkingMode)
	if err != nil {
		return domain.Session{}, err
	}
	input.ThinkingMode = thinkingMode
	input.Metadata = valueutil.CloneMap(input.Metadata)
	delete(input.Metadata, "workspace_root")
	delete(input.Metadata, "sandbox_roots")
	if err := s.validateSessionPlacement(ctx, input.WorkspaceID, input.ProjectID); err != nil {
		return domain.Session{}, err
	}
	if err := s.ensureEphemeralProjectReady(ctx, input.ProjectID); err != nil {
		return domain.Session{}, err
	}
	workspace, err := s.workspaces.Get(ctx, input.WorkspaceID)
	if err != nil {
		return domain.Session{}, err
	}
	defaultProviderID := strings.TrimSpace(workspace.DefaultProviderID)
	// Session 建立後便保存實際 Provider 與模型，避免 Workspace 日後調整預設值時，
	// 舊對話在沒有明確操作的情況下被悄悄切換推理環境。
	if input.ProviderID == "" {
		input.ProviderID = defaultProviderID
	}
	if strings.TrimSpace(input.Model) == "" {
		if input.ProviderID == defaultProviderID {
			input.Model = strings.TrimSpace(workspace.Model)
		}
		if input.Model == "" {
			input.Model = s.defaultModelForProvider(input.ProviderID)
		}
	}
	if err := s.validateSessionProvider(input.ProviderID); err != nil {
		return domain.Session{}, err
	}
	profile, err := s.permissions.Resolve(input.PermissionProfile)
	if err != nil {
		return domain.Session{}, err
	}
	input.PermissionProfile = profile
	return engine.CreateSession(ctx, input)
}

func (s *Service) ListSessions(ctx context.Context, agentID string) ([]domain.Session, error) {
	engine, err := s.registry.Get(agentID)
	if err != nil {
		return nil, err
	}
	values, err := engine.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	return s.decorateSessionsUsage(ctx, values)
}

// ReorderSessions 只接受同一 Workspace、同一 Project 內完整且不重複的未釘選
// Session ID。ProjectID 是空字串時代表未分類；排序不具備搬移語意。
func (s *Service) ReorderSessions(ctx context.Context, agentID string, input domain.ReorderSessionsInput) ([]domain.Session, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	agentID = strings.TrimSpace(agentID)
	engine, err := s.registry.Get(agentID)
	if err != nil {
		return nil, err
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	if err := s.validateSessionPlacement(ctx, input.WorkspaceID, input.ProjectID); err != nil {
		return nil, err
	}
	values, err := engine.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	candidates := make(map[string]domain.Session)
	for _, session := range values {
		if session.WorkspaceID == input.WorkspaceID && session.ProjectID == input.ProjectID && !session.Pinned {
			candidates[session.ID] = session
		}
	}
	if len(input.SessionIDs) == 0 || len(input.SessionIDs) != len(candidates) {
		return nil, fmt.Errorf("%w: session order must contain every unpinned session in the same project", domain.ErrInvalidInput)
	}
	seen := make(map[string]struct{}, len(input.SessionIDs))
	for _, rawID := range input.SessionIDs {
		sessionID := strings.TrimSpace(rawID)
		if sessionID == "" {
			return nil, fmt.Errorf("%w: session id cannot be empty", domain.ErrInvalidInput)
		}
		if _, duplicate := seen[sessionID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate session id %q", domain.ErrConflict, sessionID)
		}
		if _, exists := candidates[sessionID]; !exists {
			return nil, fmt.Errorf("%w: session %q does not belong to the requested project", domain.ErrInvalidInput, sessionID)
		}
		seen[sessionID] = struct{}{}
	}

	updated := make([]domain.Session, 0, len(input.SessionIDs))
	changed := make([]domain.Session, 0, len(input.SessionIDs))
	for position, sessionID := range input.SessionIDs {
		current := candidates[sessionID]
		if current.Position == position {
			updated = append(updated, current)
			continue
		}
		value, updateErr := engine.UpdateSession(ctx, sessionID, domain.UpdateSessionInput{Position: &position})
		if updateErr != nil {
			for _, previous := range changed {
				originalPosition := previous.Position
				_, _ = engine.UpdateSession(context.WithoutCancel(ctx), previous.ID, domain.UpdateSessionInput{Position: &originalPosition})
			}
			return nil, updateErr
		}
		changed = append(changed, current)
		updated = append(updated, value)
	}
	return s.decorateSessionsUsage(ctx, updated)
}

func (s *Service) GetSession(ctx context.Context, sessionID string) (domain.Session, error) {
	_, session, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	return s.decorateSessionUsage(ctx, session)
}

func (s *Service) DeleteSession(ctx context.Context, sessionID string) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	engine, _, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if s.hasActiveSession(sessionID) {
		return fmt.Errorf("%w: session has a queued or running run", domain.ErrConflict)
	}
	return s.deleteSessionLocked(ctx, engine, sessionID)
}

// deleteSessionLocked 共用級聯刪除順序；保留 Session 到最後，失敗時仍有重試入口。
// 呼叫端必須持有 startMu 並確認沒有執行緒仍在使用此 Session。
func (s *Service) deleteSessionLocked(ctx context.Context, engine ports.AgentEngine, sessionID string) error {
	if err := s.plans.DeleteSession(ctx, sessionID); err != nil {
		return err
	}
	if store, ok := s.notifications.(ports.SessionNotificationDeleter); ok {
		if err := store.DeleteSession(ctx, sessionID); err != nil {
			return err
		}
	}
	if events, ok := s.events.(ports.SessionEventDeleter); ok {
		if err := events.DeleteSession(ctx, sessionID); err != nil {
			return err
		}
	}
	if store, ok := s.runs.(ports.SessionRunDeleter); ok {
		values, err := s.runs.List(ctx, sessionID)
		if err != nil {
			return err
		}
		if events, ok := s.events.(ports.RunEventDeleter); ok {
			for _, run := range values {
				if err := events.Delete(run.ID); err != nil {
					return err
				}
			}
		}
		if _, err := store.DeleteSession(ctx, sessionID); err != nil {
			return err
		}
	}
	return engine.DeleteSession(ctx, sessionID)
}

func (s *Service) UpdateSession(ctx context.Context, sessionID string, input domain.UpdateSessionInput) (domain.Session, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	engine, current, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	runtimeOnly := (input.Model != nil || input.ThinkingMode != nil) && input.Title == nil && input.WorkspaceID == nil && input.ProjectID == nil && input.ProviderID == nil && input.LockPlans == nil && input.PermissionProfile == nil && input.MemoryScope == nil && input.Pinned == nil && input.Position == nil
	busy := s.hasActiveSession(sessionID)
	if busy && !runtimeOnly {
		return domain.Session{}, fmt.Errorf("%w: session has a queued or running run", domain.ErrConflict)
	}
	if busy && input.Model != nil && strings.TrimSpace(*input.Model) == "" {
		return domain.Session{}, fmt.Errorf("%w: model cannot be empty", domain.ErrInvalidInput)
	}
	if busy && runtimeOnly {
		provider := current.ProviderID
		if provider == "" && current.WorkspaceID != "" {
			workspace, err := s.workspaces.Get(ctx, current.WorkspaceID)
			if err != nil {
				return domain.Session{}, err
			}
			provider = workspace.DefaultProviderID
		}
		provider = valueutil.FirstNonEmpty(provider, s.providers.DefaultProviderID())
		s.mu.Lock()
		compatible := true
		for _, active := range s.active {
			if active.sessionID == sessionID && !active.cancelRequested && (active.modelSelection == nil || valueutil.FirstNonEmpty(active.providerID, s.providers.DefaultProviderID()) != provider) {
				compatible = false
			}
		}
		s.mu.Unlock()
		if !compatible {
			return domain.Session{}, fmt.Errorf("%w: 目前工作使用不同 Provider 或不支援切換，請完成或取消後再調整模型", domain.ErrConflict)
		}
	}
	workspaceID := current.WorkspaceID
	projectID := current.ProjectID
	if input.WorkspaceID != nil {
		workspaceID = strings.TrimSpace(*input.WorkspaceID)
		input.WorkspaceID = &workspaceID
	}
	if input.ProjectID != nil {
		projectID = strings.TrimSpace(*input.ProjectID)
		input.ProjectID = &projectID
	}
	if input.WorkspaceID != nil || input.ProjectID != nil || input.ProviderID != nil {
		if err := s.validateSessionPlacement(ctx, workspaceID, projectID); err != nil {
			return domain.Session{}, err
		}
	}
	if projectID != current.ProjectID {
		if err := s.validateEphemeralSessionMove(ctx, current.ProjectID, projectID); err != nil {
			return domain.Session{}, err
		}
	}
	providerID := current.ProviderID
	if input.ProviderID != nil {
		providerID = strings.TrimSpace(*input.ProviderID)
		input.ProviderID = &providerID
	}
	if input.WorkspaceID != nil || input.ProviderID != nil {
		if err := s.validateSessionProvider(providerID); err != nil {
			return domain.Session{}, err
		}
	}
	if input.PermissionProfile != nil {
		profile, resolveErr := s.permissions.Resolve(*input.PermissionProfile)
		if resolveErr != nil {
			return domain.Session{}, resolveErr
		}
		input.PermissionProfile = &profile
	}
	if input.ThinkingMode != nil {
		thinkingMode, normalizeErr := domain.NormalizeThinkingMode(*input.ThinkingMode)
		if normalizeErr != nil {
			return domain.Session{}, normalizeErr
		}
		input.ThinkingMode = &thinkingMode
	}
	updated, err := engine.UpdateSession(ctx, sessionID, input)
	if err != nil {
		return domain.Session{}, err
	}
	if runtimeOnly {
		// 設定落盤後才通知 Run；更新失敗不可悄悄切換執行中的模型。
		s.mu.Lock()
		for _, active := range s.active {
			if active.sessionID == sessionID && !active.cancelRequested {
				var model *string
				if input.Model != nil {
					value := strings.TrimSpace(*input.Model)
					model = &value
				}
				active.modelSelection.Update(model, input.ThinkingMode)
			}
		}
		s.mu.Unlock()
	}
	if input.LockPlans != nil {
		if _, err := s.plans.Reconcile(ctx, updated.ID, updated.LockPlans); err != nil {
			return domain.Session{}, err
		}
	}
	return s.decorateSessionUsage(ctx, updated)
}

func (s *Service) sessionInstructions(ctx context.Context, session domain.Session) ([]any, error) {
	entries := []any{}
	if workspaceID := strings.TrimSpace(session.WorkspaceID); workspaceID != "" {
		workspace, err := s.workspaces.Get(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		if text := strings.TrimSpace(workspace.Instructions); text != "" {
			entries = append(entries, map[string]any{"scope": "workspace", "name": workspace.Name, "text": text})
		}
	}
	if projectID := strings.TrimSpace(session.ProjectID); projectID != "" {
		project, err := s.projects.Get(ctx, projectID)
		if err != nil {
			return nil, err
		}
		if text := strings.TrimSpace(project.Instructions); text != "" {
			entries = append(entries, map[string]any{"scope": "project", "name": project.Name, "text": text})
		}
		// 記憶體隔離專案的工作區關閉即消失。不講明的話，模型會照常寫檔、回報完成，
		// 使用者事後才發現硬碟上什麼都沒有——那不是模型說謊，是它根本不知道環境是揮發的。
		if project.Ephemeral {
			entries = append(entries, map[string]any{
				"scope": "project",
				"name":  project.Name,
				"text": "這個專案的工作目錄建立在揮發性 RAM Disk 上，程式關閉或重啟後全部消失。" +
					"需要長期保留的產出，必須在同一次對話裡告訴使用者它不會被保留，並說明可以複製到哪裡；" +
					"不要假設下次對話還讀得到這次寫的檔案。",
			})
		}
	}
	return entries, nil
}

func (s *Service) validateSessionPlacement(ctx context.Context, workspaceID, projectID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return fmt.Errorf("%w: workspace_id is required", domain.ErrInvalidInput)
	}
	if _, err := s.workspaces.Get(ctx, workspaceID); err != nil {
		return err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil
	}
	project, err := s.projects.Get(ctx, projectID)
	if err != nil {
		return err
	}
	if project.WorkspaceID != workspaceID {
		return fmt.Errorf("%w: project does not belong to workspace", domain.ErrInvalidInput)
	}
	return nil
}

// validateSessionProvider 只檢查 Provider 是否為全域已啟用項目。Workspace 提供
// Session 的預設值，但不限制對話可使用的 Provider 範圍。
func (s *Service) validateSessionProvider(providerID string) error {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil
	}
	return s.validateProvider(providerID)
}

func (s *Service) ListMessages(ctx context.Context, sessionID string) ([]domain.Message, error) {
	engine, _, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return engine.ListMessages(ctx, sessionID)
}

// RetractMessages 支援「重新提問」：把最後一則使用者訊息與其後的回答、工具過程
// 移出對話，讓同一個問題可以在乾淨的狀態下重跑。有 Run 在跑時不接受，否則會與
// 正在寫入 transcript 的流程互相踩踏。
func (s *Service) RetractMessages(ctx context.Context, sessionID, messageID string) ([]domain.Message, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	engine, _, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if s.hasActiveSession(sessionID) {
		return nil, fmt.Errorf("%w: session has a queued or running run", domain.ErrConflict)
	}
	return engine.RetractMessages(ctx, sessionID, messageID)
}

func (s *Service) ListEntries(ctx context.Context, sessionID string) ([]domain.SessionEntry, error) {
	engine, _, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return engine.ListEntries(ctx, sessionID)
}

// ListEntriesPage 把分頁一路傳到儲存層：只讀這一頁需要的位元組。
func (s *Service) ListEntriesPage(ctx context.Context, sessionID string, afterSequence int64, limit int) ([]domain.SessionEntry, bool, error) {
	engine, _, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return nil, false, err
	}
	return engine.ListEntriesPage(ctx, sessionID, afterSequence, limit)
}

// CompactSession 手動壓縮 Session 的對話歷史。
//
// 有 Run 在跑時拒絕：壓縮會寫入 transcript，而 Run 正在同一份 transcript 上讀寫，
// 兩邊同時動會讓那一輪送給模型的歷史跟畫面上的對不起來。
func (s *Service) CompactSession(ctx context.Context, sessionID string) (domain.ContextCompactionResult, error) {
	engine, session, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return domain.ContextCompactionResult{}, err
	}
	runs, err := s.runs.List(ctx, session.ID)
	if err != nil {
		return domain.ContextCompactionResult{}, err
	}
	for _, run := range runs {
		switch run.Status {
		case domain.RunStatusQueued, domain.RunStatusRunning, domain.RunStatusPaused, domain.RunStatusWaitingApproval:
			return domain.ContextCompactionResult{}, fmt.Errorf("%w: 這個 Session 還有進行中的工作，結束後才能手動壓縮", domain.ErrConflict)
		}
	}
	return engine.CompactSession(ctx, session.ID)
}

func (s *Service) decorateSessionsUsage(ctx context.Context, sessions []domain.Session) ([]domain.Session, error) {
	for index := range sessions {
		value, err := s.decorateSessionUsage(ctx, sessions[index])
		if err != nil {
			return nil, err
		}
		sessions[index] = value
	}
	return sessions, nil
}

func (s *Service) decorateSessionUsage(ctx context.Context, session domain.Session) (domain.Session, error) {
	if reader, ok := s.runs.(ports.SessionRunUsageReader); ok {
		values, err := reader.ListSessionRunUsage(ctx, session.ID)
		if err != nil {
			return domain.Session{}, err
		}
		session.Usage = summarizeUsageSnapshots(values)
		return session, nil
	}
	runs, err := s.runs.List(ctx, session.ID)
	if err != nil {
		return domain.Session{}, err
	}
	session.Usage = summarizeSessionUsage(runs)
	return session, nil
}
