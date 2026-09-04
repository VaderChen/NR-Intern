package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
)

// 這是「執行期間就不落地」的核心驗收：隔離專案的對話目錄必須建在 RAM disk 上，
// dataDir 底下不該出現任何屬於它的東西。
func TestEphemeralSessionDirectoryNeverTouchesDataDir(t *testing.T) {
	dataDir := t.TempDir()
	volatileRoot := t.TempDir()
	sessions, err := filestore.NewSessionRepository(dataDir)
	if err != nil {
		t.Fatalf("new session repository: %v", err)
	}
	// 用固定的假 pool 取代真的 RAM disk：這個測試要驗證的是接線，
	// 不是磁碟掛載本身（那有 ramdisk_test.go 涵蓋）。
	projectID := "project_abc123"
	sessions.SetSessionRoots(fixedRoots{code: "abc123", root: volatileRoot})
	sessions.SetSessionIDFactory(func(requested string) string {
		if requested != projectID {
			return ""
		}
		return domain.NewEphemeralSessionID(projectID)
	})

	ctx := context.Background()
	volatile, err := sessions.Create(ctx, "agent", domain.CreateSessionInput{
		Title: "隔離對話", ProjectID: projectID,
	})
	if err != nil {
		t.Fatalf("create volatile session: %v", err)
	}
	normal, err := sessions.Create(ctx, "agent", domain.CreateSessionInput{Title: "一般對話"})
	if err != nil {
		t.Fatalf("create normal session: %v", err)
	}

	if !strings.HasPrefix(volatile.ID, "session_v") {
		t.Fatalf("隔離對話的 ID 沒有帶歸屬：%s", volatile.ID)
	}
	if _, err := os.Stat(filepath.Join(volatileRoot, volatile.ID)); err != nil {
		t.Fatalf("隔離對話應建在 RAM disk 根：%v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "sessions", volatile.ID)); err == nil {
		t.Fatal("隔離對話不該在 dataDir 留下目錄")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "sessions", normal.ID)); err != nil {
		t.Fatalf("一般對話仍應落在 dataDir：%v", err)
	}

	// 寫入 transcript 後再確認一次：dataDir 不能因為對話進行而長出東西。
	message := domain.Message{ID: "m1", Role: "user", Content: "祕密內容"}
	if _, err := sessions.AppendEntry(ctx, volatile.ID, domain.SessionEntry{
		Type: domain.SessionEntryMessage, Message: &message,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if found := grepDir(t, filepath.Join(dataDir, "sessions"), "祕密內容"); len(found) > 0 {
		t.Fatalf("對話內容寫進了 dataDir：%v", found)
	}

	// 兩邊都要讀得到，而且 List 要同時涵蓋。
	listed, err := sessions.List(ctx, "agent")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	seen := map[string]bool{}
	for _, session := range listed {
		seen[session.ID] = true
	}
	if !seen[volatile.ID] || !seen[normal.ID] {
		t.Fatalf("List 應同時涵蓋兩邊：%v", seen)
	}
}

// RAM disk 不存在時（重開機後必然如此），隔離對話應該自然變成找不到，
// 不必再靠啟動時的補救清理。
func TestEphemeralSessionDisappearsWithoutItsDisk(t *testing.T) {
	dataDir := t.TempDir()
	volatileRoot := t.TempDir()
	sessions, err := filestore.NewSessionRepository(dataDir)
	if err != nil {
		t.Fatalf("new session repository: %v", err)
	}
	projectID := "project_abc123"
	sessions.SetSessionRoots(fixedRoots{code: "abc123", root: volatileRoot})
	sessions.SetSessionIDFactory(func(string) string { return domain.NewEphemeralSessionID(projectID) })
	ctx := context.Background()
	volatile, err := sessions.Create(ctx, "agent", domain.CreateSessionInput{ProjectID: projectID})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 模擬重開機：磁碟沒了，池子裡也沒有這個專案。
	if err := os.RemoveAll(volatileRoot); err != nil {
		t.Fatalf("remove volatile root: %v", err)
	}
	sessions.SetSessionRoots(fixedRoots{code: "abc123", root: ""})

	if _, err := sessions.Get(ctx, volatile.ID); err == nil {
		t.Fatal("磁碟消失後不該還讀得到隔離對話")
	}
	listed, err := sessions.List(ctx, "agent")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, session := range listed {
		if session.ID == volatile.ID {
			t.Fatal("磁碟消失後不該還列得出隔離對話")
		}
	}
}

type fixedRoots struct {
	code string
	root string
}

func (f fixedRoots) RootFor(sessionID string) string {
	if domain.EphemeralProjectCodeFromSessionID(sessionID) == f.code {
		return f.root
	}
	return ""
}

func (f fixedRoots) AdditionalRoots() []string {
	if f.root == "" {
		return nil
	}
	return []string{f.root}
}

func grepDir(t *testing.T, root, needle string) []string {
	t.Helper()
	found := []string{}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr == nil && strings.Contains(string(data), needle) {
			found = append(found, path)
		}
		return nil
	})
	return found
}

