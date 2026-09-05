package bootstrap

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"AgenticService/src/memory"
)

const volatileProjectID = "project_abc123"

// 附件是使用者自己上傳的檔案。不分流的話，它會是隔離對話唯一整份留在硬碟上的內容。
func TestEphemeralAttachmentsFollowTheirSession(t *testing.T) {
	dataDir := t.TempDir()
	volatileRoot := t.TempDir()
	attachments, err := filestore.NewAttachmentRepository(dataDir)
	if err != nil {
		t.Fatalf("new attachment repository: %v", err)
	}
	attachments.SetProjectRoots(fixedRoots{code: "abc123", root: volatileRoot})
	ctx := context.Background()

	volatileSession := domain.NewEphemeralSessionID(volatileProjectID)
	for _, sessionID := range []string{volatileSession, "session_normal"} {
		if _, err := attachments.Save(ctx, sessionID, "note.txt", "text/plain", strings.NewReader("祕密附件內容"), 1024); err != nil {
			t.Fatalf("save attachment for %s: %v", sessionID, err)
		}
	}

	if found := grepDir(t, filepath.Join(volatileRoot, volatileSession), "祕密附件內容"); len(found) != 1 {
		t.Fatalf("隔離對話的附件應寫在 RAM disk 上：%v", found)
	}
	if found := grepDir(t, filepath.Join(dataDir, "sessions", volatileSession), "祕密附件內容"); len(found) != 0 {
		t.Fatalf("隔離對話的附件不該出現在 dataDir：%v", found)
	}
	// 一般對話完全不受影響。
	if found := grepDir(t, filepath.Join(dataDir, "sessions", "session_normal"), "祕密附件內容"); len(found) != 1 {
		t.Fatalf("一般對話的附件仍應落在 dataDir：%v", found)
	}
}

// 事件檔是 run 期間的逐筆完整紀錄，而且讀取時手上只有 Run ID——
// 這一條同時驗證 Run ID 編碼與事件分流兩件事。
func TestEphemeralRunEventsFollowTheirProject(t *testing.T) {
	dataDir := t.TempDir()
	volatileRoot := t.TempDir()
	events, err := filestore.NewRunEventRepository(dataDir)
	if err != nil {
		t.Fatalf("new run event repository: %v", err)
	}
	events.SetProjectRoots(fixedRoots{code: "abc123", root: volatileRoot})
	ctx := context.Background()

	volatileSession := domain.NewEphemeralSessionID(volatileProjectID)
	volatileRun := domain.NewRunIDForSession(volatileSession)
	normalRun := domain.NewRunIDForSession("session_normal")
	if !strings.HasPrefix(volatileRun, "run_v") {
		t.Fatalf("隔離對話的 Run ID 沒有帶歸屬：%s", volatileRun)
	}
	if strings.HasPrefix(normalRun, "run_v") {
		t.Fatalf("一般對話的 Run ID 不該帶歸屬：%s", normalRun)
	}

	for _, runID := range []string{volatileRun, normalRun} {
		if err := events.Append(ctx, domain.Event{
			ID: domain.NewID("event"), Type: "message.delta", RunID: runID,
			SessionID: volatileSession, Sequence: 1, CreatedAt: time.Now().UTC(),
			Payload: map[string]any{"text": "祕密輸出"},
		}); err != nil {
			t.Fatalf("append event for %s: %v", runID, err)
		}
	}

	if found := grepDir(t, filepath.Join(volatileRoot, "runs", "events"), "祕密輸出"); len(found) != 1 {
		t.Fatalf("隔離 run 的事件應寫在 RAM disk 上：%v", found)
	}
	if found := grepDir(t, filepath.Join(dataDir, "runs", "events"), "祕密輸出"); len(found) != 1 {
		t.Fatalf("dataDir 應只有一般 run 的事件：%v", found)
	}
	// 分流之後仍要讀得回來，否則 UI 的事件重播會空掉。
	listed, err := events.List(ctx, volatileRun, 0)
	if err != nil || len(listed) != 1 {
		t.Fatalf("隔離 run 的事件應讀得回來：%d 筆 err=%v", len(listed), err)
	}
}

