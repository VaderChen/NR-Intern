package openaicompat

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 每次只交一個 byte，涵蓋任意網路封包邊界（包括 UTF-8 與 CRLF）。
type fragmentedReader struct{ io.Reader }

func (r fragmentedReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

func TestSmokeCodexRefusalIsTerminalWithoutToolDispatch(t *testing.T) {
	for _, streamed := range []bool{false, true} {
		t.Run(fmt.Sprint(streamed), func(t *testing.T) {
			var events []string
			if streamed {
				events = append(events,
					`{"type":"response.refusal.delta","delta":"拒絕"}`,
					`{"type":"response.refusal.done","refusal":"拒絕執行"}`,
					`{"type":"response.refusal.done","refusal":"拒絕執行"}`,
				)
			}
			// 即使同時收到不完整工具參數，也不能啟動工具或協定修復重試。
			events = append(events, `{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"refusal","refusal":"拒絕執行"}]},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"shell_exec","arguments":"{"}]}}`)
			stream := ""
			for _, event := range events {
				stream += "data: " + event + "\r\n\r\n"
			}
			var text strings.Builder
			result, err := decodeCodexStream(fragmentedReader{strings.NewReader(stream)}, "model", "", "", func(event domain.ModelEvent) error {
				if event.Type == domain.ModelEventTextDelta {
					text.WriteString(event.Delta)
				}
				return nil
			})
			if err != nil || result.StopReason != "refusal" || result.Content != "拒絕執行" || text.String() != result.Content || len(result.ToolCalls) != 0 {
				t.Fatalf("unexpected refusal: result=%+v, text=%q, err=%v", result, text.String(), err)
			}
		})
	}
}

func TestSmokeCodexEncryptedReasoningRoundTripAndScope(t *testing.T) {
	const reasoning = `{"type":"reasoning","id":"rs_fake","encrypted_content":"synthetic-opaque-state","summary":[]}`
	var received [][]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Input []json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode: %v", err)
		}
		received = append(received, payload.Input)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":%s}\n\n", reasoning)
		fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[%s,{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_1\",\"name\":\"file_read\",\"arguments\":\"{}\"}]}}\n\n", reasoning)
	}))
	defer server.Close()
	config := Config{UseResponses: true, BaseURL: server.URL + "/responses", Model: "test-model", MaxAttempts: 1}
	model, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	first, err := model.Stream(context.Background(), domain.ModelRequest{UserPrompt: "read"}, nil)
	if err != nil || first.Continuation == nil || len(first.Continuation.Items) != 1 {
		t.Fatalf("capture=%+v err=%v", first, err)
	}
	history := []domain.Message{{Role: "assistant", ToolCalls: first.ToolCalls, Continuation: first.Continuation}, {Role: "tool", ToolCallID: "call_1", Content: "ok"}}
	request := domain.ModelRequest{History: history}
	if _, err := model.Stream(context.Background(), request, nil); err != nil {
		t.Fatal(err)
	}
	other, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Stream(context.Background(), request, nil); err != nil {
		t.Fatal(err)
	}
	request.Model = "different-model"
	if _, err := model.Stream(context.Background(), request, nil); err != nil {
		t.Fatal(err)
	}
	for index, want := range []bool{false, true, false, false} {
		count := 0
		for _, item := range received[index] {
			if validCodexReasoning(item) {
				count++
			}
		}
		if (count == 1) != want || count > 1 {
			t.Fatalf("request %d reasoning count=%d want=%v", index, count, want)
		}
	}
	if history[0].Continuation != first.Continuation {
		t.Fatal("request scope filter modified history")
	}
	clone := first.Continuation.Clone()
	clone.Items[0][0] = ' '
	if first.Continuation.Items[0][0] != '{' || clone.Bytes() == 0 {
		t.Fatal("continuation clone aliases bytes")
	}
}
