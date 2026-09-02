package harness

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/approval"
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newTestRunner(sessions *memorySessions, model *scriptedModel, tools *fakeTools) *Runner {
	return &Runner{
		Model:        model,
		Tools:        tools,
		Sessions:     sessions,
		Context:      &ContextManager{Model: model, Sessions: sessions},
		Budget:       domain.RunBudget{MaxTurns: 4},
		SystemPrompt: "system",
	}
}

type nativeCapableProviderModel struct {
	ports.Model
}

func (nativeCapableProviderModel) Capabilities(string, string) domain.ModelCapabilities {
	return domain.ModelCapabilities{}
}

func (nativeCapableProviderModel) DefaultProviderID() string  { return "native-provider" }
func (nativeCapableProviderModel) HasProvider(id string) bool { return id == "native-provider" }
func (nativeCapableProviderModel) ListProviders() []domain.ProviderDescriptor {
	return []domain.ProviderDescriptor{{ID: "native-provider", SupportsNativeToolCalls: true}}
}

func TestEffectiveToolCallModePrefersProviderNativeCapability(t *testing.T) {
	model := nativeCapableProviderModel{Model: &scriptedModel{}}
	if mode := effectiveToolCallMode(model, "native-provider", ToolCallModeInstruction); mode != ToolCallModeNative {
		t.Fatalf("effective mode = %q, want native", mode)
	}
	if mode := effectiveToolCallMode(&scriptedModel{}, "", ToolCallModeInstruction); mode != ToolCallModeInstruction {
		t.Fatalf("fallback mode = %q, want instruction", mode)
	}
}

func TestRunReturnsFinalAssistantMessage(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{{Content: "完成了"}}}
	runner := newTestRunner(sessions, model, &fakeTools{})

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "做點事"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Message.Content != "完成了" {
		t.Errorf("content = %q, want 完成了", result.Message.Content)
	}
}

func TestRunPreservesUserToolResultOrderOnFollowUpTurn(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "shell_exec", Arguments: map[string]any{"command": "update"}}}},
		{Content: "已完成更新。"},
	}}
	tools := &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "shell_exec"}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "exit_code: 0"}, nil
		},
	}
	runner := newTestRunner(sessions, model, tools)

	if _, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "更新檔案"}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(model.requests))
	}
	followUp := model.requests[1]
	if followUp.UserPrompt != "" {
		t.Fatalf("follow-up user prompt = %q, want empty because it already precedes tool output in history", followUp.UserPrompt)
	}
	if len(followUp.History) != 3 {
		t.Fatalf("follow-up history = %+v, want user, assistant tool call, and tool result", followUp.History)
	}
	if followUp.History[0].Role != "user" || followUp.History[0].Content != "更新檔案" ||
		followUp.History[1].Role != "assistant" || len(followUp.History[1].ToolCalls) != 1 ||
		followUp.History[2].Role != "tool" || followUp.History[2].ToolCallID != "call_1" {
		t.Fatalf("follow-up history order is invalid: %+v", followUp.History)
	}
}

func TestRunSkipsIdenticalRepeatedSideEffectAndFinalizes(t *testing.T) {
	sessions := newMemorySessions(testSession())
	arguments := map[string]any{"path": "story.md", "old_text": "a", "new_text": "b"}
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "file_edit", Arguments: arguments}}},
		{ToolCalls: []domain.ToolCall{{ID: "call_2", Name: "file_edit", Arguments: arguments}}},
		{Content: "已使用第一次成功寫入的結果完成。"},
	}}
	executed := 0
	tools := &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "file_edit", Capabilities: []string{"atomic-replace"}}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			executed++
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
		},
	}
	runner := newTestRunner(sessions, model, tools)

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "修改故事"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if executed != 1 {
		t.Fatalf("executed = %d, want identical side effect only once", executed)
	}
	if result.Message.Content != "已使用第一次成功寫入的結果完成。" {
		t.Fatalf("result = %q", result.Message.Content)
	}
	if len(model.requests) != 3 || len(model.requests[2].Tools) != 0 || !strings.Contains(model.requests[2].PhasePrompt, "重複操作防護") {
		t.Fatalf("finalization request = %+v", model.requests[2])
	}
	finalRequest := model.requests[2]
	if !strings.Contains(finalRequest.ToolPrompt, "Session 工具能力目錄") || !strings.Contains(finalRequest.ToolPrompt, "file_edit") || strings.Contains(finalRequest.ToolPrompt, "本輪沒有可用") {
		t.Fatalf("finalization lost the session tool catalog: %q", finalRequest.ToolPrompt)
	}
	if !strings.Contains(finalRequest.PhasePrompt, "已確認的工具執行事實") || !strings.Contains(finalRequest.PhasePrompt, "file_edit → story.md：ok") {
		t.Fatalf("finalization lost confirmed mutation facts: %q", finalRequest.PhasePrompt)
	}
}

func TestRunAllowsDistinctSuccessfulEditsToSameResource(t *testing.T) {
	sessions := newMemorySessions(testSession())
	responses := make([]domain.ModelResponse, 0, 4)
	for index := 1; index <= 3; index++ {
		responses = append(responses, domain.ModelResponse{ToolCalls: []domain.ToolCall{{
			ID:   fmt.Sprintf("call_%d", index),
			Name: "file_edit",
			Arguments: map[string]any{
				"path": "story.md", "old_text": fmt.Sprintf("old-%d", index), "new_text": fmt.Sprintf("new-%d", index),
			},
		}}})
	}
	responses = append(responses, domain.ModelResponse{Content: "三項不同修正均已完成。"})
	model := &scriptedModel{responses: responses}
	executed := 0
	tools := &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "file_edit", Capabilities: []string{"atomic-replace"}}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			executed++
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
		},
	}
	runner := newTestRunner(sessions, model, tools)

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "把故事調整到指定長度"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if executed != 3 {
		t.Fatalf("executed = %d, want 3 distinct edits", executed)
	}
	if result.Message.Content != "三項不同修正均已完成。" {
		t.Fatalf("result = %q", result.Message.Content)
	}
	finalRequest := model.requests[len(model.requests)-1]
	if len(finalRequest.Tools) == 0 || strings.Contains(finalRequest.PhasePrompt, "重複操作防護") {
		t.Fatalf("distinct edits were incorrectly forced into finalization: %+v", finalRequest)
	}
}

