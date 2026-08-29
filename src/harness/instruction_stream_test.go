package harness

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"strings"
	"testing"
)

type instructionStreamingModel struct {
	deltas    [][]string
	responses []domain.ModelResponse
	index     int
}

func (model *instructionStreamingModel) Stream(_ context.Context, _ domain.ModelRequest, sink ports.ModelEventSink) (domain.ModelResponse, error) {
	turn := model.index
	model.index++
	for _, delta := range model.deltas[turn] {
		if err := sink(domain.ModelEvent{Type: domain.ModelEventTextDelta, Delta: delta}); err != nil {
			return domain.ModelResponse{}, err
		}
	}
	return model.responses[turn], nil
}

func TestInstructionTextStreamReleasesOrdinaryAnswerIncrementally(t *testing.T) {
	stream := &instructionTextStream{}
	emitted := []string{}
	emit := func(delta string) error {
		emitted = append(emitted, delta)
		return nil
	}
	for _, delta := range []string{"你", "好", "，", "這是串流回答。"} {
		if err := stream.Push(delta, emit); err != nil {
			t.Fatalf("Push: %v", err)
		}
	}
	if strings.Join(emitted, "") != "你好，這是串流回答。" || len(emitted) != 4 {
		t.Fatalf("emitted = %#v, want four incremental chunks", emitted)
	}
}

func TestInstructionTextStreamKeepsToolJSONPrivate(t *testing.T) {
	stream := &instructionTextStream{}
	emitted := []string{}
	emit := func(delta string) error {
		emitted = append(emitted, delta)
		return nil
	}
	for _, delta := range []string{"{", `"type":"tool_use",`, `"tool":"directory_list",`, `"input":{"path":"."}}`} {
		if err := stream.Push(delta, emit); err != nil {
			t.Fatalf("Push: %v", err)
		}
	}
	if len(emitted) != 0 || stream.Released() {
		t.Fatalf("tool instruction leaked as answer: %#v", emitted)
	}
}

func TestInstructionTextStreamSuppressesToolJSONAfterProviderPreface(t *testing.T) {
	stream := &instructionTextStream{}
	emitted := []string{}
	emit := func(delta string) error {
		emitted = append(emitted, delta)
		return nil
	}
	for _, delta := range []string{
		"我會先更新檔案，再驗證結果。",
		`{"ty`,
		`pe":"tool_use","tool":"shell_exec",`,
		`"input":{"command":"printf ok"}}`,
	} {
		if err := stream.Push(delta, emit); err != nil {
			t.Fatalf("Push: %v", err)
		}
	}
	visible := strings.Join(emitted, "")
	if visible != "我會先更新檔案，再驗證結果。" || strings.Contains(visible, "tool_use") {
		t.Fatalf("embedded tool instruction leaked as answer: %q", visible)
	}
}

func TestInstructionTextStreamReleasesOrdinaryJSON(t *testing.T) {
	stream := &instructionTextStream{}
	emitted := []string{}
	emit := func(delta string) error {
		emitted = append(emitted, delta)
		return nil
	}
	for _, delta := range []string{`結果：{`, `"status":"ok"}`} {
		if err := stream.Push(delta, emit); err != nil {
			t.Fatalf("Push: %v", err)
		}
	}
	if err := stream.Finish(`結果：{"status":"ok"}`, emit); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if visible := strings.Join(emitted, ""); visible != `結果：{"status":"ok"}` {
		t.Fatalf("ordinary JSON was not streamed intact: %q", visible)
	}
}

func TestRunInstructionModeEmitsProviderTextDeltas(t *testing.T) {
	session := testSession()
	sessions := newMemorySessions(session)
	model := &instructionStreamingModel{
		deltas:    [][]string{{"你", "好", "，", "這是串流回答。"}},
		responses: []domain.ModelResponse{{Content: "你好，這是串流回答。", StopReason: "stop"}},
	}
	runner := &Runner{
		Model:        model,
		Tools:        &fakeTools{},
		Sessions:     sessions,
		Context:      &ContextManager{Model: model, Sessions: sessions},
		Budget:       domain.RunBudget{MaxTurns: 4},
		SystemPrompt: "system",
		ToolCallMode: ToolCallModeInstruction,
	}
	deltas := []string{}

	result, err := runner.Run(context.Background(), Input{Session: session, UserInput: "打招呼"}, func(event domain.EngineEvent) error {
		if event.Type == "message.delta" {
			if delta, ok := event.Payload["delta"].(string); ok {
				deltas = append(deltas, delta)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Message.Content != "你好，這是串流回答。" {
		t.Fatalf("result = %q", result.Message.Content)
	}
	if strings.Join(deltas, "") != result.Message.Content || len(deltas) != len(model.deltas[0]) {
		t.Fatalf("message deltas = %#v, want provider chunks %#v", deltas, model.deltas[0])
	}
}

func TestRunInstructionModeDoesNotExposeStreamedToolJSON(t *testing.T) {
	session := testSession()
	sessions := newMemorySessions(session)
	toolJSON := `{"type":"tool_use","tool":"directory_list","input":{"path":"."}}`
	model := &instructionStreamingModel{
		deltas: [][]string{
			{"{", `"type":"tool_use",`, `"tool":"directory_list",`, `"input":{"path":"."}}`},
			{"已", "完成目錄分析。"},
		},
		responses: []domain.ModelResponse{
			{Content: toolJSON, StopReason: "stop"},
			{Content: "已完成目錄分析。", StopReason: "stop"},
		},
	}
	executed := 0
	tools := &fakeTools{
		definitions: instructionTestDefinitions(),
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			executed++
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: `{"entries":["README.md"]}`}, nil
		},
	}
	runner := &Runner{
		Model:        model,
		Tools:        tools,
		Sessions:     sessions,
		Context:      &ContextManager{Model: model, Sessions: sessions},
		Budget:       domain.RunBudget{MaxTurns: 4},
		SystemPrompt: "system",
		ToolCallMode: ToolCallModeInstruction,
	}
	deltas := []string{}

	result, err := runner.Run(context.Background(), Input{Session: session, UserInput: "分析目錄"}, func(event domain.EngineEvent) error {
		if event.Type == "message.delta" {
			if delta, ok := event.Payload["delta"].(string); ok {
				deltas = append(deltas, delta)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if executed != 1 || result.Message.Content != "已完成目錄分析。" {
		t.Fatalf("executed = %d, result = %+v", executed, result.Message)
	}
	visible := strings.Join(deltas, "")
	if visible != result.Message.Content || strings.Contains(visible, "tool_use") {
		t.Fatalf("visible deltas leaked tool instruction: %q", visible)
	}
}