// runs.json 是單一檔案、每次 Save 整份重寫，沒辦法按根目錄分流。
// Run.Input 存的是使用者提問原文，所以改成在寫入時濾掉。
func TestEphemeralRunsStayOutOfRunsFile(t *testing.T) {
	dataDir := t.TempDir()
	runs, err := filestore.NewRunRepository(dataDir)
	if err != nil {
		t.Fatalf("new run repository: %v", err)
	}
	ctx := context.Background()

	volatileSession := domain.NewEphemeralSessionID(volatileProjectID)
	volatileRun := domain.Run{
		ID: domain.NewRunIDForSession(volatileSession), SessionID: volatileSession,
		Status: domain.RunStatusCompleted, Input: "祕密提問", CreatedAt: time.Now().UTC(),
	}
	normalRun := domain.Run{
		ID: domain.NewID("run"), SessionID: "session_normal",
		Status: domain.RunStatusCompleted, Input: "一般提問", CreatedAt: time.Now().UTC(),
	}
	for _, run := range []domain.Run{volatileRun, normalRun} {
		if err := runs.Save(ctx, run); err != nil {
			t.Fatalf("save run %s: %v", run.ID, err)
		}
	}

	if found := grepDir(t, filepath.Join(dataDir, "runs"), "祕密提問"); len(found) != 0 {
		t.Fatalf("隔離對話的 run 不該落地：%v", found)
	}
	if found := grepDir(t, filepath.Join(dataDir, "runs"), "一般提問"); len(found) != 1 {
		t.Fatalf("一般對話的 run 仍應落地：%v", found)
	}
	// 本次執行期間的讀取完全不受影響——濾掉的只有寫入。
	if got, err := runs.Get(ctx, volatileRun.ID); err != nil || got.Input != "祕密提問" {
		t.Fatalf("隔離 run 在記憶體中應完整可讀：%+v err=%v", got, err)
	}
	// 重新開啟等同重啟：RAM disk 已經不在，這筆 run 也不該回來。
	reopened, err := filestore.NewRunRepository(dataDir)
	if err != nil {
		t.Fatalf("reopen run repository: %v", err)
	}
	if _, err := reopened.Get(ctx, volatileRun.ID); err == nil {
		t.Fatal("重啟後不該還讀得到隔離對話的 run")
	}
	if _, err := reopened.Get(ctx, normalRun.ID); err != nil {
		t.Fatalf("重啟後一般 run 應仍在：%v", err)
	}
}

// 通知的 Title 與 Message 帶的是該次對話的內容摘要，處理方式與 runs.json 相同。
func TestEphemeralNotificationsStayOutOfNotificationsFile(t *testing.T) {
	dataDir := t.TempDir()
	notifications, err := filestore.NewNotificationRepository(dataDir)
	if err != nil {
		t.Fatalf("new notification repository: %v", err)
	}
	ctx := context.Background()

	volatileSession := domain.NewEphemeralSessionID(volatileProjectID)
	for _, item := range []domain.Notification{
		{ID: domain.NewID("notification"), Type: "run.completed", Title: "祕密標題", Message: "祕密訊息", SessionID: volatileSession, CreatedAt: time.Now().UTC()},
		{ID: domain.NewID("notification"), Type: "run.completed", Title: "一般標題", Message: "一般訊息", SessionID: "session_normal", CreatedAt: time.Now().UTC()},
	} {
		if err := notifications.Save(ctx, item); err != nil {
			t.Fatalf("save notification %s: %v", item.ID, err)
		}
	}

	if found := grepDir(t, filepath.Join(dataDir, "notifications"), "祕密訊息"); len(found) != 0 {
		t.Fatalf("隔離對話的通知不該落地：%v", found)
	}
	if found := grepDir(t, filepath.Join(dataDir, "notifications"), "一般訊息"); len(found) != 1 {
		t.Fatalf("一般對話的通知仍應落地：%v", found)
	}
	listed, err := notifications.List(ctx, 10, false)
	if err != nil || len(listed) != 2 {
		t.Fatalf("兩則通知在本次執行期間都應讀得到：%d 筆 err=%v", len(listed), err)
	}
}

