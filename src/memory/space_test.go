package memory_test

import (
	"context"
	"strings"
	"testing"

	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"AgenticService/src/memory"
)

func newSpaceManager(t *testing.T, enabled bool) *memory.Manager {
	t.Helper()
	repository, err := filestore.NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatalf("new memory repository: %v", err)
	}
	return memory.NewManager(repository, memory.Config{
		Enabled:     true,
		AutoRecall:  true,
		AllowWrites: true,
		Space:       memory.SpaceConfig{Enabled: enabled},
	})
}

func projectSession() domain.Session {
	return domain.Session{
		ID:          "session-1",
		AgentID:     "agent-1",
		WorkspaceID: "workspace-1",
		ProjectID:   "project-1",
	}
}

// 准入過濾是回憶空間的第一道關卡：沒有它，其他機制都只是在整理垃圾。
func TestRememberRejectsNonReusableKinds(t *testing.T) {
	manager := newSpaceManager(t, true)
	_, err := manager.Remember(context.Background(), projectSession(), domain.RememberMemoryInput{
		Kind:    domain.MemoryKindFact,
		Content: "這個檔案有 300 行",
	})
	if err == nil {
		t.Fatal("expected fact memories to be rejected when the space is enabled")
	}
	if !strings.Contains(err.Error(), "fact") {
		t.Fatalf("rejection should name the offending kind, got %v", err)
	}
}

// 憑證外洩不能靠模型自律：寫入端必須自己擋。
func TestRememberRejectsSecrets(t *testing.T) {
	manager := newSpaceManager(t, true)
	for _, content := range []string{
		"部署時用 API key sk-abcdefghijklmnopqrstuvwx",
		"資料庫 password 是 hunter2hunter2",
		"ghp_abcdefghijklmnopqrstuvwxyz12 是推送用的",
	} {
		if _, err := manager.Remember(context.Background(), projectSession(), domain.RememberMemoryInput{
			Kind:    domain.MemoryKindProcedure,
			Content: content,
		}); err == nil {
			t.Fatalf("expected secret-bearing content to be rejected: %q", content)
		}
	}
}

// 關閉回憶空間時不應改變既有長期記憶的行為，包括收 fact。
func TestRememberKeepsLegacyBehaviourWhenSpaceDisabled(t *testing.T) {
	manager := newSpaceManager(t, false)
	value, err := manager.Remember(context.Background(), projectSession(), domain.RememberMemoryInput{
		Kind:    domain.MemoryKindFact,
		Content: "這個檔案有 300 行",
	})
	if err != nil {
		t.Fatalf("remember with the space disabled: %v", err)
	}
	if value.Scope != "agent-1" {
		t.Fatalf("scope should stay on the agent when the space is off, got %q", value.Scope)
	}
}

// 跨對話共用不等於全域共用：甲專案的決策不該出現在乙專案的對話裡。
func TestScopeNarrowsToProjectWhenSpaceEnabled(t *testing.T) {
	session := projectSession()
	if scope := memory.ScopeForSessionWithSpace(session, true); scope != "project:project-1" {
		t.Fatalf("expected the project scope, got %q", scope)
	}
	if scope := memory.ScopeForSessionWithSpace(session, false); scope != "agent-1" {
		t.Fatalf("expected the agent scope when the space is off, got %q", scope)
	}
	session.ProjectID = ""
	if scope := memory.ScopeForSessionWithSpace(session, true); scope != "workspace:workspace-1" {
		t.Fatalf("expected the workspace fallback, got %q", scope)
	}
	session.Metadata = map[string]any{"memory_scope": "tenant:acme"}
	if scope := memory.ScopeForSessionWithSpace(session, true); scope != "tenant:acme" {
		t.Fatalf("explicit memory_scope must win, got %q", scope)
	}
}