func TestRunFinalizesAfterRepeatedSuccessfulMutationStrategy(t *testing.T) {
	sessions := newMemorySessions(testSession())
	responses := make([]domain.ModelResponse, 0, maxSuccessfulMutationsPerStrategy+1)
	for index := 1; index <= maxSuccessfulMutationsPerStrategy; index++ {
		responses = append(responses, domain.ModelResponse{ToolCalls: []domain.ToolCall{{
			ID:   fmt.Sprintf("call_%d", index),
			Name: "file_write",
			Arguments: map[string]any{
				"path": "story.md", "content": fmt.Sprintf("draft-%d", index), "overwrite": true,
			},
		}}})
	}
	responses = append(responses, domain.ModelResponse{Content: "已停止重複完整覆寫。"})
	model := &scriptedModel{responses: responses}
	executed := 0
	tools := &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "file_write", Capabilities: []string{"atomic-replace"}}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			executed++
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
		},
	}
	runner := newTestRunner(sessions, model, tools)

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "更新故事"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if executed != maxSuccessfulMutationsPerStrategy {
		t.Fatalf("executed = %d, want %d", executed, maxSuccessfulMutationsPerStrategy)
	}
	if result.Message.Content != "已停止重複完整覆寫。" {
		t.Fatalf("result = %q", result.Message.Content)
	}
	finalRequest := model.requests[len(model.requests)-1]
	if len(finalRequest.Tools) != 0 || !strings.Contains(finalRequest.PhasePrompt, "相同控制策略") {
		t.Fatalf("repeated successful strategy did not force finalization: %+v", finalRequest)
	}
}

func TestRunBlocksRepeatedFailedMutationStrategyButAllowsCorrectedStrategy(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "file_write", Arguments: map[string]any{"path": "story.md", "content": "draft 1", "overwrite": false}}}},
		{ToolCalls: []domain.ToolCall{{ID: "call_2", Name: "file_write", Arguments: map[string]any{"path": "story.md", "content": "draft 2", "overwrite": false}}}},
		{ToolCalls: []domain.ToolCall{{ID: "call_3", Name: "file_write", Arguments: map[string]any{"path": "story.md", "content": "draft 3", "overwrite": false}}}},
		{ToolCalls: []domain.ToolCall{{ID: "call_4", Name: "file_write", Arguments: map[string]any{"path": "story.md", "content": "corrected", "overwrite": true}}}},
		{Content: "已用修正後的策略完成寫入。"},
	}}
	executed := 0
	tools := &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "file_write", Capabilities: []string{"atomic-replace"}}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			executed++
			if overwrite, _ := call.Arguments["overwrite"].(bool); !overwrite {
				return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "file exists", IsError: true}, nil
			}
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
		},
	}
	runner := newTestRunner(sessions, model, tools)
	runner.Budget.MaxTurns = 6

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "更新故事"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if executed != 3 {
		t.Fatalf("executed = %d, want two failed attempts plus one corrected attempt", executed)
	}
	if result.Message.Content != "已用修正後的策略完成寫入。" {
		t.Fatalf("result = %q", result.Message.Content)
	}
	messages, err := sessions.ListMessages(context.Background(), testSession().ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var guarded bool
	for _, message := range messages {
		if message.ToolCallID == "call_3" && message.IsError && message.Metadata["repeated_failure"] == true {
			guarded = true
		}
	}
	if !guarded {
		t.Fatal("third unchanged failed strategy was not blocked by the loop guard")
	}
}

