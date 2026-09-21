package httpapi_test

import (
	"AgenticService/src/adapters/openaicompat"
	"AgenticService/src/application"
	"AgenticService/src/bootstrap"
	"AgenticService/src/domain"
	"AgenticService/src/modelrouter"
	"AgenticService/src/ports"
	"AgenticService/src/providerauth"
	"AgenticService/src/transport/httpapi"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 使用獨立 Runtime 與合成 Token，不啟動真實 Provider、背景查詢或使用者設定。
func TestProviderAccountImportSmokeHTTP(t *testing.T) {
	m, _ := providerauth.New(filepath.Join(t.TempDir(), "tokens.json"), nil)
	r := &bootstrap.Runtime{ProviderAuth: m, Config: bootstrap.Config{Providers: map[string]bootstrap.ProviderConfig{
		"codex": {Type: "openai-codex-responses", OpenAICodexResponses: &openaicompat.Config{}},
		"chat":  {Type: "openai-compatible"},
	}}}
	r.Model, _ = modelrouter.New("codex", map[string]modelrouter.Provider{
		"codex": {Model: accountSmokeModel{}}, "chat": {Model: accountSmokeModel{}},
	})
	h, err := httpapi.New(&application.Service{}, httpapi.Config{APIToken: "smoke", ProviderOAuthFileAccounts: r.ProviderOAuthFileAccounts, ImportProviderOAuthFileAccount: r.ImportProviderOAuthFileAccount})
	if err != nil {
		t.Fatal(err)
	}
	entries := []any{}
	for _, id := range []string{"one", "two"} {
		claims, _ := json.Marshal(map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "https://api.openai.com/auth": map[string]string{"chatgpt_account_id": id}})
		entries = append(entries, map[string]any{"email": id + "@example.invalid", "access_token": "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".synthetic"})
	}
	data, _ := json.Marshal(entries)
	invoke := func(path, body, auth, contentType string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	path := "/api/v1/admin/provider-settings/codex/oauth/accounts"
	if w := invoke(path, string(data), "", "application/json"); w.Code != http.StatusUnauthorized {
		t.Fatal("帳號端點應受 Bearer 保護")
	}
	w := invoke(path, string(data), "smoke", "application/json")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("列表端點失敗：%d", w.Code)
	}
	if strings.Contains(w.Body.String(), "synthetic") || strings.Contains(w.Body.String(), "access_token") {
		t.Fatal("HTTP 列表不得洩漏憑證")
	}
	var list struct {
		Data []domain.ProviderOAuthFileAccount `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Data) != 2 {
		t.Fatal("列表回應錯誤")
	}
	w = invoke(path+"/"+list.Data[1].ID, string(data), "smoke", "application/json")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "two@example.invalid") || strings.Contains(w.Body.String(), "synthetic") {
		t.Fatalf("第二筆匯入失敗：%d", w.Code)
	}
	current, _ := r.Model.ProviderUsage("codex")
	other, _ := r.Model.ProviderUsage("chat")
	if current.UpdatedAt != "" || other.UpdatedAt != "previous-account" {
		t.Fatal("應只清除切換帳號的快取")
	}
	for _, check := range []struct {
		path, body, contentType string
		status                  int
	}{
		{path, string(data), "text/plain", 400},
		{path, "not-json synthetic-secret", "application/json", 400},
		{path + "/" + list.Data[0].ID, string(data) + " ", "application/json", 400},
		{strings.Replace(path, "/codex/", "/missing/", 1), string(data), "application/json", 404},
		{strings.Replace(path, "/codex/", "/chat/", 1), string(data), "application/json", 400},
		{path, strings.Repeat("x", 8*1024*1024+1), "application/json", 400},
	} {
		w := invoke(check.path, check.body, "smoke", check.contentType)
		if w.Code != check.status || strings.Contains(w.Body.String(), "synthetic-secret") {
			t.Fatalf("失敗路徑檢查錯誤：%d", w.Code)
		}
	}
}

type accountSmokeModel struct{}

func (accountSmokeModel) Stream(context.Context, domain.ModelRequest, ports.ModelEventSink) (domain.ModelResponse, error) {
	return domain.ModelResponse{}, nil
}

func (accountSmokeModel) ProviderUsage() domain.ProviderUsage {
	return domain.ProviderUsage{UpdatedAt: "previous-account"}
}
