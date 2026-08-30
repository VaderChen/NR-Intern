package harness

import (
	"AgenticService/src/approval"
	"AgenticService/src/domain"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func calls(names ...string) []domain.ToolCall {
	result := make([]domain.ToolCall, len(names))
	for index, name := range names {
		result[index] = domain.ToolCall{ID: "call_" + name + "_" + string(rune('a'+index)), Name: name}
	}
	return result
}

func groupShape(groups [][]domain.ToolCall) string {
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		names := make([]string, 0, len(group))
		for _, call := range group {
			names = append(names, call.Name)
		}
		parts = append(parts, strings.Join(names, "+"))
	}
	return strings.Join(parts, "|")
}

func readOnlySet() map[string]bool {
	return map[string]bool{"file_read": true, "file_search": true}
}

func TestGroupToolCallsBatchesConsecutiveReadOnlyCalls(t *testing.T) {
	groups := groupToolCalls(calls("file_read", "file_search", "file_read"), readOnlySet())

	if got, want := groupShape(groups), "file_read+file_search+file_read"; got != want {
		t.Fatalf("groups = %q, want %q", got, want)
	}
}

// TestGroupToolCallsSplitsAroundWrites 是安全性的核心：兩個讀取之間隔著寫入時
// 不能併發，否則第二次讀取會看到不確定的狀態。
func TestGroupToolCallsSplitsAroundWrites(t *testing.T) {
	groups := groupToolCalls(calls("file_read", "file_write", "file_read"), readOnlySet())

	if got, want := groupShape(groups), "file_read|file_write|file_read"; got != want {
		t.Fatalf("groups = %q, want %q", got, want)
	}
}

func TestGroupToolCallsTreatsUnknownToolsAsSequential(t *testing.T) {
	groups := groupToolCalls(calls("file_read", "mystery_tool"), readOnlySet())

	if got, want := groupShape(groups), "file_read|mystery_tool"; got != want {
		t.Fatalf("groups = %q, want %q", got, want)
	}
}

func TestGroupToolCallsCapsParallelism(t *testing.T) {
	names := make([]string, maxParallelTools+2)
	for index := range names {
		names[index] = "file_read"
	}

	groups := groupToolCalls(calls(names...), readOnlySet())

	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if len(groups[0]) != maxParallelTools || len(groups[1]) != 2 {
		t.Fatalf("group sizes = %d,%d want %d,2", len(groups[0]), len(groups[1]), maxParallelTools)
	}
}

func TestGroupToolCallsHandlesNoCalls(t *testing.T) {
	if groups := groupToolCalls(nil, readOnlySet()); len(groups) != 0 {
		t.Fatalf("groups = %d, want 0", len(groups))
	}
}

func TestStagedToolDefinitionsAlwaysExposePlanningControls(t *testing.T) {
	definitions := []domain.ToolDefinition{
		{Name: "file_read"}, {Name: "plan_get"}, {Name: "shell_exec"}, {Name: "plan_step_update"},
	}
	active := stagedToolDefinitions(definitions, false)
	if got := availableToolNamesSorted(active); strings.Join(got, ",") != "plan_get,plan_step_update,shell_exec" {
		t.Fatalf("active tools = %v", got)
	}
}

func TestStagedToolDefinitionsExposeWaitAlongsideShell(t *testing.T) {
	definitions := []domain.ToolDefinition{
		{Name: "file_read"}, {Name: "shell_exec"}, {Name: "wait_for", ReadOnly: true}, {Name: "ssh_wait", ReadOnly: true},
	}
	active := stagedToolDefinitions(definitions, false)
	if got := availableToolNamesSorted(active); strings.Join(got, ",") != "shell_exec,ssh_wait,wait_for" {
		t.Fatalf("active tools = %v, want shell_exec,ssh_wait,wait_for", got)
	}
}

// TestRunExecutesReadOnlyToolsInParallel 用會合點證明工具真的同時在跑：
// 三個工具必須都抵達才會被放行，依序執行永遠湊不齊。
func TestRunExecutesReadOnlyToolsInParallel(t *testing.T) {
	const parallel = 3
	arrived := make(chan struct{}, parallel)
	release := make(chan struct{})
	go func() {
		for index := 0; index < parallel; index++ {
			<-arrived
		}
		close(release)
	}()

	tools := &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "file_read", ReadOnly: true}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			arrived <- struct{}{}
			select {
			case <-release:
			case <-time.After(3 * time.Second):
				return domain.ToolExecution{}, errors.New("tools did not run concurrently")
			}
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
		},
	}
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{
			{ID: "call_1", Name: "file_read"},
			{ID: "call_2", Name: "file_read"},
			{ID: "call_3", Name: "file_read"},
		}},
		{Content: "讀完了"},
	}}
	runner := newTestRunner(sessions, model, tools)

	if _, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "平行讀取"}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	messages, err := sessions.ListMessages(context.Background(), sessions.session.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	for _, message := range messages {
		if strings.EqualFold(message.Role, "tool") && message.Content != "ok" {
			t.Fatalf("tool result = %q, want ok", message.Content)
		}
	}
}

