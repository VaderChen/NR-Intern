package application

import (
	"AgenticService/src/domain"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	defaultNotificationLimit = 100
	maxSearchQueryRunes      = 200
	maxSearchResults         = 100
)

func (s *Service) ListNotifications(ctx context.Context, limit int, unreadOnly bool) ([]domain.Notification, error) {
	if s.notifications == nil || !s.notificationsEnabled.Load() {
		return []domain.Notification{}, nil
	}
	if limit <= 0 {
		limit = defaultNotificationLimit
	}
	return s.notifications.List(ctx, limit, unreadOnly)
}

func (s *Service) MarkNotificationRead(ctx context.Context, id string) error {
	if s.notifications == nil {
		return fmt.Errorf("%w: notifications are unavailable", domain.ErrConflict)
	}
	return s.notifications.MarkRead(ctx, strings.TrimSpace(id))
}

func (s *Service) MarkAllNotificationsRead(ctx context.Context) error {
	if s.notifications == nil {
		return fmt.Errorf("%w: notifications are unavailable", domain.ErrConflict)
	}
	return s.notifications.MarkAllRead(ctx)
}

func (s *Service) ClearReadNotifications(ctx context.Context) error {
	if s.notifications == nil {
		return fmt.Errorf("%w: notifications are unavailable", domain.ErrConflict)
	}
	return s.notifications.DeleteRead(ctx)
}

func (s *Service) addNotification(value domain.Notification) {
	if s.notifications == nil || !s.notificationsEnabled.Load() {
		return
	}
	if value.ID == "" {
		value.ID = domain.NewID("notification")
	}
	if value.CreatedAt.IsZero() {
		value.CreatedAt = s.now().UTC()
	}
	if value.Level == "" {
		value.Level = "info"
	}
	if value.Type == "" {
		value.Type = "system"
	}
	if err := s.notifications.Save(context.Background(), value); err != nil {
		s.logger.Warn("save notification failed", "notification_id", value.ID, "error", err)
	}
}

// SetNotificationsEnabled 即時切換通知摘要的建立與讀取；關閉時既有資料保留，
// 之後重新開啟仍可查看，不會因切換設定而破壞使用者資料。
func (s *Service) SetNotificationsEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.notificationsEnabled.Store(enabled)
}

func (s *Service) notifyRunFinished(run domain.Run) {
	level, title, message := "success", "Run 已完成", "工作已完成。"
	switch run.Status {
	case domain.RunStatusFailed:
		level, title, message = "error", "Run 執行失敗", "工作執行失敗，可從 Run 詳情重試。"
		if run.Error != nil && run.Error.Code == "server_restarted" {
			level, title, message = "warning", "Run 因後端重啟中斷", "後端重新啟動時這項工作被中斷，可重試以繼續。"
		}
	case domain.RunStatusCanceled:
		level, title, message = "warning", "Run 已取消", "工作已取消。"
	}
	s.addNotification(domain.Notification{
		Type:      "run." + string(run.Status),
		Level:     level,
		Title:     title,
		Message:   message,
		DedupeKey: "run-finished:" + run.ID + ":" + string(run.Status),
		RunID:     run.ID,
		SessionID: run.SessionID,
		Metadata:  map[string]any{"retryable": run.Error != nil && run.Error.Retryable},
	})
}

func (s *Service) notifyApproval(run domain.Run, approval domain.ToolApprovalRequest) {
	s.addNotification(domain.Notification{
		Type:      "run.approval_required",
		Level:     "warning",
		Title:     "需要工具授權",
		Message:   fmt.Sprintf("Run 需要授權工具：%s", strings.TrimSpace(approval.ToolName)),
		DedupeKey: "approval:" + approval.ID,
		RunID:     run.ID,
		SessionID: run.SessionID,
	})
}

// NotifyUpdateAvailable 將新版本加入通知中心。版本號是去重鍵的一部分，
// 同一個 Release 只通知一次；通知內容不包含任何本機設定或憑證。
func (s *Service) NotifyUpdateAvailable(status domain.UpdateStatus) {
	if !status.Available || strings.TrimSpace(status.LatestVersion) == "" {
		return
	}
	s.addNotification(domain.Notification{
		Type:      "system.update_available",
		Level:     "info",
		Title:     "有新的 NR-Intern 版本",
		Message:   fmt.Sprintf("發現新版本 %s，目前版本為 %s。", status.LatestVersion, status.CurrentVersion),
		DedupeKey: "update:" + strings.TrimSpace(status.LatestVersion),
		Metadata: map[string]any{
			"current_version": status.CurrentVersion,
			"latest_version":  status.LatestVersion,
			"release_name":    status.ReleaseName,
			"release_url":     status.ReleaseURL,
		},
	})
}

