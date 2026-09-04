package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
)

// snapshotTree 列出整棵目錄樹的相對路徑，含檔案內容指紋。
//
// 逐一檢查「想得到的」儲存位置沒有意義：想得到的都已經處理了，出事的永遠是
// 沒想到的那一個。整棵樹比對才抓得到新加的儲存忘了納入清理的情況。
func snapshotTree(t *testing.T, root string) []string {
	t.Helper()
	entries := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if relative == "." {
			return nil
		}
		if entry.IsDir() {
			entries = append(entries, "d "+filepath.ToSlash(relative))
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		entries = append(entries, "f "+filepath.ToSlash(relative)+" "+string(rune(len(data)%26+'a')))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(entries)
	return entries
}

// 清理後，dataDir 底下不得殘留任何屬於記憶體隔離 Project 的痕跡。
//
// 這是這個功能唯一真正重要的驗收：使用者信任的是「重啟後那些對話不存在」，
// 而不是「我們記得清掉的那幾個檔案不存在」。
func TestEphemeralPurgeLeavesNoResidueInDataDir(t *testing.T) {
	dataDir := t.TempDir()
	ctx := context.Background()
	sessions, _ := filestore.NewSessionRepository(dataDir)
	plans, _ := filestore.NewPlanRepository(dataDir)
	runs, _ := filestore.NewRunRepository(dataDir)
	events, _ := filestore.NewRunEventRepository(dataDir)
	notifications, _ := filestore.NewNotificationRepository(dataDir)

	// 先建立一個一般 Project 的對話，並在這個狀態取快照。
	// 清理過後整棵樹必須回到這裡——不能多，也不能少。
	regular, err := sessions.Create(ctx, "agent", domain.CreateSessionInput{
		WorkspaceID: "workspace_1", ProjectID: "project_disk", Title: "一般對話",
	})
	if err != nil {
		t.Fatalf("create regular session: %v", err)
	}
	seedSessionData(t, ctx, regular.ID, "run_disk", sessions, plans, runs, events, notifications)
	baseline := snapshotTree(t, dataDir)

	// 再加入記憶體隔離 Project 的對話與所有周邊資料。
	ephemeral, err := sessions.Create(ctx, "agent", domain.CreateSessionInput{
		WorkspaceID: "workspace_1", ProjectID: "project_ram", Title: "暫存對話",
	})
	if err != nil {
		t.Fatalf("create ephemeral session: %v", err)
	}
	seedSessionData(t, ctx, ephemeral.ID, "run_ram", sessions, plans, runs, events, notifications)
	if len(snapshotTree(t, dataDir)) <= len(baseline) {
		t.Fatal("測試資料沒有真的寫進 dataDir，後面的比對就沒有意義")
	}

	projects := []domain.Project{
		{ID: "project_ram", Ephemeral: true, RAMDiskSizeMB: 512},
		{ID: "project_disk"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := purgeEphemeralProjectSessions(ctx, projects, sessions, plans, runs, events, notifications, logger); err != nil {
		t.Fatalf("purge: %v", err)
	}

	after := snapshotTree(t, dataDir)
	if difference := treeDifference(baseline, after); len(difference) > 0 {
		t.Fatalf("清理後 dataDir 與基準不一致，殘留或誤刪：\n%s", strings.Join(difference, "\n"))
	}
	// 直接掃內容確認 session id 沒有以任何形式留在檔案裡（例如索引、清單）。
	if found := grepTree(t, dataDir, ephemeral.ID); len(found) > 0 {
		t.Fatalf("dataDir 仍有檔案含有隔離 Session 的 ID：\n%s", strings.Join(found, "\n"))
	}
}

func seedSessionData(
	t *testing.T, ctx context.Context, sessionID, runID string,
	sessions *filestore.SessionRepository, plans *filestore.PlanRepository,
	runs *filestore.RunRepository, events *filestore.RunEventRepository,
	notifications *filestore.NotificationRepository,
) {
	t.Helper()
	message := domain.Message{ID: domain.NewID("msg"), Role: "user", Content: "測試內容"}
	if _, err := sessions.AppendEntry(ctx, sessionID, domain.SessionEntry{
		Type: domain.SessionEntryMessage, Message: &message,
	}); err != nil {
		t.Fatalf("append entry: %v", err)
	}
	if err := runs.Save(ctx, domain.Run{
		ID: runID, SessionID: sessionID, Status: domain.RunStatusCompleted, Input: "測試提問",
	}); err != nil {
		t.Fatalf("save run: %v", err)
	}
	if err := events.Append(ctx, domain.Event{
		ID: "event_" + runID, RunID: runID, Sequence: 1, Type: "run.completed",
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	plan, err := domain.NewPlan(sessionID, domain.CreatePlanInput{
		Title: "計畫", Steps: []domain.CreatePlanStepInput{{Title: "步驟", Verification: "完成"}},
	}, time.Now())
	if err != nil {
		t.Fatalf("new plan: %v", err)
	}
	if _, err := plans.Create(ctx, plan); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if err := notifications.Save(ctx, domain.Notification{
		ID: "notification_" + runID, Title: "完成", Message: "完成", SessionID: sessionID, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("save notification: %v", err)
	}
}

// treeDifference 回傳兩份快照的雙向差異，讓失敗訊息直接指出是殘留還是誤刪。
func treeDifference(baseline, after []string) []string {
	inBaseline := map[string]bool{}
	for _, entry := range baseline {
		inBaseline[entry] = true
	}
	inAfter := map[string]bool{}
	for _, entry := range after {
		inAfter[entry] = true
	}
	difference := []string{}
	for _, entry := range after {
		if !inBaseline[entry] {
			difference = append(difference, "殘留 "+entry)
		}
	}
	for _, entry := range baseline {
		if !inAfter[entry] {
			difference = append(difference, "誤刪 "+entry)
		}
	}
	sort.Strings(difference)
	return difference
}

// grepTree 找出內容含有指定字串的檔案。
func grepTree(t *testing.T, root, needle string) []string {
	t.Helper()
	found := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), needle) {
			relative, _ := filepath.Rel(root, path)
			found = append(found, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("grep %s: %v", root, err)
	}
	sort.Strings(found)
	return found
}