// 唯讀工具在系統工具優先階段就會公開；只有寫入型內建工具要等 Shell 實際失敗。
func TestRunStartsWithReadOnlyToolsAndSystemShell(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{{Content: "完成了"}}}
	definitions := []domain.ToolDefinition{
		{Name: "file_read", Description: "讀取 Sandbox 內的檔案。", ReadOnly: true},
		{Name: "directory_list", Description: "列出 Sandbox 內的目錄。", ReadOnly: true},
		{Name: "file_write", Description: "寫入 Sandbox 內的檔案。", RequiresPermission: true},
		{Name: "shell_exec", Description: "執行主機命令。"},
	}
	runner := newTestRunner(sessions, model, &fakeTools{definitions: definitions})
	events := []domain.EngineEvent{}

	_, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "請讀取檔案"}, func(event domain.EngineEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	request := model.requests[0]
	if request.SystemPrompt != "system" || request.UserPrompt != "請讀取檔案" || len(request.History) != 0 {
		t.Fatalf("prompt sections were not separated: system=%q user=%q history=%+v", request.SystemPrompt, request.UserPrompt, request.History)
	}
	// 工具清單本身走 OpenAI-compatible 的 tools 欄位；文字提示只補「怎麼用」的規則，
	// 不再重列一次工具名稱。
	for _, expected := range []string{"本輪已透過 OpenAI-compatible tools 欄位提供", "不可要求使用者代為執行", "ＨＥＬＬＯ．ＭＤ 應使用 HELLO.MD"} {
		if !strings.Contains(request.ToolPrompt, expected) {
			t.Errorf("tools prompt does not contain %q: %s", expected, request.ToolPrompt)
		}
	}
	// 唯讀工具沒有副作用，第一輪就要能用，否則每個讀取需求都得先跑一個註定失敗的
	// Shell 命令，多花一輪卻沒有任何產出。
	exposedNames := strings.Join(availableToolNamesSorted(request.Tools), ",")
	for _, exposed := range []string{"file_read", "directory_list", "shell_exec"} {
		if !strings.Contains(exposedNames, exposed) {
			t.Errorf("read-only tool %q must be available in the system-first stage: %s", exposed, exposedNames)
		}
	}
	if strings.Contains(exposedNames, "file_write") {
		t.Errorf("write tool was exposed before Shell failure: %s", exposedNames)
	}
	for _, expected := range []string{"Host 執行環境", "GOOS=" + runtime.GOOS, runtime.GOARCH, "shell_exec 本輪可呼叫：true", "必須透過 shell_exec 實際執行", "direct mode", "shell mode"} {
		if !strings.Contains(request.HostPrompt, expected) {
			t.Errorf("host prompt does not contain %q: %s", expected, request.HostPrompt)
		}
	}
	if !strings.Contains(request.PhasePrompt, "大型目錄") || !strings.Contains(request.PhasePrompt, "深度 1–2") {
		t.Fatalf("large-scope exploration policy missing: %s", request.PhasePrompt)
	}
	if !strings.Contains(request.PhasePrompt, "OS 系統工具優先階段") || !strings.Contains(request.PhasePrompt, "寫入型內建工具") {
		t.Fatalf("system-first tool policy missing: %s", request.PhasePrompt)
	}
	if !strings.Contains(request.PhasePrompt, "唯讀內建工具") || !strings.Contains(request.PhasePrompt, "不必先用 shell_exec 試探") {
		t.Fatalf("read-only availability is not stated in the phase prompt: %s", request.PhasePrompt)
	}
	for _, expected := range []string{"使用者可見的工作進度", "我打算分三個步驟完成", "語言與語系", "不得固定綁定", "Awaiting tool execution results"} {
		if !strings.Contains(request.PhasePrompt, expected) {
			t.Errorf("progress presentation prompt does not contain %q: %s", expected, request.PhasePrompt)
		}
	}
	if names := strings.Join(availableToolNamesSorted(request.Tools), ","); names != "directory_list,file_read,shell_exec" {
		t.Fatalf("request tools = %s, want the read-only tools plus shell_exec", names)
	}
	for _, event := range events {
		if event.Type != "turn.start" {
			continue
		}
		names, ok := event.Payload["available_tools"].([]string)
		if !ok {
			t.Fatalf("available_tools = %#v, want []string", event.Payload["available_tools"])
		}
		if strings.Join(names, ",") != "directory_list,file_read,shell_exec" {
			t.Fatalf("available_tools = %v, want the read-only tools plus shell_exec", names)
		}
		if event.Payload["tool_stage"] != toolStageSystemShell {
			t.Fatalf("tool_stage = %v, want %s", event.Payload["tool_stage"], toolStageSystemShell)
		}
		return
	}
	t.Fatal("turn.start event was not emitted")
}

func TestRunUnlocksBuiltinToolsAfterShellExecutionFailure(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_shell", Name: "shell_exec", Arguments: map[string]any{"command": "missing-command"}}}},
		{ToolCalls: []domain.ToolCall{{ID: "call_read", Name: "file_read", Arguments: map[string]any{"path": "README.md"}}}},
		{Content: "已改用內建檔案工具完成。"},
	}}
	definitions := []domain.ToolDefinition{
		{Name: "file_read", Description: "讀取檔案", ReadOnly: true},
		{Name: "directory_list", Description: "列出目錄", ReadOnly: true},
		{Name: "file_write", Description: "寫入檔案", RequiresPermission: true},
		{Name: "shell_exec", Description: "執行主機命令"},
	}
	executed := []string{}
	tools := &fakeTools{
		definitions: definitions,
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			executed = append(executed, call.Name)
			if call.Name == "shell_exec" {
				return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "executable file not found", IsError: true, Details: map[string]any{"exit_code": 127}}, nil
			}
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "read ok"}, nil
		},
	}
	runner := newTestRunner(sessions, model, tools)
	events := []domain.EngineEvent{}

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "讀取 README"}, func(event domain.EngineEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Message.Content != "已改用內建檔案工具完成。" || strings.Join(executed, ",") != "shell_exec,file_read" {
		t.Fatalf("result = %q, executed = %v", result.Message.Content, executed)
	}
	if len(model.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(model.requests))
	}
	// 第一輪已包含唯讀工具，但寫入型工具仍要等 Shell 實際失敗才解鎖。
	firstNames := strings.Join(availableToolNamesSorted(model.requests[0].Tools), ",")
	if firstNames != "directory_list,file_read,shell_exec" {
		t.Fatalf("first request tools = %s, want read-only tools plus shell_exec", firstNames)
	}
	secondNames := strings.Join(availableToolNamesSorted(model.requests[1].Tools), ",")
	if secondNames != "directory_list,file_read,file_write,shell_exec" {
		t.Fatalf("second request tools = %s, want full catalog", secondNames)
	}
	if !strings.Contains(model.requests[1].PhasePrompt, "內建工具備援階段") {
		t.Fatalf("fallback phase prompt missing: %s", model.requests[1].PhasePrompt)
	}
	if !hasEngineEvent(events, "tools.fallback_enabled") {
		t.Fatal("tools.fallback_enabled event was not emitted")
	}
}

