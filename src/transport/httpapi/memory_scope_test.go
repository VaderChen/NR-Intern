package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"
)

// 回憶空間開啟後記憶落在 project:<id>，使用者猜不到那串 ID。
// scope=all 是管理介面唯一能「不先知道 scope 就看到記了什麼」的入口。
func TestListMemoriesAcrossScopes(t *testing.T) {
	runtime := testRuntime(t, "")
	for _, seed := range []struct{ scope, content string }{
		{"project:alpha", "回覆一律使用繁體中文"},
		{"project:beta", "部署前先跑演練"},
		{"workspace:main", "報表輸出使用 UTF-8"},
	} {
		created, _ := call(t, runtime, http.MethodPost, "/api/v1/memories",
			fmt.Sprintf(`{"scope":%q,"content":%q,"kind":"preference"}`, seed.scope, seed.content))
		if created.Code != http.StatusCreated {
			t.Fatalf("seed %s: status %d body %s", seed.scope, created.Code, created.Body.String())
		}
	}
	created, decoded := call(t, runtime, http.MethodPost, "/api/v1/memories",
		`{"scope":"project:alpha","content":"已經作廢的決策","kind":"decision"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("seed forgotten: status %d", created.Code)
	}
	id, _ := memoryData(t, decoded)["id"].(string)
	if forgotten, _ := call(t, runtime, http.MethodDelete,
		"/api/v1/memories/"+id+"?scope=project:alpha&reason=test", ""); forgotten.Code != http.StatusOK {
		t.Fatalf("forget status = %d", forgotten.Code)
	}

	// 指定不到 scope 就等於看不到：這是加上 scope=all 之前的實際行為。
	_, decoded = call(t, runtime, http.MethodGet, "/api/v1/memories", "")
	if items, _ := decoded["data"].([]any); len(items) != 0 {
		t.Fatalf("the default scope should hold nothing here, got %d", len(items))
	}

	listed, decoded := call(t, runtime, http.MethodGet, "/api/v1/memories?scope=all&limit=50", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("cross-scope status = %d body = %s", listed.Code, listed.Body.String())
	}
	items, _ := decoded["data"].([]any)
	if len(items) != 3 {
		t.Fatalf("listed %d memories, want the 3 active ones across every scope", len(items))
	}
	scopes := map[string]bool{}
	for _, item := range items {
		value, _ := item.(map[string]any)
		scopes[fmt.Sprint(value["scope"])] = true
		if value["status"] != "active" {
			t.Fatalf("a forgotten memory must not be listed: %v", value["content"])
		}
	}
	for _, scope := range []string{"project:alpha", "project:beta", "workspace:main"} {
		if !scopes[scope] {
			t.Fatalf("scope %s is missing from the cross-scope listing", scope)
		}
	}
}