// 計畫的內容含步驟敘述與驗證條件，與 transcript 同樣不該留在硬碟。
func TestEphemeralPlansFollowTheirSession(t *testing.T) {
	dataDir := t.TempDir()
	volatileRoot := t.TempDir()
	plans, err := filestore.NewPlanRepository(dataDir)
	if err != nil {
		t.Fatalf("new plan repository: %v", err)
	}
	plans.SetSessionRoots(fixedRoots{code: "abc123", root: volatileRoot})
	ctx := context.Background()

	volatileSession := domain.NewEphemeralSessionID("project_abc123")
	for _, sessionID := range []string{volatileSession, "session_normal"} {
		plan, err := domain.NewPlan(sessionID, domain.CreatePlanInput{
			Title: "祕密計畫", Steps: []domain.CreatePlanStepInput{{Title: "祕密步驟", Verification: "完成"}},
		}, time.Now())
		if err != nil {
			t.Fatalf("new plan: %v", err)
		}
		if _, err := plans.Create(ctx, plan); err != nil {
			t.Fatalf("create plan for %s: %v", sessionID, err)
		}
	}

	if found := grepDir(t, filepath.Join(volatileRoot, "plans"), "祕密步驟"); len(found) == 0 {
		t.Fatal("隔離對話的計畫應寫在 RAM disk 上")
	}
	// 一般對話的計畫仍在 dataDir，而隔離對話的內容不能出現在那裡。
	if found := grepDir(t, filepath.Join(dataDir, "plans"), "祕密步驟"); len(found) != 1 {
		t.Fatalf("dataDir 的計畫數量不對：%v（應只有一般對話那一份）", found)
	}
	listed, err := plans.List(ctx, volatileSession)
	if err != nil || len(listed) != 1 {
		t.Fatalf("隔離對話的計畫應讀得回來：%d 筆 err=%v", len(listed), err)
	}
}

// 沙箱不能看到後端資料。沙箱若直接用磁碟根目錄，Agent 就能讀寫自己的
// transcript 與計畫，隔離也就失去意義。
func TestRAMDiskSeparatesWorkspaceFromStore(t *testing.T) {
	disk := &RAMDisk{root: t.TempDir()}
	if err := disk.prepareLayout(); err != nil {
		t.Fatalf("prepare layout: %v", err)
	}
	workspace := disk.WorkspaceRoot()
	store := disk.StoreRoot()
	if workspace == "" || store == "" || workspace == store {
		t.Fatalf("兩個目錄必須存在且不同：workspace=%q store=%q", workspace, store)
	}
	if strings.HasPrefix(store, workspace+string(filepath.Separator)) {
		t.Fatalf("後端資料區 %q 不能落在沙箱 %q 底下", store, workspace)
	}
	for _, path := range []string{workspace, store} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("%q 應該已建立為目錄：%v", path, err)
		}
	}
}