func TestHostEnvironmentPromptDistinguishesCatalogFromCurrentCallableTools(t *testing.T) {
	prompt := hostEnvironmentPrompt(
		[]domain.ToolDefinition{{Name: "shell_exec"}},
		nil,
	)
	for _, expected := range []string{"shell_exec 位於 Session 工具目錄：true", "shell_exec 本輪可呼叫：false", "Harness 正在收斂", "不得誤稱系統沒有 Shell 工具"} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("host prompt does not contain %q: %s", expected, prompt)
		}
	}
}

func TestRunRejectsTextualImitationOfNativeToolCall(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{{
		Content: "to=directory_list code\n{\"path\":\".\"}",
	}}}
	runner := newTestRunner(sessions, model, &fakeTools{definitions: []domain.ToolDefinition{{
		Name: "directory_list", Description: "列出目錄", ReadOnly: true,
	}}})

	_, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "列出目錄"}, nil)
	if !errors.Is(err, domain.ErrProviderProtocol) {
		t.Fatalf("Run error = %v, want ErrProviderProtocol", err)
	}
	if !strings.Contains(err.Error(), "tool_calls") {
		t.Fatalf("Run error = %v, want native tool-call diagnostic", err)
	}
	messages, listErr := sessions.ListMessages(context.Background(), testSession().ID)
	if listErr != nil {
		t.Fatalf("ListMessages: %v", listErr)
	}
	for _, message := range messages {
		if message.Role == "assistant" {
			t.Fatalf("textual tool imitation was persisted as assistant output: %+v", message)
		}
	}
}

func TestRunExecutesToolsAndFeedsResultsBack(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "file_read"}}},
		{Content: "讀完了"},
	}}
	tools := &fakeTools{definitions: []domain.ToolDefinition{{Name: "file_read", ReadOnly: true}}, execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
		return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "檔案內容"}, nil
	}}
	runner := newTestRunner(sessions, model, tools)

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "讀檔"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Message.Content != "讀完了" {
		t.Errorf("content = %q, want 讀完了", result.Message.Content)
	}

	second := model.requests[1]
	if len(second.History) == 0 {
		t.Fatal("second turn carried no messages")
	}
	last := second.History[len(second.History)-1]
	if !strings.EqualFold(last.Role, "tool") || last.Content != "檔案內容" {
		t.Errorf("last message = %+v, want the tool result", last)
	}
}

func TestRunForcesFinalizationAfterAutonomousToolTurnLimitWithoutPlanLoop(t *testing.T) {
	session := testSession()
	session.LockPlans = true
	sessions := newMemorySessions(session)
	plans, err := filestore.NewPlanRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewPlanRepository: %v", err)
	}
	plan, err := domain.NewPlan(session.ID, domain.CreatePlanInput{
		Title: "長任務", Steps: []domain.CreatePlanStepInput{{Title: "分析目錄", Verification: "摘要完成"}},
	}, time.Now())
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	if _, err := plans.Create(context.Background(), plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "directory_list", Arguments: map[string]any{"path": "."}}}},
		{ToolCalls: []domain.ToolCall{{ID: "call_2", Name: "file_search", Arguments: map[string]any{"path": ".", "query": "README"}}}},
		{Content: "已根據目前取得的目錄與搜尋結果完成摘要。"},
	}}
	tools := &fakeTools{
		definitions: []domain.ToolDefinition{
			{Name: "directory_list", Description: "列出目錄", ReadOnly: true},
			{Name: "file_search", Description: "搜尋檔案", ReadOnly: true},
		},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "有限範圍的觀察結果"}, nil
		},
	}
	runner := newTestRunner(sessions, model, tools)
	runner.Plans = plans
	runner.MaxAutonomousToolTurns = 2

	result, err := runner.Run(context.Background(), Input{Session: session, UserInput: "分析目前目錄"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Message.Content != "已根據目前取得的目錄與搜尋結果完成摘要。" {
		t.Fatalf("answer = %q", result.Message.Content)
	}
	if result.Message.Metadata["termination"] != "plan_no_progress" {
		t.Fatalf("final metadata = %+v, want visible plan no-progress termination", result.Message.Metadata)
	}
	if len(model.requests) != 3 {
		t.Fatalf("model requests = %d, want 3", len(model.requests))
	}
	forced := model.requests[2]
	if len(forced.Tools) != 0 {
		t.Fatalf("forced finalization still advertised tools: %+v", forced.Tools)
	}
	if !strings.Contains(forced.PhasePrompt, "強制收斂階段") || !strings.Contains(forced.ToolPrompt, "Session 工具能力目錄") || strings.Contains(forced.ToolPrompt, "本輪沒有可用") {
		t.Fatalf("forced finalization prompts missing: phase=%q tools=%q", forced.PhasePrompt, forced.ToolPrompt)
	}
}

func TestRunDoesNotApplyFixedAutonomousToolLimitByDefault(t *testing.T) {
	sessions := newMemorySessions(testSession())
	const toolTurns = 17
	responses := make([]domain.ModelResponse, 0, toolTurns+1)
	for index := 0; index < toolTurns; index++ {
		responses = append(responses, domain.ModelResponse{ToolCalls: []domain.ToolCall{{
			ID:   fmt.Sprintf("call_%d", index),
			Name: "directory_list",
			Arguments: map[string]any{
				"path": fmt.Sprintf("dir-%d", index),
			},
		}}})
	}
	responses = append(responses, domain.ModelResponse{Content: "長任務已完成。"})
	model := &scriptedModel{responses: responses}
	tools := &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "directory_list", ReadOnly: true}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
		},
	}
	runner := newTestRunner(sessions, model, tools)
	runner.Budget.MaxTurns = toolTurns + 2
	runner.MaxAutonomousToolTurns = 0

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "執行長任務"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Message.Content != "長任務已完成。" || len(model.requests) != toolTurns+1 {
		t.Fatalf("result=%+v requests=%d, want all %d tool turns followed by final answer", result.Message, len(model.requests), toolTurns)
	}
	if len(model.requests[toolTurns].Tools) == 0 {
		t.Fatal("default unlimited mode unexpectedly forced finalization")
	}
}