// TestRunPreservesToolResultOrderWhenParallel 保證併發不改變 transcript 的順序，
// 否則重播與 tool call 配對都會出錯。
func TestRunPreservesToolResultOrderWhenParallel(t *testing.T) {
	var mu sync.Mutex
	tools := &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "file_read", ReadOnly: true}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			mu.Lock()
			defer mu.Unlock()
			// 讓最先送出的呼叫最晚完成。
			if call.ID == "call_1" {
				time.Sleep(30 * time.Millisecond)
			}
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: call.ID}, nil
		},
	}
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{
			{ID: "call_1", Name: "file_read"},
			{ID: "call_2", Name: "file_read"},
		}},
		{Content: "好"},
	}}
	runner := newTestRunner(sessions, model, tools)

	if _, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "順序"}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	messages, err := sessions.ListMessages(context.Background(), sessions.session.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	order := []string{}
	for _, message := range messages {
		if strings.EqualFold(message.Role, "tool") {
			order = append(order, message.ToolCallID)
		}
	}
	if len(order) != 2 || order[0] != "call_1" || order[1] != "call_2" {
		t.Fatalf("order = %v, want the original tool_calls order", order)
	}
	assertToolCallProtocol(t, messages)
}

// TestRunSerializesWritesBetweenReads 檢查寫入工具不會和讀取重疊。
func TestRunSerializesWritesBetweenReads(t *testing.T) {
	var mu sync.Mutex
	active := 0
	overlapped := false
	tools := &fakeTools{
		definitions: []domain.ToolDefinition{
			{Name: "file_read", ReadOnly: true},
			{Name: "file_write", RequiresPermission: true},
		},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			mu.Lock()
			active++
			if call.Name == "file_write" && active > 1 {
				overlapped = true
			}
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
		},
	}
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{
			{ID: "call_1", Name: "file_read"},
			{ID: "call_2", Name: "file_write"},
			{ID: "call_3", Name: "file_read"},
		}},
		{Content: "好"},
	}}
	runner := newTestRunner(sessions, model, tools)

	if _, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "混合"}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if overlapped {
		t.Fatal("a write tool ran concurrently with another tool")
	}
}

// TestParallelToolEventsAreSerialized 保護事件輸出：下游 event log 以遞增 sequence
// 寫入，不可併發呼叫。這個測試在 -race 下會抓到未加鎖的實作。
func TestParallelToolEventsAreSerialized(t *testing.T) {
	emitted := 0
	sink := &serializedSink{emit: func(domain.EngineEvent) error {
		emitted++ // 故意不加鎖：唯一的保護必須來自 serializedSink
		return nil
	}}

	var waiter sync.WaitGroup
	for index := 0; index < 16; index++ {
		waiter.Add(1)
		go func() {
			defer waiter.Done()
			if err := sink.emitEvent("tool.execution.update", nil); err != nil {
				t.Errorf("emitEvent: %v", err)
			}
		}()
	}
	waiter.Wait()

	if emitted != 16 {
		t.Fatalf("emitted = %d, want 16", emitted)
	}
}

// TestParallelizableExcludesApprovalRequiredTools 保護一個原本只靠巧合成立的不變式：
// ApprovalCoordinator 每個 run 只允許一筆 pending approval，
// 同群組內兩個需要核准的工具會讓第二個 Begin 衝突，進而讓整個 run 失敗。
func TestParallelizableExcludesApprovalRequiredTools(t *testing.T) {
	definitions := []domain.ToolDefinition{
		{Name: "file_read", ReadOnly: true},
		{Name: "file_search", ReadOnly: true},
	}
	approvals := &fakeApprovals{required: map[string]bool{"file_search": true}}

	parallel := parallelizableToolNames(definitions, approvals)

	if !parallel["file_read"] {
		t.Error("file_read should stay parallelizable")
	}
	if parallel["file_search"] {
		t.Error("an approval-required tool must never join a parallel group")
	}
}

func TestParallelizableWithoutCoordinatorKeepsAllReadOnlyTools(t *testing.T) {
	definitions := []domain.ToolDefinition{{Name: "file_read", ReadOnly: true}}

	if parallel := parallelizableToolNames(definitions, nil); !parallel["file_read"] {
		t.Error("without an approval coordinator every read-only tool stays parallelizable")
	}
}

func TestVisibleApprovalArgumentsHidesFileContents(t *testing.T) {
	visible := visibleApprovalArguments(map[string]any{
		"path":                  "/sandbox/HELLO.MD",
		"old_text":              "large old content",
		"new_text":              "large new content",
		"expected_replacements": 1,
		"nested": map[string]any{
			"content": "nested content",
			"mode":    "append",
		},
	})
	if visible["path"] != "/sandbox/HELLO.MD" || visible["expected_replacements"] != 1 {
		t.Fatalf("decision parameters were removed: %+v", visible)
	}
	if _, exists := visible["old_text"]; exists {
		t.Fatal("old_text leaked into approval arguments")
	}
	if _, exists := visible["new_text"]; exists {
		t.Fatal("new_text leaked into approval arguments")
	}
	nested, _ := visible["nested"].(map[string]any)
	if _, exists := nested["content"]; exists || nested["mode"] != "append" {
		t.Fatalf("nested content redaction = %+v", nested)
	}
}