// 回憶空間是持久化設計，與隔離專案的前提直接衝突：模型主動記下的內容
// 若寫進 memories.json，就會是隔離對話唯一撐過重啟的痕跡。
func TestEphemeralMemoriesStayOutOfMemoryFile(t *testing.T) {
	dataDir := t.TempDir()
	memories, err := filestore.NewMemoryRepository(dataDir)
	if err != nil {
		t.Fatalf("new memory repository: %v", err)
	}
	ctx := context.Background()

	volatileSession := domain.NewEphemeralSessionID(volatileProjectID)
	volatile, err := memories.Remember(ctx, domain.RememberMemoryInput{
		Scope: "project:" + volatileProjectID, Kind: domain.MemoryKindFact,
		Content: "祕密事實", Confidence: 0.9, SourceSessionID: volatileSession,
	})
	if err != nil {
		t.Fatalf("remember volatile: %v", err)
	}
	if _, err := memories.Remember(ctx, domain.RememberMemoryInput{
		Scope: "agent-default", Kind: domain.MemoryKindFact,
		Content: "一般事實", Confidence: 0.9, SourceSessionID: "session_normal",
	}); err != nil {
		t.Fatalf("remember normal: %v", err)
	}

	if found := grepDir(t, filepath.Join(dataDir, "memory"), "祕密事實"); len(found) != 0 {
		t.Fatalf("隔離對話的記憶不該落地：%v", found)
	}
	if found := grepDir(t, filepath.Join(dataDir, "memory"), "一般事實"); len(found) != 1 {
		t.Fatalf("一般對話的記憶仍應落地：%v", found)
	}
	// 本次執行期間照常召回。
	if got, err := memories.Get(ctx, "project:"+volatileProjectID, volatile.ID); err != nil || got.Content != "祕密事實" {
		t.Fatalf("隔離記憶在記憶體中應完整可讀：%+v err=%v", got, err)
	}
	reopened, err := filestore.NewMemoryRepository(dataDir)
	if err != nil {
		t.Fatalf("reopen memory repository: %v", err)
	}
	if _, err := reopened.Get(ctx, "project:"+volatileProjectID, volatile.ID); err == nil {
		t.Fatal("重啟後不該還讀得到隔離對話的記憶")
	}
}

// 這是「寫入時過濾」能成立的前提：隔離對話不能與一般對話共用 scope。
// 共用的話，內容相同的記憶會被就地改寫成隔離來源，然後在寫入時被整筆濾掉——
// 一般對話原本記住的東西會憑空消失。
func TestEphemeralSessionsGetTheirOwnMemoryScope(t *testing.T) {
	volatileSession := domain.Session{
		ID: domain.NewEphemeralSessionID(volatileProjectID), ProjectID: volatileProjectID,
		AgentID: "agent-default",
		// 使用者可以在對話設定裡指定 memory_scope；隔離對話必須忽略它，
		// 否則等於提供一條把隔離內容送進共用 scope 的路徑。
		Metadata: map[string]any{"memory_scope": "agent-default"},
	}
	normalSession := domain.Session{ID: "session_normal", ProjectID: "project_other", AgentID: "agent-default"}

	for _, spaceEnabled := range []bool{true, false} {
		volatileScope := memory.ScopeForSessionWithSpace(volatileSession, spaceEnabled)
		normalScope := memory.ScopeForSessionWithSpace(normalSession, spaceEnabled)
		if volatileScope != "project:"+volatileProjectID {
			t.Fatalf("space=%v：隔離對話的 scope 應為專案專屬，得到 %q", spaceEnabled, volatileScope)
		}
		if volatileScope == normalScope {
			t.Fatalf("space=%v：隔離對話不該與一般對話共用 scope（%q）", spaceEnabled, volatileScope)
		}
	}
}

