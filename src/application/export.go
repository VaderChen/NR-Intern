package application

import (
	"AgenticService/src/domain"
	"context"
	"fmt"
	"strings"
)

// SessionExport 是一次 Session 的可攜快照。
//
// Session 的內容過去只能經由 messages/entries 端點逐段取得，沒有辦法完整帶走；
// 對一個會長期累積工作記錄的 Agent 來說，這是基本能力。
type SessionExport struct {
	Session  domain.Session        `json:"session"`
	Project  *domain.Project       `json:"project,omitempty"`
	Plans    []domain.Plan         `json:"plans,omitempty"`
	Messages []domain.Message      `json:"messages"`
	Entries  []domain.SessionEntry `json:"entries,omitempty"`
	Runs     []domain.Run          `json:"runs,omitempty"`
	Metadata map[string]any        `json:"metadata,omitempty"`
}

// ExportSession 匯出整個 Session。includeEntries 為 false 時只帶訊息，
// 適合分享；為 true 時附上完整 transcript，適合稽核與問題重現。
func (s *Service) ExportSession(ctx context.Context, sessionID string, includeEntries bool) (SessionExport, error) {
	engine, session, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return SessionExport{}, err
	}
	messages, err := engine.ListMessages(ctx, session.ID)
	if err != nil {
		return SessionExport{}, err
	}
	runs, err := s.runs.List(ctx, session.ID)
	if err != nil {
		return SessionExport{}, err
	}
	export := SessionExport{
		Session:  session,
		Messages: messages,
		Runs:     runs,
		Metadata: map[string]any{"exported_at": s.now().UTC()},
	}
	if session.ProjectID != "" {
		if project, projectErr := s.projects.Get(ctx, session.ProjectID); projectErr == nil {
			export.Project = &project
		}
	}
	if plans, planErr := s.plans.List(ctx, session.ID); planErr == nil {
		export.Plans = plans
	} else {
		return SessionExport{}, planErr
	}
	if includeEntries {
		entries, entriesErr := engine.ListEntries(ctx, session.ID)
		if entriesErr != nil {
			return SessionExport{}, entriesErr
		}
		export.Entries = entries
	}
	return export, nil
}

// Markdown 產生人類可讀的逐字稿。工具結果會標示錯誤狀態，
// 因為「工具失敗但對話看起來順利」正是閱讀記錄時最需要看見的事。
func (e SessionExport) Markdown() string {
	var builder strings.Builder
	title := strings.TrimSpace(e.Session.Title)
	if title == "" {
		title = e.Session.ID
	}
	fmt.Fprintf(&builder, "# %s\n\n", title)
	fmt.Fprintf(&builder, "- Session: `%s`\n", e.Session.ID)
	fmt.Fprintf(&builder, "- Agent: `%s`\n", e.Session.AgentID)
	if e.Project != nil {
		fmt.Fprintf(&builder, "- Project: %s\n", e.Project.Name)
	}
	if e.Session.Model != "" {
		fmt.Fprintf(&builder, "- Model: `%s`\n", e.Session.Model)
	}
	for index, plan := range e.Plans {
		fmt.Fprintf(&builder, "## 計畫 %d：%s\n\n", index+1, plan.Title)
		for _, step := range plan.Steps {
			marker := " "
			if step.Status == domain.PlanStepStatusCompleted || step.Status == domain.PlanStepStatusSkipped {
				marker = "x"
			}
			fmt.Fprintf(&builder, "- [%s] %s（%s）\n", marker, step.Title, step.Status)
		}
		builder.WriteString("\n")
	}
	fmt.Fprintf(&builder, "- Created: %s\n\n", e.Session.CreatedAt.Format("2006-01-02 15:04:05 MST"))
	for _, message := range e.Messages {
		switch strings.ToLower(message.Role) {
		case "user":
			fmt.Fprintf(&builder, "## 使用者\n\n%s\n\n", strings.TrimSpace(message.Content))
		case "assistant":
			if content := strings.TrimSpace(message.Content); content != "" {
				fmt.Fprintf(&builder, "## Agent\n\n%s\n\n", content)
			}
			for _, call := range message.ToolCalls {
				fmt.Fprintf(&builder, "> 呼叫工具 `%s`（%s）\n\n", call.Name, call.ID)
			}
		case "tool":
			status := "結果"
			if message.IsError {
				status = "失敗"
			}
			fmt.Fprintf(&builder, "> 工具 `%s` %s：\n\n```\n%s\n```\n\n", message.ToolName, status, strings.TrimSpace(message.Content))
		}
	}
	return builder.String()
}

// ListRunsByStatus 讓呼叫端直接取得特定狀態的 Run。
// 沒有這個過濾時，UI 要找出等待人工核准的 Run 只能把全部 Run 拉回來自己掃。
func (s *Service) ListRunsByStatus(ctx context.Context, sessionID string, statuses []domain.RunStatus) ([]domain.Run, error) {
	values, err := s.runs.List(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		return nil, err
	}
	if len(statuses) == 0 {
		return values, nil
	}
	wanted := make(map[domain.RunStatus]struct{}, len(statuses))
	for _, status := range statuses {
		wanted[status] = struct{}{}
	}
	result := make([]domain.Run, 0, len(values))
	for _, value := range values {
		if _, ok := wanted[value.Status]; ok {
			result = append(result, value)
		}
	}
	return result, nil
}