func TestRunInstructionModeExecutesJSONToolDecisionAndFeedsTextResultBack(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: `{"type":"tool_use","tool":"directory_list","input":{"path":"."},"reason":"讀取目錄"}`},
		{Content: "已找到 target.txt"},
	}}
	tools := &fakeTools{
		definitions: instructionTestDefinitions(),
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: `{"entries":["target.txt"]}`}, nil
		},
	}
	runner := newTestRunner(sessions, model, tools)
	runner.ToolCallMode = ToolCallModeInstruction

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "列出目前目錄"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Message.Content != "已找到 target.txt" {
		t.Fatalf("answer = %q", result.Message.Content)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(model.requests))
	}
	if len(model.requests[0].Tools) != 0 {
		t.Fatalf("instruction mode advertised native tools: %+v", model.requests[0].Tools)
	}
	if !strings.Contains(model.requests[0].ToolPrompt, "Harness 工具指令協定") {
		t.Fatalf("instruction protocol missing from tools prompt: %s", model.requests[0].ToolPrompt)
	}
	second := model.requests[1]
	if !strings.Contains(second.PhasePrompt, "收斂與最終回答階段") || !strings.Contains(second.PhasePrompt, "不得揭露") {
		t.Fatalf("second turn did not enter the finalization phase: %s", second.PhasePrompt)
	}
	foundToolResult := false
	for _, message := range second.History {
		if message.Role == "tool" || len(message.ToolCalls) > 0 {
			t.Fatalf("native tool protocol leaked into second request: %+v", message)
		}
		if message.Role == "user" && strings.Contains(message.Content, `"type":"tool_result"`) && strings.Contains(message.Content, "target.txt") {
			foundToolResult = true
		}
	}
	if !foundToolResult {
		t.Fatalf("second request did not contain textual tool result: %+v", second.History)
	}
	messages, err := sessions.ListMessages(context.Background(), testSession().ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	for _, message := range messages {
		if (message.Role == "tool" || len(message.ToolCalls) > 0) && message.Metadata["internal"] != true {
			t.Fatalf("internal loop artifact was not marked hidden: %+v", message)
		}
	}
}

func TestRunInstructionModeExecutesEmbeddedJSONToolDecisionAndContinues(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: `我會先確認目錄內容。{"type":"tool_use","tool":"directory_list","input":{"path":"."},"reason":"盤點檔案"}`},
		{Content: "已完成目錄分析。"},
	}}
	tools := &fakeTools{
		definitions: instructionTestDefinitions(),
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: `{"entries":["README.md"]}`}, nil
		},
	}
	runner := newTestRunner(sessions, model, tools)
	runner.ToolCallMode = ToolCallModeInstruction

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "分析目錄"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Message.Content != "已完成目錄分析。" {
		t.Fatalf("answer = %q", result.Message.Content)
	}
	if len(model.requests) != 2 || len(model.requests[1].History) == 0 {
		t.Fatalf("loop did not continue after embedded tool decision: requests=%d", len(model.requests))
	}
	for _, message := range model.requests[1].History {
		if strings.Contains(message.Content, "我會先確認目錄內容") {
			t.Fatalf("provider preface leaked into tool transcript: %+v", message)
		}
	}
}

func TestRunInstructionModeRepairsMalformedToolDecision(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: `{"type":"tool_use","input":{"path":"."}}`},
		{Content: `{"type":"tool_use","tool":"directory_list","input":{"path":"."}}`},
		{Content: "已修正工具格式並完成目錄分析。"},
	}}
	tools := &fakeTools{
		definitions: instructionTestDefinitions(),
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: `{"entries":["README.md"]}`}, nil
		},
	}
	runner := newTestRunner(sessions, model, tools)
	runner.ToolCallMode = ToolCallModeInstruction

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "分析目錄"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Message.Content != "已修正工具格式並完成目錄分析。" {
		t.Fatalf("answer = %q", result.Message.Content)
	}
	if len(model.requests) != 3 || !strings.Contains(model.requests[1].ContextPrompt, "tool_protocol_repair") {
		t.Fatalf("protocol repair was not sent to the model: %+v", model.requests)
	}
	messages, err := sessions.ListMessages(context.Background(), testSession().ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	foundRepair := false
	for _, message := range messages {
		if message.Metadata["phase"] == "tool_protocol_repair" {
			foundRepair = message.Metadata["internal"] == true
		}
	}
	if !foundRepair {
		t.Fatal("malformed instruction was not retained as an internal repair message")
	}
}

func TestRunInstructionModeStopsVisiblyAfterRepeatedMalformedToolDecisions(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{{Content: `{"type":"tool_use","input":{}}`}}}
	runner := newTestRunner(sessions, model, &fakeTools{definitions: instructionTestDefinitions()})
	runner.ToolCallMode = ToolCallModeInstruction

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "執行計畫"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if model.index != maxToolProtocolRepairAttempts+1 {
		t.Fatalf("model calls = %d, want %d", model.index, maxToolProtocolRepairAttempts+1)
	}
	if internalAssistantMessage(result.Message) || !strings.Contains(result.Message.Content, "模型連續回傳不完整的工具指令") {
		t.Fatalf("result must be a visible recovery message: %+v", result.Message)
	}
}

