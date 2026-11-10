package bootstrap

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/adapters/openaicompat"
	harnessagent "AgenticService/src/agents/harness"
	"AgenticService/src/application"
	"AgenticService/src/domain"
	"AgenticService/src/harness"
	"AgenticService/src/internal/logging"
	"AgenticService/src/modelrouter"
	"AgenticService/src/providerauth"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// 全部使用暫存資料及未登入的合成 Provider，不接觸真實設定或上游。
func TestProviderSettingsSaveDeleteSmoke(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := DefaultConfig()
	config.DataDir = root
	config.DefaultProviderID = "primary"
	config.ModelPrices = map[string]map[string]domain.ModelPrice{
		"primary": {"synthetic": {InputPerMillion: 1, Currency: "USD"}},
		"codex":   {"synthetic": {InputPerMillion: 2, Currency: "USD"}},
	}
	config.Providers = map[string]ProviderConfig{
		"primary": {Type: "openai-compatible", OpenAICompatible: &openaicompat.Config{BaseURL: "https://example.invalid/v1", Model: "synthetic"}},
		"codex":   {Type: "openai-codex-responses", OpenAICodexResponses: &openaicompat.Config{Model: "synthetic"}},
	}
	auth, err := providerauth.New(filepath.Join(root, "synthetic-oauth.json"), logging.Discard())
	if err != nil {
		t.Fatal(err)
	}
	values, err := buildProviderValues(config, logging.Discard(), auth)
	if err != nil {
		t.Fatal(err)
	}
	router, err := modelrouter.New(config.DefaultProviderID, values)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := application.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	sessionStore, err := filestore.NewSessionRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := harnessagent.New(domain.AgentDescriptor{ID: "synthetic"}, sessionStore, &harness.Runner{})
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.Register(agent); err != nil {
		t.Fatal(err)
	}
	session, err := agent.CreateSession(ctx, domain.CreateSessionInput{Title: "保留對話", ProviderID: "codex", Model: "old-model", ThinkingMode: "high"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sessionStore.AppendEntry(ctx, session.ID, domain.SessionEntry{Type: domain.SessionEntryMessage, Message: &domain.Message{Role: "user", Content: "既有紀錄"}}); err != nil {
		t.Fatal(err)
	}
	runs, err := filestore.NewRunRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	events, err := filestore.NewRunEventRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := filestore.NewProjectRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	workspaces, err := filestore.NewWorkspaceRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := filestore.NewPlanRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewService(application.Dependencies{Registry: registry, Runs: runs, Events: events, Projects: projects, Workspaces: workspaces, Plans: plans, Providers: router, Logger: logging.Discard()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(ctx) })
	r := &Runtime{Config: config, Application: service, Model: router, ProviderAuth: auth, logger: logging.Discard()}
	view := providerSettingsView(r.Config, auth)
	for i := range view.Providers {
		view.Providers[i].DisplayName = "更新名稱"
	}
	saved, err := r.UpdateProviderSettings(ctx, domain.UpdateProviderSettingsInput{DefaultProviderID: "primary", Providers: view.Providers})
	if err != nil {
		t.Fatalf("未登入應可儲存：%v", err)
	}
	var primary domain.ProviderSetting
	for _, p := range saved.Providers {
		if p.ID == "primary" {
			primary = p
		}
	}
	deleteInput := domain.UpdateProviderSettingsInput{DefaultProviderID: "primary", Providers: []domain.ProviderSetting{primary}}
	deleted, err := r.UpdateProviderSettings(ctx, deleteInput)
	if err != nil || len(deleted.Providers) != 1 || router.HasProvider("codex") {
		t.Fatalf("未使用的 Provider 應可刪除：%v", err)
	}
	// 重新開啟 Session 儲存庫，確認不是只更動畫面或快取。
	reopened, err := filestore.NewSessionRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	migrated, err := reopened.Get(ctx, session.ID)
	if err != nil || migrated.ProviderID != "primary" || migrated.Model != "synthetic" || migrated.ThinkingMode != "" || migrated.Title != session.Title {
		t.Fatalf("Session 未持久化回到預設：%+v %v", migrated, err)
	}
	messages, err := reopened.ListMessages(ctx, session.ID)
	if err != nil || len(messages) != 1 || messages[0].Content != "既有紀錄" {
		t.Fatalf("對話紀錄未保留：%v", err)
	}
	if len(r.Config.ModelPrices) != 1 || r.Config.ModelPrices["primary"]["synthetic"].InputPerMillion != 1 {
		t.Fatal("刪除未正確保留其他 Provider 的價格")
	}
	// 模擬重新啟動：基底仍包含被刪除的 Provider 與計價設定。
	reloaded := config
	if err := loadPersistedProviderSettings(&reloaded); err != nil {
		t.Fatal(err)
	}
	if err := validateConfig(&reloaded); err != nil {
		t.Fatalf("刪除後重啟失敗：%v", err)
	}
	if len(reloaded.ModelPrices) != 1 || len(config.ModelPrices) != 2 {
		t.Fatal("舊價格復活或基底 map 被改寫")
	}
	if _, err = r.UpdateProviderSettings(ctx, domain.UpdateProviderSettingsInput{DefaultProviderID: "primary", Providers: saved.Providers}); err != nil {
		t.Fatal(err)
	}
	_, err = workspaces.Create(ctx, domain.CreateWorkspaceInput{Name: "synthetic-workspace", ProviderIDs: []string{"primary", "codex"}, DefaultProviderID: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	r.Config.ModelPrices["codex"] = map[string]domain.ModelPrice{"synthetic": {InputPerMillion: 2, Currency: "USD"}}
	before, err := os.ReadFile(filepath.Join(root, providerSettingsFilename))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.UpdateProviderSettings(ctx, deleteInput); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("使用中的 Provider 應明確拒絕刪除：%v", err)
	}
	if !router.HasProvider("codex") {
		t.Fatal("拒絕刪除不應改動 Router")
	}
	after, err := os.ReadFile(filepath.Join(root, providerSettingsFilename))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || len(r.Config.ModelPrices) != 2 {
		t.Fatal("拒絕刪除提前修改了計價設定或檔案")
	}
}

func TestProviderPricesLegacyAndEmptySmoke(t *testing.T) {
	for _, explicitEmpty := range []bool{false, true} {
		config := DefaultConfig()
		config.DataDir = t.TempDir()
		config.ModelPrices = map[string]map[string]domain.ModelPrice{
			config.DefaultProviderID: {"synthetic": {InputPerMillion: 1}},
			"removed":                {"synthetic": {InputPerMillion: 2}},
		}
		stored := storedProviderSettings{DefaultProviderID: config.DefaultProviderID, Providers: config.Providers}
		if explicitEmpty {
			prices := map[string]map[string]domain.ModelPrice{}
			stored.ModelPrices = &prices
		}
		data, err := json.Marshal(stored)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(config.DataDir, providerSettingsFilename), data, 0600); err != nil {
			t.Fatal(err)
		}
		if err = loadPersistedProviderSettings(&config); err != nil {
			t.Fatal(err)
		}
		if err = validateConfig(&config); err != nil {
			t.Fatal(err)
		}
		want := 1
		if explicitEmpty {
			want = 0
		}
		if len(config.ModelPrices) != want {
			t.Fatalf("legacy=%t prices=%d want=%d", !explicitEmpty, len(config.ModelPrices), want)
		}
	}
}
