package httpapi_test

import (
	"AgenticService/src/bootstrap"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testRuntime(t *testing.T, token string) *bootstrap.Runtime {
	t.Helper()
	config := bootstrap.DefaultConfig()
	config.DataDir = t.TempDir()
	config.APIToken = token
	runtime, err := bootstrap.Build(config)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Application.Close(context.Background()); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return runtime
}

func TestHTTPAuthenticationExemptsHealthButProtectsAPI(t *testing.T) {
	runtime := testRuntime(t, "test-token")

	health := httptest.NewRecorder()
	runtime.HTTPHandler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d", health.Code)
	}

	unauthorized := httptest.NewRecorder()
	runtime.HTTPHandler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil))
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Header().Get("Content-Type"), "application/problem+json") {
		t.Fatalf("unauthorized response = %d %s", unauthorized.Code, unauthorized.Header().Get("Content-Type"))
	}

	authorizedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	authorizedRequest.Header.Set("Authorization", "Bearer test-token")
	authorized := httptest.NewRecorder()
	runtime.HTTPHandler.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, body = %s", authorized.Code, authorized.Body.String())
	}
}

func TestHTTPRejectsUnknownJSONFieldsWithProblemResponse(t *testing.T) {
	runtime := testRuntime(t, "")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(`{
		"name":"test",
		"provider_ids":["openai-compatible"],
		"default_provider_id":"openai-compatible",
		"unexpected":true
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	runtime.HTTPHandler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if body.Code != "invalid_input" {
		t.Fatalf("problem code = %q", body.Code)
	}
}

func TestP1AdminEndpointsExposePersistentCapabilities(t *testing.T) {
	runtime := testRuntime(t, "")

	status, decoded := call(t, runtime, http.MethodGet, "/api/v1/admin/status", "")
	if status.Code != http.StatusOK {
		t.Fatalf("status endpoint = %d, body = %s", status.Code, status.Body.String())
	}
	statusData := memoryData(t, decoded)
	if statusData["api_version"] != "1.0" || statusData["event_schema_version"] != "1.0" {
		t.Fatalf("compatibility fields = %v", statusData)
	}
	capabilities, _ := statusData["capabilities"].([]any)
	seen := map[string]bool{}
	for _, value := range capabilities {
		if name, ok := value.(string); ok {
			seen[name] = true
		}
	}
	for _, required := range []string{"durable-outbox.v1", "run-recovery.v1", "run-retry.v1", "search.v1", "admin-backup.v1", "admin-permissions.v1", "update-check.v1"} {
		if !seen[required] {
			t.Errorf("missing capability %q in %v", required, capabilities)
		}
	}

	for _, endpoint := range []string{
		"/api/v1/admin/permissions",
		"/api/v1/admin/update",
		"/api/v1/notifications",
		"/api/v1/search?q=workspace",
	} {
		response, _ := call(t, runtime, http.MethodGet, endpoint, "")
		if response.Code != http.StatusOK {
			t.Errorf("GET %s = %d, body = %s", endpoint, response.Code, response.Body.String())
		}
	}

	backup, _ := call(t, runtime, http.MethodGet, "/api/v1/admin/backup", "")
	if backup.Code != http.StatusOK || !strings.Contains(backup.Header().Get("Content-Type"), "application/zip") {
		t.Errorf("backup response = %d %q", backup.Code, backup.Header().Get("Content-Type"))
	}
}