// 規格的第 3 條驗收：對話**進行中**，dataDir 整棵樹必須與對話開始前完全一致。
//
// 逐一測試每個儲存不能取代這一條。前者只證明「我想到的那幾個位置沒問題」，
// 而漏掉的儲存正是不會被想到的那一個；整棵樹比對才會在有人新增一個沒接上
// 分流的儲存時直接失敗。
func TestEphemeralConversationLeavesDataDirUntouchedWhileRunning(t *testing.T) {
	dataDir := t.TempDir()
	volatileRoot := t.TempDir()
	ctx := context.Background()
	roots := fixedRoots{code: "abc123", root: volatileRoot}

	sessions, _ := filestore.NewSessionRepository(dataDir)
	plans, _ := filestore.NewPlanRepository(dataDir)
	attachments, _ := filestore.NewAttachmentRepository(dataDir)
	runs, _ := filestore.NewRunRepository(dataDir)
	events, _ := filestore.NewRunEventRepository(dataDir)
	notifications, _ := filestore.NewNotificationRepository(dataDir)
	memories, _ := filestore.NewMemoryRepository(dataDir)
	sessions.SetProjectRoots(roots)
	plans.SetProjectRoots(roots)
	attachments.SetProjectRoots(roots)
	events.SetProjectRoots(roots)

	// 先讓一般對話把每個儲存都寫過一輪，基準才涵蓋所有檔案，
	// 而不是一棵幾乎空的樹——空樹比對什麼都抓不到。
	normal, err := sessions.Create(ctx, "agent", domain.CreateSessionInput{ProjectID: "project_disk", Title: "一般對話"})
	if err != nil {
		t.Fatalf("create normal session: %v", err)
	}
	seedEveryStore(t, ctx, normal.ID, "一般", sessions, plans, attachments, runs, events, notifications, memories)
	baseline := snapshotTree(t, dataDir)

	sessions.SetSessionIDFactory(func(string) string { return domain.NewEphemeralSessionID(volatileProjectID) })
	volatile, err := sessions.Create(ctx, "agent", domain.CreateSessionInput{ProjectID: volatileProjectID, Title: "隔離對話"})
	if err != nil {
		t.Fatalf("create volatile session: %v", err)
	}
	seedEveryStore(t, ctx, volatile.ID, "祕密", sessions, plans, attachments, runs, events, notifications, memories)

	// 資料真的有寫出去——否則下面的「沒有差異」只是因為什麼都沒發生。
	if len(snapshotTree(t, volatileRoot)) == 0 {
		t.Fatal("隔離對話沒有在 RAM disk 上留下任何東西，比對就沒有意義")
	}
	if difference := treeDifference(baseline, snapshotTree(t, dataDir)); len(difference) > 0 {
		t.Fatalf("隔離對話進行中改變了 dataDir：\n%s", strings.Join(difference, "\n"))
	}
	// 連 ID 都不能出現：索引、清單這類檔案可能大小不變但內容被改。
	if found := grepTree(t, dataDir, volatile.ID); len(found) > 0 {
		t.Fatalf("dataDir 有檔案含隔離 Session 的 ID：\n%s", strings.Join(found, "\n"))
	}
	if found := grepTree(t, dataDir, "祕密"); len(found) > 0 {
		t.Fatalf("dataDir 有檔案含隔離對話的內容：\n%s", strings.Join(found, "\n"))
	}
}

