package httpapi_test

import (
	"AgenticService/src/domain"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunStreamRepairsMissingTerminalWithoutChangingProtocolSmoke(t *testing.T) {
	for _, status := range []domain.RunStatus{domain.RunStatusCompleted, domain.RunStatusFailed, domain.RunStatusCanceled} {
		t.Run(string(status), func(t *testing.T) {
			runtime := testRuntime(t, "")
			run := domain.Run{ID: "run_stream_smoke", SessionID: "session_synthetic", AgentID: "agent_synthetic", Status: status}
			if err := runtime.Runs.Save(context.Background(), run); err != nil {
				t.Fatal(err)
			}
			if err := runtime.Events.Append(context.Background(), domain.Event{ID: "evt_synthetic", Type: "run.started", RunID: run.ID, SessionID: run.SessionID, Sequence: 1}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/runs/"+run.ID+"/events", nil).WithContext(ctx)
			request.Header.Set("Last-Event-ID", "1")
			response := httptest.NewRecorder()
			runtime.HTTPHandler.ServeHTTP(response, request)
			body := response.Body.String()
			if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/event-stream") || !strings.Contains(body, "event: run."+string(status)+"\n") || !strings.Contains(body, "id: 2\n") || strings.Contains(body, "event: run.started") {
				t.Fatalf("串流格式或增量重播錯誤：%d %s", response.Code, body)
			}
			request = httptest.NewRequest(http.MethodGet, "/api/v1/runs/"+run.ID+"/events", nil).WithContext(ctx)
			request.Header.Set("Last-Event-ID", "2")
			response = httptest.NewRecorder()
			runtime.HTTPHandler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.Len() != 0 {
				t.Fatalf("重連重複送出終態：%d %s", response.Code, response.Body.String())
			}
			events, err := runtime.Events.List(ctx, run.ID, 0)
			if err != nil || len(events) != 2 {
				t.Fatalf("補寫重複：%v %v", events, err)
			}
		})
	}
}
