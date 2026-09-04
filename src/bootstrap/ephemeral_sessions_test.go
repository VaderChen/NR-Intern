package bootstrap

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestPurgeEphemeralProjectSessionsKeepsProjectAndRegularSessions(t *testing.T) {
	dataDir := t.TempDir()
	sessions, _ := filestore.NewSessionRepository(dataDir)
	plans, _ := filestore.NewPlanRepository(dataDir)
	runs, _ := filestore.NewRunRepository(dataDir)
	events, _ := filestore.NewRunEventRepository(dataDir)
	notifications, _ := filestore.NewNotificationRepository(dataDir)
	ctx := context.Background()
	ephemeral, err := sessions.Create(ctx, "agent", domain.CreateSessionInput{WorkspaceID: "workspace_1", ProjectID: "project_ram", Title: "暫存對話"})
	if err != nil {
		t.Fatalf("create ephemeral session: %v", err)
	}
	regular, err := sessions.Create(ctx, "agent", domain.CreateSessionInput{WorkspaceID: "workspace_1", ProjectID: "project_disk", Title: "一般對話"})
	if err != nil {
		t.Fatalf("create regular session: %v", err)
	}
	if err := runs.Save(ctx, domain.Run{ID: "run_ram", SessionID: ephemeral.ID, Status: domain.RunStatusCompleted}); err != nil {
		t.Fatalf("save ephemeral run: %v", err)
	}
	if err := runs.Save(ctx, domain.Run{ID: "run_disk", SessionID: regular.ID, Status: domain.RunStatusCompleted}); err != nil {
		t.Fatalf("save regular run: %v", err)
	}
	if err := events.Append(ctx, domain.Event{ID: "event_ram", RunID: "run_ram", Sequence: 1, Type: "run.completed"}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	plan, err := domain.NewPlan(ephemeral.ID, domain.CreatePlanInput{
		Title: "暫存計畫", Steps: []domain.CreatePlanStepInput{{Title: "步驟", Verification: "完成"}},
	}, time.Now())
	if err != nil {
		t.Fatalf("new plan: %v", err)
	}
	if _, err := plans.Create(ctx, plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	if err := notifications.Save(ctx, domain.Notification{ID: "notification_ram", Title: "完成", Message: "完成", SessionID: ephemeral.ID, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("save notification: %v", err)
	}
	projects := []domain.Project{
		{ID: "project_ram", Ephemeral: true, RAMDiskSizeMB: 512},
		{ID: "project_disk"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := purgeEphemeralProjectSessions(ctx, projects, sessions, plans, runs, events, notifications, logger); err != nil {
		t.Fatalf("purgeEphemeralProjectSessions: %v", err)
	}
	if _, err := sessions.Get(ctx, ephemeral.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ephemeral session still exists: %v", err)
	}
	if _, err := runs.Get(ctx, "run_ram"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ephemeral run still exists: %v", err)
	}
	if _, err := sessions.Get(ctx, regular.ID); err != nil {
		t.Fatalf("regular session was removed: %v", err)
	}
	if _, err := runs.Get(ctx, "run_disk"); err != nil {
		t.Fatalf("regular run was removed: %v", err)
	}
	values, err := notifications.List(ctx, 100, false)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(values) != 0 {
		t.Fatalf("ephemeral notifications remain: %+v", values)
	}
}