func TestBuiltinFallbackRequiresActualShellExecutionFailure(t *testing.T) {
	call := domain.ToolCall{ID: "call_shell", Name: "shell_exec"}
	if !shouldEnableBuiltinFallback(call, domain.ToolExecution{
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    "command not found",
		IsError:    true,
		Details:    map[string]any{"exit_code": 127},
	}) {
		t.Fatal("actual shell execution failure did not enable built-in fallback")
	}
	for _, details := range []map[string]any{
		{"approval_decision": domain.ToolApprovalDeny},
		{"approval_interrupted": true},
		{"refused": true},
		{"loop_guard": true},
	} {
		if shouldEnableBuiltinFallback(call, domain.ToolExecution{IsError: true, Details: details}) {
			t.Fatalf("non-execution result enabled fallback: %+v", details)
		}
	}
	if shouldEnableBuiltinFallback(domain.ToolCall{Name: "file_read"}, domain.ToolExecution{IsError: true}) {
		t.Fatal("non-shell failure enabled built-in fallback")
	}
}

func TestPermanentApprovalSkipsLaterPromptsInSameRun(t *testing.T) {
	coordinator := approval.NewCoordinator([]string{"shell_exec"})
	executed := 0
	tools := &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "shell_exec", RequiresPermission: true}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			executed++
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
		},
	}
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{
			{ID: "call_1", Name: "shell_exec", Arguments: map[string]any{"command": "first"}},
			{ID: "call_2", Name: "shell_exec", Arguments: map[string]any{"command": "second"}},
		}},
		{Content: "完成"},
	}}
	sessions := newMemorySessions(testSession())
	runner := newTestRunner(sessions, model, tools)
	runner.Approvals = coordinator
	approvalRequests := 0

	_, err := runner.Run(context.Background(), Input{RunID: "run_permanent", Session: testSession(), UserInput: "執行兩步"}, func(event domain.EngineEvent) error {
		if event.Type != "run.approval_required" {
			return nil
		}
		approvalRequests++
		request := event.Payload["approval"].(domain.ToolApprovalRequest)
		return coordinator.Decide("run_permanent", domain.ToolApprovalDecisionInput{
			ApprovalID: request.ID,
			Decision:   domain.ToolApprovalApprove,
			Permanent:  true,
		})
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if approvalRequests != 1 || executed != 2 {
		t.Fatalf("approval requests = %d, executed = %d; want 1,2", approvalRequests, executed)
	}
}

func TestPersistedPermanentApprovalSkipsPromptInFutureRun(t *testing.T) {
	approvals := &fakeApprovals{required: map[string]bool{"file_write": true}}
	executed := 0
	tools := &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "file_write", RequiresPermission: true}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			executed++
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
		},
	}
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_write", Name: "file_write", Arguments: map[string]any{"path": "HELLO.MD", "content": "hello"}}}},
		{Content: "完成"},
	}}
	session := testSession()
	session.PermanentToolApproval = true
	sessions := newMemorySessions(session)
	runner := newTestRunner(sessions, model, tools)
	runner.Approvals = approvals

	if _, err := runner.Run(context.Background(), Input{Session: session, UserInput: "建立檔案"}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if executed != 1 || len(approvals.begun) != 0 {
		t.Fatalf("executed = %d, approvals = %d; want 1,0", executed, len(approvals.begun))
	}
}

// TestRunIsolatesApprovalRequiredReadOnlyTools 端到端證明：即使工具是 read-only，
// 只要需要核准就必須各自成組，不能併發。
func TestRunIsolatesApprovalRequiredReadOnlyTools(t *testing.T) {
	approvals := &fakeApprovals{required: map[string]bool{"file_read": true}}
	var mu sync.Mutex
	active := 0
	overlapped := false
	tools := &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "file_read", ReadOnly: true}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			mu.Lock()
			active++
			if active > 1 {
				overlapped = true
			}
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
		},
	}
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{
			{ID: "call_1", Name: "file_read"},
			{ID: "call_2", Name: "file_read"},
		}},
		{Content: "好"},
	}}
	runner := newTestRunner(sessions, model, tools)
	runner.Approvals = approvals

	if _, err := runner.Run(context.Background(), Input{RunID: "run_1", Session: testSession(), UserInput: "核准型讀取"}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if overlapped {
		t.Fatal("approval-required tools ran concurrently; the coordinator only allows one pending approval per run")
	}
	if len(approvals.begun) != 2 {
		t.Fatalf("began %d approvals, want 2", len(approvals.begun))
	}
}
