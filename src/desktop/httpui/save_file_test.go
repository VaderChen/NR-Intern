package httpui

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postSave(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	server := &Server{}
	recorder := httptest.NewRecorder()
	server.saveFile(recorder, httptest.NewRequest(http.MethodPost, "/desktop/api/files/save", strings.NewReader(body)))
	return recorder
}

// 檔名會直接交給原生存檔面板當預設名稱，而它來自使用者自訂的 Provider／MCP id。
// 帶路徑分隔符的名稱要在這裡就擋掉，不能讓它有機會指向別的目錄。
func TestSaveFileRejectsPathsInTheName(t *testing.T) {
	for _, name := range []string{"", "   ", "../escape.json", "dir/inner.json", `dir\inner.json`, "."} {
		body, _ := json.Marshal(map[string]any{"name": name, "content_base64": ""})
		if recorder := postSave(t, string(body)); recorder.Code != http.StatusBadRequest {
			t.Fatalf("檔名 %q 應被拒絕，得到 %d：%s", name, recorder.Code, recorder.Body.String())
		}
	}
}

func TestSaveFileRejectsInvalidBase64(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"name": "a.provider", "content_base64": "not base64!!"})
	recorder := postSave(t, string(body))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("無效的 base64 應回 400，得到 %d：%s", recorder.Code, recorder.Body.String())
	}
}

func TestSaveFileRejectsOversizedContent(t *testing.T) {
	oversized := base64.StdEncoding.EncodeToString(make([]byte, maxSaveFileBytes+1))
	body, _ := json.Marshal(map[string]any{"name": "a.provider", "content_base64": oversized})
	recorder := postSave(t, string(body))
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超過上限應回 413，得到 %d", recorder.Code)
	}
}

func TestSaveFileRejectsMalformedJSON(t *testing.T) {
	if recorder := postSave(t, "{ not json"); recorder.Code != http.StatusBadRequest {
		t.Fatalf("壞掉的 JSON 應回 400，得到 %d", recorder.Code)
	}
}