// PauseRun 將暫停標記寫入 Run。正在進行中的 Provider HTTP request 不會被強制切斷，
// 會在下一個安全回合邊界停住，避免留下半截工具／訊息協定。
func (s *Service) PauseRun(ctx context.Context, runID string) (domain.Run, error) {
	runID = strings.TrimSpace(runID)
	s.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()
	run, err := s.runs.Get(ctx, runID)
	if err != nil {
		return domain.Run{}, err
	}
	if terminalRun(run.Status) {
		return run, fmt.Errorf("%w: terminal run cannot be paused", domain.ErrConflict)
	}
	if run.Status == domain.RunStatusWaitingApproval {
		return run, fmt.Errorf("%w: waiting approval run must be approved or canceled", domain.ErrConflict)
	}
	_, active := s.active[run.ID]
	if !active {
		return run, fmt.Errorf("%w: run is not active", domain.ErrConflict)
	}
	if s.pausedRuns[run.ID] {
		return run, nil
	}
	// 重新取得 durable 狀態時仍持有控制鎖，避免在 Run 即將完成的競態中
	// 把已經是 terminal 的 Run 寫回 paused。
	run, err = s.runs.Get(ctx, run.ID)
	if err != nil {
		return domain.Run{}, err
	}
	if terminalRun(run.Status) {
		return run, fmt.Errorf("%w: terminal run cannot be paused", domain.ErrConflict)
	}
	if run.Status == domain.RunStatusWaitingApproval {
		return run, fmt.Errorf("%w: waiting approval run must be approved or canceled", domain.ErrConflict)
	}
	s.pausedRuns[run.ID] = true
	if s.runSignals[run.ID] == nil {
		s.runSignals[run.ID] = make(chan struct{})
	}
	run.Status = domain.RunStatusPaused
	if run.Metadata == nil {
		run.Metadata = map[string]any{}
	}
	run.Metadata["pause_requested"] = true
	if err := s.runs.Save(ctx, run); err != nil {
		s.clearRunPauseLocked(run.ID)
		return domain.Run{}, err
	}
	s.mu.Unlock()
	locked = false
	if err := s.appendControlEvent(run, "run.paused", map[string]any{"status": run.Status}); err != nil {
		return run, err
	}
	s.notifyRun(run.ID)
	return run, nil
}

func (s *Service) ResumeRun(ctx context.Context, runID string) (domain.Run, error) {
	runID = strings.TrimSpace(runID)
	s.mu.Lock()
	run, err := s.runs.Get(ctx, runID)
	if err != nil {
		s.mu.Unlock()
		return domain.Run{}, err
	}
	if run.Status != domain.RunStatusPaused {
		s.mu.Unlock()
		return run, fmt.Errorf("%w: run is not paused", domain.ErrConflict)
	}
	if _, active := s.active[run.ID]; !active {
		s.mu.Unlock()
		return run, fmt.Errorf("%w: run is not active", domain.ErrConflict)
	}
	run.Status = domain.RunStatusRunning
	if run.Metadata != nil {
		delete(run.Metadata, "pause_requested")
	}
	if err := s.runs.Save(ctx, run); err != nil {
		s.mu.Unlock()
		return domain.Run{}, err
	}
	delete(s.pausedRuns, run.ID)
	signal := s.runSignals[run.ID]
	if signal != nil {
		close(signal)
	}
	s.runSignals[run.ID] = make(chan struct{})
	s.mu.Unlock()
	if err := s.appendControlEvent(run, "run.resumed", map[string]any{"status": run.Status}); err != nil {
		return run, err
	}
	s.notifyRun(run.ID)
	return run, nil
}

type RunControlSummary struct {
	Requested int          `json:"requested"`
	Accepted  int          `json:"accepted"`
	Runs      []domain.Run `json:"runs"`
}

func (s *Service) CancelAllRuns(ctx context.Context) (RunControlSummary, error) {
	values, err := s.runs.List(ctx, "")
	if err != nil {
		return RunControlSummary{}, err
	}
	result := RunControlSummary{Runs: make([]domain.Run, 0)}
	for _, run := range values {
		if run.Status != domain.RunStatusQueued && run.Status != domain.RunStatusRunning && run.Status != domain.RunStatusPaused && run.Status != domain.RunStatusWaitingApproval {
			continue
		}
		result.Requested++
		updated, cancelErr := s.CancelRun(ctx, run.ID)
		if cancelErr == nil {
			result.Accepted++
			result.Runs = append(result.Runs, updated)
		}
	}
	return result, nil
}

