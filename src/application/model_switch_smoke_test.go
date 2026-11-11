package application

import (
	"AgenticService/src/domain"
	"context"
	"errors"
	"testing"
)

func TestActiveSessionModelSwitchSmoke(t *testing.T) {
	const id = "session_model_switch"
	f := newRunInputFixture(t, id, false)
	run := f.start(t, id)
	model := "new-model"
	updated, err := f.service.UpdateSession(context.Background(), id, domain.UpdateSessionInput{Model: &model})
	if err != nil || updated.Model != model {
		t.Fatalf("更新失敗：%v / %v", updated, err)
	}
	f.service.mu.Lock()
	selection := f.service.active[run.ID].modelSelection
	f.service.mu.Unlock()
	if got := selection.Take(); got != model {
		t.Fatalf("回合邊界待用模型=%q", got)
	}
	profile := "default"
	if _, err := f.service.UpdateSession(context.Background(), id, domain.UpdateSessionInput{Model: &model, PermissionProfile: &profile}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("執行中仍須阻擋權限變更：%v", err)
	}
	empty := ""
	if _, err := f.service.UpdateSession(context.Background(), id, domain.UpdateSessionInput{Model: &empty}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("空模型不得排入：%v", err)
	}
	if selection.Take() != "" {
		t.Fatal("失敗更新不應排入模型變更")
	}
	for _, mode := range []string{"high", ""} {
		updated, err := f.service.UpdateSession(context.Background(), id, domain.UpdateSessionInput{ThinkingMode: &mode})
		if err != nil || updated.ThinkingMode != mode {
			t.Fatalf("Thinking 更新失敗：%v / %v", updated, err)
		}
		model, thinking := selection.TakeSettings()
		if model != "" || thinking == nil || *thinking != mode {
			t.Fatalf("Thinking 更新遺失：%q / %v", model, thinking)
		}
	}
	invalid := "invalid-mode"
	if _, err := f.service.UpdateSession(context.Background(), id, domain.UpdateSessionInput{ThinkingMode: &invalid}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("無效 Thinking 應拒絕：%v", err)
	}
	if _, thinking := selection.TakeSettings(); thinking != nil {
		t.Fatal("無效設定不得排入")
	}
	f.service.mu.Lock()
	active := f.service.active[run.ID]
	active.providerID = "different-provider"
	f.service.active[run.ID] = active
	f.service.mu.Unlock()
	if _, err := f.service.UpdateSession(context.Background(), id, domain.UpdateSessionInput{Model: &model}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("不同 Provider 的 Run 不得誤套模型：%v", err)
	}
}