// TestRunToolRejectionIsModelObservation 防止 BeforeTool 拒絕被誤當成整個 Run 的終止：
// 模型必須看得到拒絕結果，才有機會改用安全工具或向使用者說明。
func TestRunToolRejectionIsModelObservation(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_denied", Name: "shell_exec"}}},
		{Content: "權限不足，改用其他方式處理。"},
	}}
	runner := newTestRunner(sessions, model, &fakeTools{})
	runner.BeforeTool = func(context.Context, domain.Session, domain.ToolCall) error {
		return errors.New("工具被策略拒絕")
	}

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "執行指令"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Message.Content != "權限不足，改用其他方式處理。" {
		t.Fatalf("result = %q, want model fallback", result.Message.Content)
	}
	if model.index != 2 {
		t.Fatalf("model called %d times, want a second turn after rejection", model.index)
	}
}

func TestRunWaitsForApprovalBeforeExecutingHighRiskTool(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_approval", Name: "shell_exec", Arguments: map[string]any{"command": "pwd"}}}},
		{Content: "核准後已完成"},
	}}
	executed := make(chan struct{}, 1)
	runner := newTestRunner(sessions, model, &fakeTools{definitions: []domain.ToolDefinition{{Name: "shell_exec"}}, execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
		executed <- struct{}{}
		return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
	}})
	coordinator := approval.NewCoordinator([]string{"shell_exec"})
	runner.Approvals = coordinator
	approvalRequests := make(chan domain.ToolApprovalRequest, 1)
	type runOutcome struct {
		result domain.RunResult
		err    error
	}
	done := make(chan runOutcome, 1)
	go func() {
		result, err := runner.Run(context.Background(), Input{RunID: "run_approval", Session: testSession(), UserInput: "執行"}, func(event domain.EngineEvent) error {
			if event.Type == "run.approval_required" {
				request, _ := event.Payload["approval"].(domain.ToolApprovalRequest)
				approvalRequests <- request
			}
			return nil
		})
		done <- runOutcome{result: result, err: err}
	}()

	var request domain.ToolApprovalRequest
	select {
	case request = <-approvalRequests:
	case <-time.After(time.Second):
		t.Fatal("approval request was not emitted")
	}
	select {
	case <-executed:
		t.Fatal("tool executed before approval")
	default:
	}
	if err := coordinator.Decide("run_approval", domain.ToolApprovalDecisionInput{
		ApprovalID: request.ID,
		Decision:   domain.ToolApprovalApprove,
	}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatalf("Run: %v", outcome.err)
		}
		if outcome.result.Message.Content != "核准後已完成" {
			t.Fatalf("result = %+v", outcome.result)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not continue after approval")
	}
}

func TestRunDeniedApprovalReturnsObservationToModel(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_denied_by_human", Name: "shell_exec"}}},
		{Content: "使用者拒絕後已改走安全路徑"},
	}}
	executed := 0
	runner := newTestRunner(sessions, model, &fakeTools{definitions: []domain.ToolDefinition{{Name: "shell_exec"}}, execute: func(context.Context, domain.Session, domain.ToolCall) (domain.ToolExecution, error) {
		executed++
		return domain.ToolExecution{}, nil
	}})
	coordinator := approval.NewCoordinator([]string{"shell_exec"})
	runner.Approvals = coordinator
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), Input{RunID: "run_deny", Session: testSession(), UserInput: "執行"}, func(event domain.EngineEvent) error {
			if event.Type == "run.approval_required" {
				request := event.Payload["approval"].(domain.ToolApprovalRequest)
				return coordinator.Decide("run_deny", domain.ToolApprovalDecisionInput{
					ApprovalID: request.ID,
					Decision:   domain.ToolApprovalDeny,
					Reason:     "不允許此指令",
				})
			}
			return nil
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not continue after denial")
	}
	if executed != 0 {
		t.Fatalf("denied tool executed %d times", executed)
	}
	request := model.requests[1]
	if len(request.History) == 0 || !strings.Contains(request.History[len(request.History)-1].Content, "不允許此指令") {
		t.Fatalf("model did not receive denial observation: %+v", request.History)
	}
}

func TestRunDoesNotRequestApprovalForToolUnavailableToSession(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_hallucinated", Name: "shell_exec"}}},
		{Content: "已收到工具不可用的結果"},
	}}
	runner := newTestRunner(sessions, model, &fakeTools{}) // definitions 為空，代表本 Session 未公開此工具。
	runner.Approvals = approval.NewCoordinator([]string{"shell_exec"})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result, err := runner.Run(ctx, Input{RunID: "run_hallucinated", Session: testSession(), UserInput: "執行"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Message.Content != "已收到工具不可用的結果" {
		t.Fatalf("result = %+v", result)
	}
}

// TestRunBudgetMaxTurnsReturnsVisibleAssistantAndReplayableTranscript 防止上限被當成失敗：
// 使用者已等待多輪，工具協定必須完整保留，Result 也必須是 UI 不會隱藏的回答。
func TestRunBudgetMaxTurnsReturnsVisibleAssistantAndReplayableTranscript(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_loop", Name: "file_read"}}},
	}}
	runner := newTestRunner(sessions, model, &fakeTools{})
	runner.Budget.MaxTurns = 2
	events := []domain.EngineEvent{}

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "繞圈"}, func(event domain.EngineEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if model.index != 2 {
		t.Errorf("model called %d times, want 2", model.index)
	}
	if result.BudgetExceeded == nil || result.BudgetExceeded.Resource != domain.RunBudgetResourceTurns {
		t.Fatalf("budget result = %+v, want max-turns termination", result.BudgetExceeded)
	}
	if internalAssistantMessage(result.Message) || strings.TrimSpace(result.Message.Content) == "" {
		t.Errorf("last assistant = %+v, want a visible budget summary", result.Message)
	}
	if !strings.Contains(result.Message.Content, "工作持續次數過長") || !strings.Contains(result.Message.Content, "請告訴我「繼續」") {
		t.Errorf("last assistant = %q, want count safety-pause guidance", result.Message.Content)
	}
	if !hasEngineEvent(events, "run.budget_exceeded") {
		t.Error("run.budget_exceeded event was not emitted")
	}
	messages, listErr := sessions.ListMessages(context.Background(), sessions.session.ID)
	if listErr != nil {
		t.Fatalf("ListMessages: %v", listErr)
	}
	assertToolCallProtocol(t, messages)
}