func (s *Service) PermissionCenter(ctx context.Context) (domain.PermissionCenter, error) {
	values, err := s.runs.List(ctx, "")
	if err != nil {
		return domain.PermissionCenter{}, err
	}
	waiting := 0
	for _, run := range values {
		if run.Status == domain.RunStatusWaitingApproval {
			waiting++
		}
	}
	return domain.PermissionCenter{Policy: s.permissions.Normalize(), WaitingApprovalRuns: waiting}, nil
}

func (s *Service) latestRunSequence(ctx context.Context, runID string) int64 {
	values, err := s.events.List(ctx, runID, 0)
	if err != nil || len(values) == 0 {
		return 0
	}
	return values[len(values)-1].Sequence
}

// GlobalSearch 在後端統一掃描可搜尋的正式資料來源，前端只會收到短摘要。
func (s *Service) GlobalSearch(ctx context.Context, query string, limit int) ([]domain.SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []domain.SearchResult{}, nil
	}
	if len([]rune(query)) > maxSearchQueryRunes {
		return nil, fmt.Errorf("%w: search query cannot exceed %d characters", domain.ErrInvalidInput, maxSearchQueryRunes)
	}
	if limit <= 0 || limit > maxSearchResults {
		limit = maxSearchResults
	}
	needle := strings.ToLower(query)
	results := make([]domain.SearchResult, 0, limit)
	add := func(kind, id, title, text, workspaceID, projectID, sessionID string, createdAt time.Time) {
		if len(results) >= limit || !strings.Contains(strings.ToLower(text), needle) {
			return
		}
		results = append(results, domain.SearchResult{Kind: kind, ID: id, Title: title, Snippet: searchSnippet(text, query), WorkspaceID: workspaceID, ProjectID: projectID, SessionID: sessionID, CreatedAt: createdAt})
	}
	workspaces, err := s.workspaces.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, value := range workspaces {
		add("workspace", value.ID, value.Name, value.Name+" "+value.Description+" "+value.Instructions, value.ID, "", "", value.CreatedAt)
	}
	projects, err := s.projects.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, value := range projects {
		add("project", value.ID, value.Name, value.Name+" "+value.Description+" "+value.Instructions, value.WorkspaceID, value.ID, "", value.CreatedAt)
	}
	for _, agent := range s.registry.List() {
		sessions, listErr := s.ListSessions(ctx, agent.ID)
		if listErr != nil {
			return nil, listErr
		}
		for _, session := range sessions {
			add("session", session.ID, session.Title, session.Title, session.WorkspaceID, session.ProjectID, session.ID, session.CreatedAt)
			messages, messageErr := s.ListMessages(ctx, session.ID)
			if messageErr != nil {
				return nil, messageErr
			}
			for _, message := range messages {
				add("message", message.ID, session.Title, message.Content+" "+message.Reasoning, session.WorkspaceID, session.ProjectID, session.ID, message.CreatedAt)
			}
			plans, planErr := s.plans.List(ctx, session.ID)
			if planErr != nil {
				return nil, planErr
			}
			for _, plan := range plans {
				text := plan.Title + " " + plan.Objective
				for _, step := range plan.Steps {
					text += " " + step.Title + " " + step.Description + " " + step.Evidence
				}
				add("plan", plan.ID, plan.Title, text, session.WorkspaceID, session.ProjectID, session.ID, plan.CreatedAt)
			}
		}
	}
	if s.schedules != nil {
		schedules, scheduleErr := s.schedules.List(ctx)
		if scheduleErr != nil {
			return nil, scheduleErr
		}
		for _, value := range schedules {
			add("schedule", value.ID, value.Name, value.Name+" "+value.Prompt, value.WorkspaceID, "", "", value.CreatedAt)
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].CreatedAt.After(results[j].CreatedAt) })
	return results, nil
}

func searchSnippet(text, query string) string {
	runes, needle := []rune(text), []rune(strings.ToLower(query))
	lower := []rune(strings.ToLower(text))
	index := 0
	for i := range lower {
		if len(needle) <= len(lower)-i && string(lower[i:i+len(needle)]) == string(needle) {
			index = i
			break
		}
	}
	start, end := index-80, index+len(needle)+160
	if start < 0 {
		start = 0
	}
	if end > len(runes) {
		end = len(runes)
	}
	prefix, suffix := "", ""
	if start > 0 {
		prefix = "…"
	}
	if end < len(runes) {
		suffix = "…"
	}
	return prefix + strings.TrimSpace(string(runes[start:end])) + suffix
}