// 同一件事換句話說不該變成兩則記憶。
func TestRememberMergesNearDuplicates(t *testing.T) {
	manager := newSpaceManager(t, true)
	session := projectSession()
	first, err := manager.Remember(context.Background(), session, domain.RememberMemoryInput{
		Kind:       domain.MemoryKindPreference,
		Content:    "回覆一律使用繁體中文",
		Confidence: 0.6,
	})
	if err != nil {
		t.Fatalf("first remember: %v", err)
	}
	if _, err := manager.Remember(context.Background(), session, domain.RememberMemoryInput{
		Kind:       domain.MemoryKindPreference,
		Content:    "回覆一律使用繁體中文。",
		Confidence: 0.6,
	}); err != nil {
		t.Fatalf("duplicate remember: %v", err)
	}
	values, err := manager.Search(context.Background(), session, domain.MemoryQuery{Limit: 50})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	active := activeMemories(values)
	if len(active) != 1 {
		t.Fatalf("expected the duplicate to merge into one memory, got %d", len(active))
	}
	if active[0].Confidence <= first.Confidence {
		t.Fatalf("a reconfirmed memory should gain confidence: %v -> %v", first.Confidence, active[0].Confidence)
	}
}

// 明確的取代不能被相似度吃掉，否則模型永遠改不掉舊決策。
func TestRememberHonoursExplicitSupersede(t *testing.T) {
	manager := newSpaceManager(t, true)
	session := projectSession()
	first, err := manager.Remember(context.Background(), session, domain.RememberMemoryInput{
		Kind:    domain.MemoryKindDecision,
		Content: "部署目標選用 staging 環境",
	})
	if err != nil {
		t.Fatalf("first remember: %v", err)
	}
	if _, err := manager.Remember(context.Background(), session, domain.RememberMemoryInput{
		Kind:       domain.MemoryKindDecision,
		Content:    "部署目標改用 production 環境",
		Supersedes: []string{first.ID},
	}); err != nil {
		t.Fatalf("superseding remember: %v", err)
	}
	values, err := manager.Search(context.Background(), session, domain.MemoryQuery{Limit: 50})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	active := activeMemories(values)
	if len(active) != 1 {
		t.Fatalf("expected exactly one active memory after the supersede, got %d", len(active))
	}
	if !strings.Contains(active[0].Content, "production") {
		t.Fatalf("the superseding memory should be the surviving one, got %q", active[0].Content)
	}
}

// 低於相關度門檻就完全不注入：寧可空手，不要塞雜訊。
func TestRecallSkipsIrrelevantMemories(t *testing.T) {
	manager := newSpaceManager(t, true)
	session := projectSession()
	if _, err := manager.Remember(context.Background(), session, domain.RememberMemoryInput{
		Kind:    domain.MemoryKindPreference,
		Content: "簡報排版一律留白兩公分",
	}); err != nil {
		t.Fatalf("remember: %v", err)
	}
	result, err := manager.Recall(context.Background(), session, "資料庫連線逾時要怎麼調整")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(result.Memories) != 0 {
		t.Fatalf("an unrelated memory must not be injected, got %d", len(result.Memories))
	}
	related, err := manager.Recall(context.Background(), session, "簡報排版要留白多少")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(related.Memories) != 1 {
		t.Fatalf("a related memory should be injected, got %d", len(related.Memories))
	}
}

// 注入視窗要壓在 6 條／1,200 字內，否則本機模型每輪都在重送這批內容。
func TestRecallHonoursSpaceWindow(t *testing.T) {
	manager := newSpaceManager(t, true)
	session := projectSession()
	for index := 0; index < 12; index++ {
		if _, err := manager.Remember(context.Background(), session, domain.RememberMemoryInput{
			Kind:    domain.MemoryKindProcedure,
			Content: "部署流程步驟 " + strings.Repeat("甲乙丙丁", 40) + " 編號" + string(rune('A'+index)),
		}); err != nil {
			t.Fatalf("remember %d: %v", index, err)
		}
	}
	result, err := manager.Recall(context.Background(), session, "部署流程步驟")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(result.Memories) > 6 {
		t.Fatalf("recall must stay within 6 memories, got %d", len(result.Memories))
	}
	if count := len([]rune(result.SystemPrompt)); count > 1_600 {
		t.Fatalf("injected prompt should stay near the 1,200 character budget, got %d", count)
	}
}

