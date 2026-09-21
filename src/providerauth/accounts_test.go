package providerauth

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func smokeAccount(t *testing.T, id string, nested bool, expires time.Time) accountFileEntry {
	t.Helper()
	claims, _ := json.Marshal(map[string]any{"exp": expires.Unix(), "email": id + "@example.invalid", "client_id": CodexClientID, chatGPTAuthClaim: map[string]any{"chatgpt_account_id": id}})
	entry := accountFileEntry{AccessToken: "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".synthetic", RefreshToken: "synthetic-refresh-" + id, AccountID: id, Email: id + "@example.invalid"}
	if nested {
		data, _ := json.Marshal(map[string]any{"codex_auth": map[string]any{"tokens": map[string]string{"access_token": entry.AccessToken, "refresh_token": entry.RefreshToken, "account_id": id}}, "email": entry.Email})
		if err := json.Unmarshal(data, &entry); err != nil {
			t.Fatal(err)
		}
		entry.AccessToken = "different-top-level-token"
		entry.RefreshToken = "different-top-level-refresh"
	}
	return entry
}

func TestAccountFileSmokeSelectionAndRedaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	m, _ := New(path, nil)
	entries := []accountFileEntry{smokeAccount(t, "first", false, time.Now().Add(time.Hour)), smokeAccount(t, "second", true, time.Now().Add(time.Hour))}
	data, _ := json.Marshal(entries)
	accounts, err := m.FileAccounts(data)
	if err != nil || len(accounts) != 2 {
		t.Fatalf("列表解析失敗：%v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("列出或取消時不應寫入 Token")
	}
	public, _ := json.Marshal(accounts)
	if strings.Contains(string(public), "synthetic") || strings.Contains(string(public), "access_token") || strings.Contains(string(public), "refresh_token") {
		t.Fatal("列表洩漏憑證")
	}
	if err := m.putRecord(tokenRecord{ProviderID: "other", AccessToken: "keep-other"}); err != nil {
		t.Fatal(err)
	}
	status, err := m.ImportFileAccount("target", data, accounts[1].ID)
	if err != nil || status.AccountEmail != "second@example.invalid" {
		t.Fatalf("未匯入選取的第二筆：%v", err)
	}
	record, _ := m.getRecord("target")
	if record.AccountID != "second" || record.RefreshToken != entries[1].CodexAuth.Tokens.RefreshToken {
		t.Fatal("選取的帳號與儲存內容不符")
	}
	other, _ := m.getRecord("other")
	if other.AccessToken != "keep-other" {
		t.Fatal("不應變更其他 Provider")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("Token 檔權限不是 0600")
	}
	changed := append(append([]byte{}, data...), ' ')
	if _, err := m.ImportFileAccount("target", changed, accounts[0].ID); err == nil {
		t.Fatal("檔案變更應拒絕舊選取 ID")
	}
	if _, err := m.ImportFileAccount("target", data, strings.Repeat("0", 64)); err == nil {
		t.Fatal("應拒絕未知 ID")
	}
	record, _ = m.getRecord("target")
	if record.AccountID != "second" {
		t.Fatal("失敗時不應覆蓋現有帳號")
	}
}

func TestAccountFileSmokeInvalidInputsAndPendingOAuth(t *testing.T) {
	m, _ := New(filepath.Join(t.TempDir(), "tokens.json"), nil)
	for _, data := range [][]byte{[]byte(`{"access_token":"synthetic-secret"}`), []byte(`[{"access_token":123}]`), []byte(`null`), []byte(`broken synthetic-secret`), make([]byte, maxAccountFileBytes+1)} {
		_, err := m.FileAccounts(data)
		if err == nil || strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatal("無效檔案應安全回報錯誤")
		}
	}
	invalid := smokeAccount(t, "expired", false, time.Now().Add(-time.Hour))
	invalid.RefreshToken = ""
	valid := smokeAccount(t, "valid", false, time.Now().Add(time.Hour))
	data, _ := json.Marshal([]accountFileEntry{invalid, valid})
	accounts, err := m.FileAccounts(data)
	if err != nil || accounts[0].Problem == "" || accounts[1].Problem != "" {
		t.Fatal("應個別標記不可用帳號")
	}
	if _, err := m.ImportFileAccount("p", data, accounts[0].ID); err == nil {
		t.Fatal("應拒絕失效且無 refresh token 的帳號")
	}
	m.pending = &pendingSession{ProviderID: "p", Status: "pending"}
	if _, err := m.ImportFileAccount("p", data, accounts[1].ID); err == nil {
		t.Fatal("不能覆蓋等待中的 OAuth")
	}
	m.pending.Status = "failed"
	if _, err := m.ImportFileAccount("p", data, accounts[1].ID); err != nil {
		t.Fatalf("已失敗的 OAuth 不應永久阻止匯入：%v", err)
	}
}
