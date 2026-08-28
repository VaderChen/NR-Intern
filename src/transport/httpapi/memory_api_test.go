package httpapi_test

import (
	"AgenticService/src/bootstrap"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func call(t *testing.T, runtime *bootstrap.Runtime, method, target, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	runtime.HTTPHandler.ServeHTTP(recorder, request)
	decoded := map[string]any{}
	if strings.Contains(recorder.Header().Get("Content-Type"), "json") {
		_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	}
	return recorder, decoded
}

func memoryData(t *testing.T, decoded map[string]any) map[string]any {
	t.Helper()
	value, ok := decoded["data"].(map[string]any)
	if !ok {
		t.Fatalf("response has no data object: %v", decoded)
	}
	return value
}

// TestMemoryAPILifecycle 覆蓋一個原本完全沒有 HTTP 出口的能力：
// 長期記憶只能經由 Agent 工具存取，使用者無法檢視或更正 Agent 記住的內容。
func TestMemoryAPILifecycle(t *testing.T) {
	runtime := testRuntime(t, "")

	created, decoded := call(t, runtime, http.MethodPost, "/api/v1/memories",
		`{"content":"使用者偏好繁體中文","kind":"preference","tags":["language"]}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	memory := memoryData(t, decoded)
	id, _ := memory["id"].(string)
	if id == "" {
		t.Fatal("created memory has no id")
	}
	if metadata, _ := memory["metadata"].(map[string]any); metadata["source"] != "operator" {
		t.Errorf("metadata.source = %v, want operator so agent-written memories stay distinguishable", metadata["source"])
	}

	listed, decoded := call(t, runtime, http.MethodGet, "/api/v1/memories?q=繁體", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("search status = %d", listed.Code)
	}
	if items, _ := decoded["data"].([]any); len(items) != 1 {
		t.Fatalf("search returned %d memories, want 1", len(items))
	}

	fetched, decoded := call(t, runtime, http.MethodGet, "/api/v1/memories/"+id, "")
	if fetched.Code != http.StatusOK {
		t.Fatalf("get status = %d", fetched.Code)
	}
	if memoryData(t, decoded)["status"] != "active" {
		t.Errorf("status = %v, want active", memoryData(t, decoded)["status"])
	}

	forgotten, decoded := call(t, runtime, http.MethodDelete, "/api/v1/memories/"+id+"?reason=test", "")
	if forgotten.Code != http.StatusOK {
		t.Fatalf("forget status = %d", forgotten.Code)
	}
	// 軟性遺忘：資料仍在（保留稽核），但不再被召回。
	value := memoryData(t, decoded)
	if value["status"] != "forgotten" || value["forget_reason"] != "test" {
		t.Fatalf("forgotten memory = %v, want status=forgotten with the reason recorded", value)
	}
	_, decoded = call(t, runtime, http.MethodGet, "/api/v1/memories?q=繁體", "")
	if items, _ := decoded["data"].([]any); len(items) != 0 {
		t.Fatalf("forgotten memory still appears in search: %v", items)
	}
}

func TestMemoryAPIRejectsUnknownKind(t *testing.T) {
	runtime := testRuntime(t, "")

	recorder, _ := call(t, runtime, http.MethodPost, "/api/v1/memories", `{"content":"x","kind":"nonsense"}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestMemoryAPIRequiresContent(t *testing.T) {
	runtime := testRuntime(t, "")

	recorder, _ := call(t, runtime, http.MethodPost, "/api/v1/memories", `{"content":"   "}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

// TestRunListRejectsUnknownStatus 保證過濾條件打錯時會明確報錯，
// 而不是靜默回傳全部 Run——後者會讓 UI 顯示錯誤的待審清單。
func TestRunListRejectsUnknownStatus(t *testing.T) {
	runtime := testRuntime(t, "")

	recorder, decoded := call(t, runtime, http.MethodGet, "/api/v1/runs?status=bogus", "")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if detail, _ := decoded["detail"].(string); !strings.Contains(detail, "bogus") {
		t.Errorf("detail = %q, want it to name the offending value", detail)
	}
}

func TestRunListAcceptsKnownStatus(t *testing.T) {
	runtime := testRuntime(t, "")

	recorder, _ := call(t, runtime, http.MethodGet, "/api/v1/runs?status=waiting_approval,running", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestSessionExportRendersJSONAndMarkdown(t *testing.T) {
	runtime := testRuntime(t, "")
	_, decoded := call(t, runtime, http.MethodGet, "/api/v1/workspaces", "")
	items, _ := decoded["data"].([]any)
	if len(items) == 0 {
		t.Fatal("no default workspace")
	}
	workspace, _ := items[0].(map[string]any)
	workspaceID, _ := workspace["id"].(string)

	_, decoded = call(t, runtime, http.MethodPost, "/api/v1/agents/general-agent/sessions",
		`{"workspace_id":"`+workspaceID+`","title":"匯出測試"}`)
	sessionID, _ := memoryData(t, decoded)["id"].(string)
	if sessionID == "" {
		t.Fatal("session was not created")
	}

	jsonExport, _ := call(t, runtime, http.MethodGet, "/api/v1/sessions/"+sessionID+"/export", "")
	if jsonExport.Code != http.StatusOK {
		t.Fatalf("json export status = %d", jsonExport.Code)
	}

	markdown, _ := call(t, runtime, http.MethodGet, "/api/v1/sessions/"+sessionID+"/export?format=markdown", "")
	if markdown.Code != http.StatusOK {
		t.Fatalf("markdown export status = %d", markdown.Code)
	}
	if !strings.Contains(markdown.Header().Get("Content-Type"), "text/markdown") {
		t.Errorf("content type = %q, want text/markdown", markdown.Header().Get("Content-Type"))
	}
	if !strings.Contains(markdown.Body.String(), "# 匯出測試") {
		t.Errorf("markdown body missing the session title: %s", markdown.Body.String())
	}

	bad, _ := call(t, runtime, http.MethodGet, "/api/v1/sessions/"+sessionID+"/export?format=pdf", "")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("unknown format status = %d, want 400", bad.Code)
	}
}