// 記憶只增不減，時間夠久必然劣化成雜訊。
func TestRememberEvictsBeyondScopeCap(t *testing.T) {
	repository, err := filestore.NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatalf("new memory repository: %v", err)
	}
	manager := memory.NewManager(repository, memory.Config{
		Enabled:     true,
		AllowWrites: true,
		Space:       memory.SpaceConfig{Enabled: true, ScopeCap: 5},
	})
	session := projectSession()
	written := make([]string, 0, 9)
	for index := 0; index < 9; index++ {
		value, err := manager.Remember(context.Background(), session, domain.RememberMemoryInput{
			Kind:    domain.MemoryKindDecision,
			Content: distinctDecision(index),
		})
		if err != nil {
			t.Fatalf("remember %d: %v", index, err)
		}
		written = append(written, value.ID)
	}
	active, err := repository.ListScope(context.Background(), "project:project-1")
	if err != nil {
		t.Fatalf("list scope: %v", err)
	}
	if len(active) != 5 {
		t.Fatalf("expected the scope to be capped at 5 active memories, got %d", len(active))
	}
	// 淘汰是轉狀態不是刪資料：出問題時要能查回當時記了什麼。
	forgotten := 0
	for _, id := range written {
		value, err := repository.Get(context.Background(), "project:project-1", id)
		if err != nil {
			t.Fatalf("evicted memory %s must remain readable for auditing: %v", id, err)
		}
		if value.Status == domain.MemoryStatusForgotten {
			forgotten++
			if strings.TrimSpace(value.ForgetReason) == "" {
				t.Fatalf("evicted memory %s should record why it was dropped", id)
			}
		}
	}
	if forgotten != 4 {
		t.Fatalf("expected 4 evicted memories, got %d", forgotten)
	}
}

// 第一次失敗常常只是打錯字；第二次才代表模型沒有頭緒。
func TestFailureRecallTracker(t *testing.T) {
	tracker := &memory.FailureRecallTracker{}
	if tracker.Observe("document_create", true) {
		t.Fatal("a single failure must not trigger a recall")
	}
	if !tracker.Observe("document_create", true) {
		t.Fatal("a second consecutive failure should trigger a recall")
	}
	if tracker.Observe("document_create", true) {
		t.Fatal("the same tool must not trigger a recall twice in one run")
	}
	tracker = &memory.FailureRecallTracker{}
	tracker.Observe("shell_exec", true)
	tracker.Observe("shell_exec", false)
	if tracker.Observe("shell_exec", true) {
		t.Fatal("a success in between should reset the consecutive-failure count")
	}
}

func TestFailureRecallQueryTrimsLongMessages(t *testing.T) {
	query := memory.FailureRecallQuery("document_create", strings.Repeat("錯", 900))
	if count := len([]rune(query)); count > 320 {
		t.Fatalf("failure query should stay short, got %d runes", count)
	}
	if !strings.HasPrefix(query, "document_create") {
		t.Fatalf("failure query should lead with the tool name, got %q", query)
	}
}

// distinctDecision 產生九筆真正互不相同的決策，讓淘汰測試不會被去重先吃掉。
func distinctDecision(index int) string {
	subjects := []string{
		"日誌保留天數定為三十天",
		"前端建置改用離線快取",
		"資料庫遷移一律先跑演練",
		"憑證輪替週期縮短為每季",
		"批次工作排在離峰時段執行",
		"錯誤回報統一送到通知中心",
		"測試資料每次跑完就清空",
		"外部呼叫都要設定逾時上限",
		"設定檔變更需要兩人覆核",
	}
	return subjects[index%len(subjects)]
}

func activeMemories(values []domain.Memory) []domain.Memory {
	result := make([]domain.Memory, 0, len(values))
	for _, value := range values {
		if value.Status == domain.MemoryStatusActive {
			result = append(result, value)
		}
	}
	return result
}