// seedEveryStore 讓一次對話碰過所有會落地的儲存。
// 新增儲存時要一併加進來，否則上面那條驗收會有盲區。
func seedEveryStore(
	t *testing.T, ctx context.Context, sessionID, marker string,
	sessions *filestore.SessionRepository, plans *filestore.PlanRepository,
	attachments *filestore.AttachmentRepository, runs *filestore.RunRepository,
	events *filestore.RunEventRepository, notifications *filestore.NotificationRepository,
	memories *filestore.MemoryRepository,
) {
	t.Helper()
	message := domain.Message{ID: domain.NewID("msg"), Role: "user", Content: marker + "訊息"}
	if _, err := sessions.AppendEntry(ctx, sessionID, domain.SessionEntry{
		Type: domain.SessionEntryMessage, Message: &message,
	}); err != nil {
		t.Fatalf("append entry: %v", err)
	}
	runID := domain.NewRunIDForSession(sessionID)
	if err := runs.Save(ctx, domain.Run{
		ID: runID, SessionID: sessionID, Status: domain.RunStatusCompleted,
		Input: marker + "提問", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save run: %v", err)
	}
	if err := events.Append(ctx, domain.Event{
		ID: domain.NewID("event"), RunID: runID, SessionID: sessionID, Sequence: 1,
		Type: "run.completed", Payload: map[string]any{"text": marker + "輸出"},
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	plan, err := domain.NewPlan(sessionID, domain.CreatePlanInput{
		Title: marker + "計畫", Steps: []domain.CreatePlanStepInput{{Title: marker + "步驟", Verification: "完成"}},
	}, time.Now())
	if err != nil {
		t.Fatalf("new plan: %v", err)
	}
	if _, err := plans.Create(ctx, plan); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if _, err := attachments.Save(ctx, sessionID, "note.txt", "text/plain", strings.NewReader(marker+"附件"), 1024); err != nil {
		t.Fatalf("save attachment: %v", err)
	}
	if err := notifications.Save(ctx, domain.Notification{
		ID: domain.NewID("notification"), Title: marker + "標題", Message: marker + "訊息",
		SessionID: sessionID, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save notification: %v", err)
	}
	if _, err := memories.Remember(ctx, domain.RememberMemoryInput{
		Scope: "project:" + sessionID, Kind: domain.MemoryKindFact,
		Content: marker + "事實", Confidence: 0.9, SourceSessionID: sessionID,
	}); err != nil {
		t.Fatalf("remember: %v", err)
	}
}

// 隔離對話用自己的 scope 只擋住一半：漏的是一般對話主動把 memory_scope 指過去。
// 那條路徑會讓兩者共用 scope，去重與取代就能跨越隔離界線。
func TestMemoryScopeOverrideCannotTargetAnotherProject(t *testing.T) {
	volatileScope := "project:" + volatileProjectID
	normal := func(scope string) domain.Session {
		return domain.Session{
			ID: "session_normal", ProjectID: "project_other", AgentID: "agent-default",
			Metadata: map[string]any{"memory_scope": scope},
		}
	}
	cases := []struct {
		name         string
		session      domain.Session
		wantSpaceOn  string
		wantSpaceOff string
	}{
		{
			// 覆寫被忽略後走預設解析：開啟回憶空間時收斂到自己的 Project，
			// 關閉時退回 Agent scope——兩者都不會是隔離專案的 scope。
			name:         "指向隔離專案的 scope 會被忽略",
			session:      normal(volatileScope),
			wantSpaceOn:  "project:project_other",
			wantSpaceOff: "agent-default",
		},
		{
			name:         "指向自己的專案照常生效",
			session:      normal("project:project_other"),
			wantSpaceOn:  "project:project_other",
			wantSpaceOff: "project:project_other",
		},
		{
			name:         "非 Project scope 不受影響",
			session:      normal("tenant:acme"),
			wantSpaceOn:  "tenant:acme",
			wantSpaceOff: "tenant:acme",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			for spaceEnabled, want := range map[bool]string{true: testCase.wantSpaceOn, false: testCase.wantSpaceOff} {
				got := memory.ScopeForSessionWithSpace(testCase.session, spaceEnabled)
				if got != want {
					t.Fatalf("space=%v：scope 應為 %q，得到 %q", spaceEnabled, want, got)
				}
				if got == volatileScope {
					t.Fatalf("space=%v：一般對話不該落在隔離專案的 scope", spaceEnabled)
				}
			}
		})
	}
}