func TestRunCompactsContextAndContinuesWithoutSafetyPause(t *testing.T) {
	session := testSession()
	sessions := newMemorySessions(session)
	appendTestMessage(t, sessions, domain.Message{Role: "user", Content: strings.Repeat("早期需求", 1_000)})
	appendTestMessage(t, sessions, domain.Message{Role: "assistant", Content: strings.Repeat("早期處理結果", 1_000)})
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: "已整理舊資料"},
		{Content: "整理上下文後已繼續完成工作。"},
	}}
	runner := newTestRunner(sessions, model, &fakeTools{})
	runner.Context = &ContextManager{
		Model: model, Sessions: sessions,
		Config: ContextConfig{MaxEstimatedTokens: 2_000, RetainMessages: 1},
	}
	events := []domain.EngineEvent{}

	result, err := runner.Run(context.Background(), Input{Session: session, UserInput: "請繼續目前工作"}, func(event domain.EngineEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.BudgetExceeded != nil || result.Message.Content != "整理上下文後已繼續完成工作。" {
		t.Fatalf("result = %+v, want completion after context compaction", result)
	}
	if !hasEngineEvent(events, "context.compacted") {
		t.Fatal("context.compacted event was not emitted")
	}
	if hasEngineEvent(events, "run.budget_exceeded") {
		t.Fatal("context compaction must not be reported as a safety pause")
	}
	if len(model.requests) < 2 || model.requests[1].UserPrompt != "請繼續目前工作" {
		t.Fatalf("latest user prompt was lost during prompt fitting: %+v", model.requests)
	}
}

func TestRunBudgetMaxTokensStopsBeforeExecutingNewSideEffects(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{{
		ToolCalls: []domain.ToolCall{{ID: "call_over_tokens", Name: "shell_exec"}},
		Usage:     domain.Usage{InputTokens: 30, OutputTokens: 20, TotalTokens: 50},
	}}}
	executed := 0
	runner := newTestRunner(sessions, model, &fakeTools{execute: func(context.Context, domain.Session, domain.ToolCall) (domain.ToolExecution, error) {
		executed++
		return domain.ToolExecution{}, nil
	}})
	runner.Budget.MaxTokens = 40

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "不要超支"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if executed != 0 {
		t.Fatalf("tool executed %d times after token budget was exhausted", executed)
	}
	if result.BudgetExceeded == nil || result.BudgetExceeded.Resource != domain.RunBudgetResourceTokens {
		t.Fatalf("budget result = %+v, want token termination", result.BudgetExceeded)
	}
	messages, _ := sessions.ListMessages(context.Background(), sessions.session.ID)
	assertToolCallProtocol(t, messages)
}

func TestRunBudgetMaxToolCallsExecutesOnlyAllowedPrefix(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{{ToolCalls: []domain.ToolCall{
		{ID: "call_allowed", Name: "shell_exec"},
		{ID: "call_over_budget", Name: "file_read"},
	}}}}
	executed := []string{}
	runner := newTestRunner(sessions, model, &fakeTools{definitions: []domain.ToolDefinition{{Name: "file_read", ReadOnly: true}, {Name: "shell_exec"}}, execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
		executed = append(executed, call.ID)
		return domain.ToolExecution{Content: "ok"}, nil
	}})
	runner.Budget.MaxToolCalls = 1

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "兩個工具"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Join(executed, ",") != "call_allowed" {
		t.Fatalf("executed = %v, want only call_allowed", executed)
	}
	if result.BudgetExceeded == nil || result.BudgetExceeded.Resource != domain.RunBudgetResourceToolCalls {
		t.Fatalf("budget result = %+v, want tool-call termination", result.BudgetExceeded)
	}
	messages, _ := sessions.ListMessages(context.Background(), sessions.session.ID)
	assertToolCallProtocol(t, messages)
}

func TestRunBudgetWallClockCancelsRunAsCompletedBudgetTermination(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{{Content: "太晚才回來"}}, onStream: func(int) {
		time.Sleep(10 * time.Millisecond)
	}}
	runner := newTestRunner(sessions, model, &fakeTools{})
	runner.Budget.MaxWallClock = time.Millisecond

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "限時工作"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.BudgetExceeded == nil || result.BudgetExceeded.Resource != domain.RunBudgetResourceWallClock {
		t.Fatalf("budget result = %+v, want wall-clock termination", result.BudgetExceeded)
	}
	if strings.TrimSpace(result.Message.Content) == "" {
		t.Fatal("wall-clock termination must still return an assistant result")
	}
	if !strings.Contains(result.Message.Content, "工作持續時間過長") || !strings.Contains(result.Message.Content, "請告訴我「繼續」") {
		t.Errorf("last assistant = %q, want time safety-pause guidance", result.Message.Content)
	}
}

func TestRunFinalAnswerAtTurnLimitCompletesWithoutBudgetMarker(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{{Content: "剛好完成"}}}
	runner := newTestRunner(sessions, model, &fakeTools{})
	runner.Budget.MaxTurns = 1

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "一次完成"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.BudgetExceeded != nil {
		t.Fatalf("natural completion was mislabeled as budget termination: %+v", result.BudgetExceeded)
	}
}

