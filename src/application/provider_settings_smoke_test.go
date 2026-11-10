package application

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"errors"
	"testing"
)

type fallbackFailEngine struct {
	*fakeEngine
	updates int
}

func (e *fallbackFailEngine) UpdateSession(ctx context.Context, id string, input domain.UpdateSessionInput) (domain.Session, error) {
	e.updates++
	if e.updates == 2 {
		return domain.Session{}, errors.New("synthetic session write failure")
	}
	return e.fakeEngine.UpdateSession(ctx, id, input)
}

type fallbackRunRepository struct{ ports.RunRepository }

func (r fallbackRunRepository) Get(context.Context, string) (domain.Run, error) {
	return domain.Run{ProviderID: "removed"}, nil
}

func TestProviderSessionFallbackFailureSmoke(t *testing.T) {
	s, engine := newTestService(t, domain.PermissionPolicy{})
	for _, id := range []string{"one", "two"} {
		engine.sessions[id] = domain.Session{ID: id, ProviderID: "removed", Model: "old"}
	}
	failing := &fallbackFailEngine{fakeEngine: engine}
	registry, err := NewRegistry(failing)
	if err != nil {
		t.Fatal(err)
	}
	s.registry = registry
	committed := false
	commit := func() error { committed = true; return nil }
	err = s.ApplyProviderSettings(context.Background(), map[string]bool{"default": true}, domain.ProviderDescriptor{ID: "default", DefaultModel: "new"}, commit)
	if err == nil || committed {
		t.Fatal("部分遷移失敗仍提交")
	}
	for _, session := range engine.sessions {
		if session.ProviderID != "removed" || session.Model != "old" {
			t.Fatal("部分遷移未復原")
		}
	}
	// Session 選擇與當輪 Run 覆寫不同時，仍不可移除執行中的 Provider。
	engine.sessions = map[string]domain.Session{"one": {ID: "one", ProviderID: "default"}}
	s.active["run"] = activeRun{sessionID: "one"}
	s.runs = fallbackRunRepository{s.runs}
	err = s.ApplyProviderSettings(context.Background(), map[string]bool{"default": true}, domain.ProviderDescriptor{ID: "default", DefaultModel: "new"}, commit)
	if !errors.Is(err, domain.ErrConflict) || committed {
		t.Fatal("執行中的 Run 覆寫未受保護")
	}
}

func TestProviderSessionFallbackSmoke(t *testing.T) {
	for _, mode := range []string{"success", "commit-failure", "cancel", "active", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			s, engine := newTestService(t, domain.PermissionPolicy{})
			original := domain.Session{ID: "one", ProviderID: "removed", Model: "old-model", ThinkingMode: "high", Title: "保留對話", ProjectID: "project"}
			engine.sessions["one"] = original
			engine.sessions["two"] = domain.Session{ID: "two", ProviderID: "removed", Model: "old-model"}
			engine.sessions["keep"] = domain.Session{ID: "keep", ProviderID: "default", Model: "custom-model"}
			if mode == "active" {
				s.active["run"] = activeRun{sessionID: "one"}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			enabled := map[string]bool{"default": true}
			if mode == "disabled" {
				enabled["removed"] = false
			}
			called := false
			err := s.ApplyProviderSettings(ctx, enabled, domain.ProviderDescriptor{ID: "default", DefaultModel: "new-model"}, func() error {
				called = true
				if engine.sessions["one"].ProviderID != "default" || engine.sessions["two"].Model != "new-model" {
					t.Fatal("提交前尚未完成遷移")
				}
				if mode == "cancel" {
					cancel()
					return ctx.Err()
				}
				if mode == "commit-failure" {
					return errors.New("synthetic write failure")
				}
				return nil
			})
			got := engine.sessions["one"]
			if mode == "success" {
				if err != nil || !called || got.ProviderID != "default" || got.Model != "new-model" || got.ThinkingMode != "" {
					t.Fatalf("回到預設失敗：%+v %v", got, err)
				}
			} else {
				if err == nil || got.ProviderID != original.ProviderID || got.Model != original.Model || got.ThinkingMode != original.ThinkingMode {
					t.Fatalf("失敗未還原：%+v %v", got, err)
				}
				if (mode == "active" || mode == "disabled") && called {
					t.Fatal("預檢失敗仍提交")
				}
			}
			if got.Title != original.Title || got.ProjectID != original.ProjectID || engine.sessions["keep"].Model != "custom-model" {
				t.Fatal("無關欄位或對話被改寫")
			}
		})
	}
}
