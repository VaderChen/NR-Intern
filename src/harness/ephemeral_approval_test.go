package harness

import (
	"context"
	"testing"
	"time"

	"AgenticService/src/approval"
	"AgenticService/src/domain"
)

// ephemeralSession 回傳一個帶有後端旗標的 Session；旗標由 application 在組裝
// Run metadata 時寫入，這裡直接模擬那個結果。
func ephemeralSession() domain.Session {
	session := testSession()
	session.Metadata = map[string]any{"ephemeral_project": true}
	return session
}

// runShellOnce 跑一輪只呼叫一次 shell_exec 的 Run，回報是否跳出核准、
// 以及跳過核准時記錄的原因。
func runShellOnce(t *testing.T, session domain.Session) (asked bool, skipReason string, err error) {
	t.Helper()
	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "shell_exec", Arguments: map[string]any{"command": "touch a.txt"}}}},
		{Content: "完成"},
	}}
	runner := newTestRunner(sessions, model, &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "shell_exec"}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
		},
	})
	runner.Approvals = approval.NewCoordinator([]string{"shell_exec"})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = runner.Run(ctx, Input{RunID: "run_1", Session: session, UserInput: "執行"}, func(event domain.EngineEvent) error {
		switch event.Type {
		case "run.approval_required":
			asked = true
			request := event.Payload["approval"].(domain.ToolApprovalRequest)
			return runner.Approvals.Decide(request.RunID, domain.ToolApprovalDecisionInput{ApprovalID: request.ID, Decision: domain.ToolApprovalApprove})
		case "run.approval_skipped":
			skipReason, _ = event.Payload["reason"].(string)
		}
		return nil
	})
	return asked, skipReason, err
}

// Shell 能操作工作區外的系統或網路，RAM 儲存不構成免核准的安全邊界。
func TestEphemeralProjectShellStillRequiresApproval(t *testing.T) {
	session := ephemeralSession()
	session.Metadata["sandbox_roots"] = []string{t.TempDir()}
	asked, reason, err := runShellOnce(t, session)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !asked {
		t.Fatal("記憶體隔離專案的 Shell 仍須人工核准")
	}
	if reason != "" {
		t.Fatalf("Shell 不應記錄核准豁免：%q", reason)
	}
}

func TestEphemeralApprovalRequiresContainedToolAndSingleRootSmoke(t *testing.T) {
	definitions := []domain.ToolDefinition{
		{Name: "file_write", Capabilities: []string{"workspace-contained"}},
		{Name: "shell_exec"}, {Name: "ssh_exec"}, {Name: "mcp__remote"},
		{Name: "file_read", ReadOnly: true},
	}
	for _, tc := range []struct {
		name   string
		roots  any
		exempt bool
	}{
		{"single", []string{"synthetic-workspace"}, true},
		{"decoded-single", []any{"synthetic-workspace"}, true},
		{"additional-persistent", []string{"synthetic-workspace", "synthetic-persistent"}, false},
		{"missing", nil, false}, {"empty", []string{""}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := ephemeralSession()
			session.Metadata["sandbox_roots"] = tc.roots
			exempt := approvalExemptToolNames(definitions, session)
			if exempt["file_write"] != tc.exempt || !exempt["file_read"] {
				t.Fatalf("豁免範圍不符：%v", exempt)
			}
			for _, name := range []string{"shell_exec", "ssh_exec", "mcp__remote"} {
				if exempt[name] {
					t.Fatalf("外部副作用被豁免：%s", name)
				}
			}
		})
	}
}

// 一般專案必須維持原本的逐次核准，否則這個豁免會變成全域關閉審核。
func TestNormalProjectStillRequiresApproval(t *testing.T) {
	session := testSession()
	session.Metadata = map[string]any{}
	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "shell_exec", Arguments: map[string]any{"command": "touch a.txt"}}}},
		{Content: "完成"},
	}}
	runner := newTestRunner(sessions, model, &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "shell_exec"}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
		},
	})
	coordinator := approval.NewCoordinator([]string{"shell_exec"})
	runner.Approvals = coordinator

	asked := make(chan domain.ToolApprovalRequest, 1)
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		_, err := runner.Run(ctx, Input{RunID: "run_normal", Session: session, UserInput: "執行"}, func(event domain.EngineEvent) error {
			if event.Type == "run.approval_required" {
				request, _ := event.Payload["approval"].(domain.ToolApprovalRequest)
				select {
				case asked <- request:
				default:
				}
			}
			return nil
		})
		done <- err
	}()

	select {
	case request := <-asked:
		// 放行讓 Run 收尾，避免測試卡在等待。
		if err := coordinator.Decide("run_normal", domain.ToolApprovalDecisionInput{
			ApprovalID: request.ID, Decision: domain.ToolApprovalApprove,
		}); err != nil {
			t.Fatalf("decide: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("一般專案仍必須跳出人工核准")
	}
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}

// 旗標必須是後端判定的。Client 若能自行夾帶，等於「宣告自己是記憶體專案就免審核」。
// 這裡守住讀取端的型別：只有真正的布林 true 才算數。
func TestEphemeralFlagIgnoresNonBooleanValues(t *testing.T) {
	for _, value := range []any{"true", 1, "ephemeral", nil} {
		session := testSession()
		session.Metadata = map[string]any{"ephemeral_project": value}
		if ephemeralProjectSession(session) {
			t.Fatalf("metadata 值 %#v 不應被當成記憶體隔離專案", value)
		}
	}
	if !ephemeralProjectSession(ephemeralSession()) {
		t.Fatal("布林 true 應該被認可")
	}
}