// TestRunCancelDuringToolLeavesReplayableTranscript 覆蓋原本會讓 session 永久壞掉的路徑：
// 工具執行中取消時，已完成工具的結果仍必須寫進 transcript，
// 而未執行的 tool_call 必須能在下一次組裝 context 時補齊，否則 Provider 會拒絕整個 session。
func TestRunCancelDuringToolLeavesReplayableTranscript(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{{ToolCalls: []domain.ToolCall{
		{ID: "call_1", Name: "shell_exec"},
		{ID: "call_2", Name: "shell_exec"},
	}}}}
	ctx, cancel := context.WithCancel(context.Background())
	executed := []string{}
	tools := &fakeTools{definitions: []domain.ToolDefinition{{Name: "shell_exec"}}, execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
		executed = append(executed, call.ID)
		cancel() // 模擬工具執行途中收到 CancelRun
		return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "副作用已經發生"}, nil
	}}
	runner := newTestRunner(sessions, model, tools)

	_, err := runner.Run(ctx, Input{Session: testSession(), UserInput: "跑指令"}, nil)
	if !errors.Is(err, domain.ErrCanceled) {
		t.Fatalf("err = %v, want ErrCanceled", err)
	}
	if len(executed) != 1 {
		t.Fatalf("executed %v, want only the first tool call", executed)
	}

	messages, err := sessions.ListMessages(context.Background(), sessions.session.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if !hasToolResult(messages, "call_1") {
		t.Error("the executed tool result was lost; its side effect is now unrecorded")
	}
	if hasToolResult(messages, "call_2") {
		t.Error("call_2 never ran and must not have a recorded result")
	}

	repaired := repairMessages(messages)
	assertToolCallProtocol(t, repaired)
}

func TestRunFailsWhenModelReturnsNothing(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{{}}}
	runner := newTestRunner(sessions, model, &fakeTools{})

	if _, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "hi"}, nil); err == nil {
		t.Fatal("expected an error when the model returns neither text nor tool calls")
	}
}

func TestRunRecordsOperationLifecycle(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{{Content: "好"}}}
	runner := newTestRunner(sessions, model, &fakeTools{})

	if _, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "hi"}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	types := sessions.entryTypes()
	for _, wanted := range []string{
		domain.SessionEntryOperationStarted,
		domain.SessionEntryTurnStarted,
		domain.SessionEntryTurnFinished,
		domain.SessionEntryOperationFinished,
	} {
		if !containsString(types, wanted) {
			t.Errorf("transcript is missing a %q entry; got %v", wanted, types)
		}
	}
}

func hasToolResult(messages []domain.Message, callID string) bool {
	for _, message := range messages {
		if strings.EqualFold(message.Role, "tool") && message.ToolCallID == callID {
			return true
		}
	}
	return false
}

// assertToolCallProtocol 檢查 OpenAI tool call 協定的兩個硬性條件：
// 每個 assistant tool_call 都有結果，且每個 tool 訊息都對應到前面的 tool_call。
func assertToolCallProtocol(t *testing.T, messages []domain.Message) {
	t.Helper()
	pending := map[string]bool{}
	for _, message := range messages {
		switch strings.ToLower(message.Role) {
		case "assistant":
			for _, call := range message.ToolCalls {
				pending[call.ID] = true
			}
		case "tool":
			if !pending[message.ToolCallID] {
				t.Errorf("tool message %q has no matching tool_call", message.ToolCallID)
			}
			delete(pending, message.ToolCallID)
		}
	}
	for id := range pending {
		t.Errorf("tool_call %q has no result; the provider would reject this transcript", id)
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func hasEngineEvent(events []domain.EngineEvent, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

// 本機模型常見的收尾失敗：只吐了思考（<think> 或 harmony 的 analysis 頻道）
// 就停住，回答是空的。直接讓整個 Run 失敗，使用者就是白等一輪。
func TestRunRetriesWhenTheModelOnlyProducesReasoning(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Reasoning: "使用者說 HELLO，我要回招呼。"},
		{Content: "你好，有什麼需要協助的嗎？"},
	}}
	runner := newTestRunner(sessions, model, &fakeTools{})

	events := []domain.EngineEvent{}
	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "HELLO"}, func(event domain.EngineEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Message.Content != "你好，有什麼需要協助的嗎？" {
		t.Fatalf("content = %q", result.Message.Content)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model was called %d times, want 2", len(model.requests))
	}
	var retried bool
	for _, event := range events {
		if event.Type == "run.empty_answer_retry" {
			retried = true
		}
	}
	if !retried {
		t.Fatal("no run.empty_answer_retry event was emitted")
	}
	// 空回答留在 transcript 供稽核，但不能當成使用者可見的回覆。
	stored, err := sessions.ListMessages(context.Background(), testSession().ID)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	for _, message := range stored {
		if message.Role != "assistant" || strings.TrimSpace(message.Content) != "" {
			continue
		}
		if internal, _ := message.Metadata["internal"].(bool); !internal {
			t.Fatalf("empty assistant message was not marked internal: %+v", message.Metadata)
		}
	}
}

// 明確要求之後還是只有思考，就必須誠實失敗，而不是無限重試。
func TestRunFailsClearlyWhenTheModelNeverAnswers(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{{Reasoning: "想了很久"}}}
	runner := newTestRunner(sessions, model, &fakeTools{})

	_, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "HELLO"}, nil)
	if err == nil {
		t.Fatal("Run should have failed")
	}
	if !strings.Contains(err.Error(), "沒有產生回答") {
		t.Fatalf("error = %v", err)
	}
	if len(model.requests) != 1+maxEmptyAnswerRetries {
		t.Fatalf("model was called %d times, want %d", len(model.requests), 1+maxEmptyAnswerRetries)
	}
}
