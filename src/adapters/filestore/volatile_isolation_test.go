package filestore

import (
	"AgenticService/src/domain"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// missingRoots 模擬「RAM disk 沒掛載」：任何 ID 都解析不到根目錄。
type missingRoots struct{}

func (missingRoots) RootFor(string) string     { return "" }
func (missingRoots) AdditionalRoots() []string { return nil }

// 磁碟不在時必須擋下，不能悄悄退回 dataDir——那等於把隔離對話寫上硬碟，
// 而且沒有任何錯誤訊息，使用者要到重啟後才會發現資料還在。
func TestVolatileRootMissingFailsClosed(t *testing.T) {
	dataDir := t.TempDir()
	ctx := context.Background()
	volatileSession := domain.NewEphemeralSessionID("project_abc123")
	volatileRun := domain.NewRunIDForSession(volatileSession)

	sessions, _ := NewSessionRepository(dataDir)
	plans, _ := NewPlanRepository(dataDir)
	attachments, _ := NewAttachmentRepository(dataDir)
	events, _ := NewRunEventRepository(dataDir)
	sessions.SetProjectRoots(missingRoots{})
	plans.SetProjectRoots(missingRoots{})
	attachments.SetProjectRoots(missingRoots{})
	events.SetProjectRoots(missingRoots{})

	before := len(listTree(t, dataDir))

	sessions.SetSessionIDFactory(func(string) string { return volatileSession })
	if _, err := sessions.Create(ctx, "agent", domain.CreateSessionInput{ProjectID: "project_abc123"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("建立對話應被擋下並回 ErrNotFound，得到 %v", err)
	}
	plan, err := domain.NewPlan(volatileSession, domain.CreatePlanInput{
		Title: "計畫", Steps: []domain.CreatePlanStepInput{{Title: "步驟", Verification: "完成"}},
	}, time.Now())
	if err != nil {
		t.Fatalf("new plan: %v", err)
	}
	if _, err := plans.Create(ctx, plan); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("建立計畫應被擋下，得到 %v", err)
	}
	if _, err := attachments.Save(ctx, volatileSession, "note.txt", "text/plain", strings.NewReader("內容"), 1024); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("儲存附件應被擋下，得到 %v", err)
	}
	if err := events.Append(ctx, domain.Event{
		ID: domain.NewID("event"), RunID: volatileRun, Sequence: 1, Type: "run.completed",
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("寫入事件應被擋下，得到 %v", err)
	}

	if after := listTree(t, dataDir); len(after) != before {
		t.Fatalf("擋下之後 dataDir 仍長出東西：\n%s", strings.Join(after, "\n"))
	}
	// 一般對話完全不受影響：解析不出歸屬的 ID 照舊使用預設根。
	sessions.SetSessionIDFactory(nil)
	if _, err := sessions.Create(ctx, "agent", domain.CreateSessionInput{Title: "一般對話"}); err != nil {
		t.Fatalf("一般對話不該受影響：%v", err)
	}
}

// 舊版本在磁碟未掛載時會把隔離對話寫進 dataDir。那些目錄現在 List 看不到
// （解析會失敗），所以必須有專門的清掃，否則它們會永遠留在硬碟上。
func TestPurgeVolatileResidueRemovesLeakedSessions(t *testing.T) {
	dataDir := t.TempDir()
	sessions, _ := NewSessionRepository(dataDir)
	root := filepath.Join(dataDir, "sessions")
	leaked := domain.NewEphemeralSessionID("project_abc123")
	for _, name := range []string{leaked, "session_normal"} {
		if err := os.MkdirAll(filepath.Join(root, name, "workspace"), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}

	removed, err := sessions.PurgeVolatileResidue()
	if err != nil || removed != 1 {
		t.Fatalf("應剛好清掉一個殘留目錄：removed=%d err=%v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(root, leaked)); !os.IsNotExist(err) {
		t.Fatalf("殘留目錄應被刪除：%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "session_normal")); err != nil {
		t.Fatalf("一般對話不該被誤刪：%v", err)
	}
}

// 揮發的 run 根本不會寫進 runs.json，卻仍佔用保留額度的話，使用者在隔離專案
// 跑得越多，一般專案的歷史就消失得越快——而且完全看不出原因。
func TestVolatileRunsDoNotEvictPersistentRuns(t *testing.T) {
	dataDir := t.TempDir()
	runs, err := NewRunRepository(dataDir)
	if err != nil {
		t.Fatalf("new run repository: %v", err)
	}
	runs.retention = 3
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour)

	persistent := []string{}
	for index := 0; index < 3; index++ {
		id := domain.NewID("run")
		persistent = append(persistent, id)
		if err := runs.Save(ctx, domain.Run{
			ID: id, SessionID: "session_normal", Status: domain.RunStatusCompleted,
			CreatedAt: base.Add(time.Duration(index) * time.Minute),
		}); err != nil {
			t.Fatalf("save persistent run: %v", err)
		}
	}
	volatileSession := domain.NewEphemeralSessionID("project_abc123")
	for index := 0; index < 20; index++ {
		if err := runs.Save(ctx, domain.Run{
			ID: domain.NewRunIDForSession(volatileSession), SessionID: volatileSession,
			Status: domain.RunStatusCompleted, CreatedAt: base.Add(time.Duration(index+10) * time.Minute),
		}); err != nil {
			t.Fatalf("save volatile run: %v", err)
		}
	}

	for _, id := range persistent {
		if _, err := runs.Get(ctx, id); err != nil {
			t.Fatalf("持久 run 不該被揮發 run 擠掉：%s %v", id, err)
		}
	}
	// 揮發那組仍要有上限，否則長時間執行會無限成長。
	remaining := 0
	for _, run := range mustList(t, ctx, runs, volatileSession) {
		if run.SessionID == volatileSession {
			remaining++
		}
	}
	if remaining > runs.retention {
		t.Fatalf("揮發 run 也該受上限約束：%d > %d", remaining, runs.retention)
	}
}

func TestVolatileNotificationsDoNotEvictPersistentOnes(t *testing.T) {
	dataDir := t.TempDir()
	notifications, err := NewNotificationRepository(dataDir)
	if err != nil {
		t.Fatalf("new notification repository: %v", err)
	}
	volatileSession := domain.NewEphemeralSessionID("project_abc123")
	base := time.Now().UTC().Add(-time.Hour)
	persistent := domain.Notification{
		ID: "notification_keep", Title: "保留", Message: "保留",
		SessionID: "session_normal", CreatedAt: base,
	}
	notifications.items[persistent.ID] = persistent
	for index := 0; index <= maxStoredNotifications; index++ {
		item := domain.Notification{
			ID: domain.NewID("notification"), Title: "揮發", Message: "揮發",
			SessionID: volatileSession, CreatedAt: base.Add(time.Duration(index+1) * time.Second),
		}
		notifications.items[item.ID] = item
	}

	notifications.trimLocked(true)
	notifications.trimLocked(false)

	if _, ok := notifications.items[persistent.ID]; !ok {
		t.Fatal("最舊的持久通知被揮發通知擠掉了")
	}
	volatileCount := 0
	for _, item := range notifications.items {
		if volatileNotification(item) {
			volatileCount++
		}
	}
	if volatileCount > maxStoredNotifications {
		t.Fatalf("揮發通知也該受上限約束：%d", volatileCount)
	}
}

func mustList(t *testing.T, ctx context.Context, runs *RunRepository, sessionID string) []domain.Run {
	t.Helper()
	values, err := runs.List(ctx, sessionID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	return values
}

// listTree 列出整棵樹的相對路徑，用來確認「被擋下」真的沒有留下任何檔案。
func listTree(t *testing.T, root string) []string {
	t.Helper()
	entries := []string{}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if relative != "." {
			entries = append(entries, relative)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return entries
}
