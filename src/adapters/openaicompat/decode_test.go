package openaicompat

import (
	"AgenticService/src/domain"
	"strings"
	"testing"
)

func collect(t *testing.T, payload string) (domain.ModelResponse, []domain.ModelEvent) {
	t.Helper()
	events := []domain.ModelEvent{}
	response, err := decodeStream(strings.NewReader(payload), "fallback-model", "req_1", "client_1", func(event domain.ModelEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("decodeStream: %v", err)
	}
	return response, events
}

func eventsOfType(events []domain.ModelEvent, wanted string) []domain.ModelEvent {
	result := []domain.ModelEvent{}
	for _, event := range events {
		if event.Type == wanted {
			result = append(result, event)
		}
	}
	return result
}

func TestDecodeStreamAccumulatesText(t *testing.T) {
	payload := `data: {"model":"m","choices":[{"index":0,"delta":{"content":"你好"}}]}

data: {"choices":[{"index":0,"delta":{"content":"世界"},"finish_reason":"stop"}]}

data: [DONE]

`
	response, events := collect(t, payload)

	if response.Content != "你好世界" {
		t.Errorf("content = %q, want 你好世界", response.Content)
	}
	if got := len(eventsOfType(events, domain.ModelEventTextDelta)); got != 2 {
		t.Errorf("text delta count = %d, want 2", got)
	}
	if response.Model != "m" {
		t.Errorf("model = %q, want m", response.Model)
	}
}

// TestDecodeStreamEmitsToolCallDeltas 覆蓋原本無法觀察的路徑：
// 工具參數過去只在 adapter 內部累積，前端看不到工具呼叫的形成過程。
func TestDecodeStreamEmitsToolCallDeltas(t *testing.T) {
	payload := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"file_read","arguments":"{\"path\":"}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.txt\"}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]

`
	response, events := collect(t, payload)

	deltas := eventsOfType(events, domain.ModelEventToolCallDelta)
	if len(deltas) != 2 {
		t.Fatalf("tool call delta count = %d, want 2", len(deltas))
	}
	if deltas[0].ToolCall == nil || deltas[0].ToolCall.ID != "call_1" || deltas[0].ToolCall.Name != "file_read" {
		t.Errorf("first delta = %+v, want the tool identity", deltas[0].ToolCall)
	}
	if deltas[1].ToolCall == nil || deltas[1].ToolCall.ID != "call_1" {
		t.Error("later fragments must carry the tool call identity accumulated so far")
	}

	if len(response.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(response.ToolCalls))
	}
	if response.ToolCalls[0].Arguments["path"] != "a.txt" {
		t.Errorf("arguments = %+v, want path=a.txt", response.ToolCalls[0].Arguments)
	}
	if response.StopReason != "tool_calls" {
		t.Errorf("stop reason = %q, want tool_calls", response.StopReason)
	}
}

func TestDecodeStreamEmitsThinkingDeltas(t *testing.T) {
	payload := `data: {"choices":[{"index":0,"delta":{"reasoning_content":"先確認檔案是否存在"}}]}

data: {"choices":[{"index":0,"delta":{"reasoning":" 再決定要不要讀"}}]}

data: {"choices":[{"index":0,"delta":{"content":"好"},"finish_reason":"stop"}]}

data: [DONE]

`
	response, events := collect(t, payload)

	thinking := eventsOfType(events, domain.ModelEventThinkingDelta)
	if len(thinking) != 2 {
		t.Fatalf("thinking delta count = %d, want 2 (reasoning_content and reasoning are both supported)", len(thinking))
	}
	if response.Reasoning != "先確認檔案是否存在 再決定要不要讀" {
		t.Fatalf("reasoning = %q, want accumulated reasoning", response.Reasoning)
	}
}

func TestDecodeJSONResponsePreservesReasoning(t *testing.T) {
	payload := `{"model":"m","choices":[{"message":{"content":"完成","reasoning_content":"先整理證據"},"finish_reason":"stop"}]}`
	events := []domain.ModelEvent{}
	response, err := decodeJSONResponse(strings.NewReader(payload), "fallback", "req_1", "client_1", func(event domain.ModelEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("decodeJSONResponse: %v", err)
	}
	if response.Reasoning != "先整理證據" {
		t.Fatalf("reasoning = %q", response.Reasoning)
	}
	if len(eventsOfType(events, domain.ModelEventThinkingDelta)) != 1 {
		t.Fatal("reasoning event was not emitted")
	}
}

func TestDecodeStreamEmitsUsage(t *testing.T) {
	payload := `data: {"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":8,"total_tokens":128}}

data: [DONE]

`
	response, events := collect(t, payload)

	usage := eventsOfType(events, domain.ModelEventUsage)
	if len(usage) != 1 {
		t.Fatalf("usage event count = %d, want 1", len(usage))
	}
	if usage[0].Usage == nil || usage[0].Usage.InputTokens != 120 {
		t.Errorf("usage = %+v, want input tokens 120", usage[0].Usage)
	}
	if response.Usage.TotalTokens != 128 {
		t.Errorf("response usage = %+v, want total 128", response.Usage)
	}
}

// TestDecodeStreamAcceptsNDJSON 覆蓋不吐完整 SSE 欄位的相容服務。
func TestDecodeStreamAcceptsNDJSON(t *testing.T) {
	payload := `{"choices":[{"index":0,"delta":{"content":"a"}}]}
{"choices":[{"index":0,"delta":{"content":"b"},"finish_reason":"stop"}]}
`
	response, _ := collect(t, payload)

	if response.Content != "ab" {
		t.Errorf("content = %q, want ab", response.Content)
	}
}

func TestDecodeStreamRejectsTruncatedStream(t *testing.T) {
	payload := `data: {"choices":[{"index":0,"delta":{"content":"a"}}]}

`
	_, err := decodeStream(strings.NewReader(payload), "m", "req_1", "client_1", nil)
	if err == nil {
		t.Fatal("expected an error for a stream that ended before finish_reason or [DONE]")
	}
	var providerError *ProviderError
	if !asProviderError(err, &providerError) || !providerError.Retryable {
		t.Fatalf("err = %v, want a retryable provider error", err)
	}
}

func TestDecodeStreamSurfacesProviderError(t *testing.T) {
	payload := `data: {"error":{"message":"rate limited","type":"server_error","code":"429"}}

`
	_, err := decodeStream(strings.NewReader(payload), "m", "req_1", "client_1", nil)
	if err == nil {
		t.Fatal("expected the in-stream provider error to surface")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("err = %v, want the provider message", err)
	}
}

func asProviderError(err error, target **ProviderError) bool {
	value, ok := err.(*ProviderError)
	if ok {
		*target = value
	}
	return ok
}
