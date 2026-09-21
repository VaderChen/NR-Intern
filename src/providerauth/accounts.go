package providerauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const maxAccountFileBytes = 8 * 1024 * 1024

// FileAccount 只含供使用者選擇的資料，絕不包含登入憑證。
type FileAccount struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Problem   string `json:"problem,omitempty"`
}

type accountFileEntry struct {
	Email        string `json:"email"`
	Name         string `json:"name"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	AccountID    string `json:"account_id"`
	ExpiresAt    string `json:"expires_at"`
	CodexAuth    *struct {
		Tokens struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			IDToken      string `json:"id_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
	} `json:"codex_auth"`
}

// 只解析使用者明確選取並上傳的內容，不讀取預設路徑或其他應用程式設定。
func readAccountFile(data []byte) ([]FileAccount, []tokenRecord, error) {
	if len(data) > maxAccountFileBytes {
		return nil, nil, fmt.Errorf("帳號檔超過 8 MiB 上限")
	}
	var entries []accountFileEntry
	if err := json.Unmarshal(data, &entries); err != nil || entries == nil {
		return nil, nil, fmt.Errorf("帳號檔必須是 JSON 多帳號陣列")
	}
	if len(entries) > 1000 {
		return nil, nil, fmt.Errorf("帳號檔超過 1000 筆上限")
	}
	fileDigest := sha256.Sum256(data)
	accounts := make([]FileAccount, 0, len(entries))
	records := make([]tokenRecord, 0, len(entries))
	for i, entry := range entries {
		record, err := entry.record()
		// 綁定完整檔案快照與位置；排序、權杖或其他資料改變都必須重新選擇。
		digest := sha256.Sum256([]byte(fmt.Sprintf("%x:%d", fileDigest, i)))
		account := FileAccount{ID: hex.EncodeToString(digest[:]), Email: firstNonEmpty(record.AccountEmail, entry.Email), Name: firstNonEmpty(record.AccountName, entry.Name), ExpiresAt: record.ExpiresAt}
		if err != nil {
			account.Problem = err.Error()
		}
		accounts = append(accounts, account)
		records = append(records, record)
	}
	return accounts, records, nil
}

func (entry accountFileEntry) record() (tokenRecord, error) {
	access, refresh, idToken, accountID := entry.AccessToken, entry.RefreshToken, entry.IDToken, entry.AccountID
	if entry.CodexAuth != nil {
		// 同一組 Token 必須來自同一來源，不能混搭另一層的 refresh token。
		tokens := entry.CodexAuth.Tokens
		access, refresh, idToken, accountID = tokens.AccessToken, tokens.RefreshToken, tokens.IDToken, tokens.AccountID
	}
	access, refresh = strings.TrimSpace(access), strings.TrimSpace(refresh)
	claimID := codexAccountID(access)
	if access == "" || claimID == "" {
		return tokenRecord{}, fmt.Errorf("缺少有效的 Codex 帳號資料")
	}
	if accountID != "" && strings.TrimSpace(accountID) != claimID {
		return tokenRecord{}, fmt.Errorf("帳號識別與 Token 不一致")
	}
	if client := stringClaim(jwtClaims(access), "client_id"); client != "" && client != CodexClientID {
		return tokenRecord{}, fmt.Errorf("此帳號的 OAuth Client 不相容，請改用瀏覽器登入")
	}
	expires := jwtExpiry(access)
	if expires.IsZero() {
		expires, _ = time.Parse(time.RFC3339, entry.ExpiresAt)
	}
	if expires.IsZero() {
		return tokenRecord{}, fmt.Errorf("缺少有效的 Token 到期時間")
	}
	if time.Until(expires) <= 90*time.Second && refresh == "" {
		return tokenRecord{}, fmt.Errorf("Token 已到期且無法更新，請重新登入")
	}
	info := userInfoFromTokens(tokenResponse{AccessToken: access, IDToken: idToken})
	return tokenRecord{ClientID: CodexClientID, AccountID: claimID, TokenType: "Bearer", AccessToken: access, RefreshToken: refresh, IDToken: idToken, Scope: DefaultScope, ExpiresAt: expires.UTC().Format(time.RFC3339), AccountEmail: firstNonEmpty(info.Email, entry.Email), AccountName: firstNonEmpty(info.Name, entry.Name), UpdatedAt: time.Now().UTC().Format(time.RFC3339)}, nil
}

func (m *Manager) FileAccounts(data []byte) ([]FileAccount, error) {
	accounts, _, err := readAccountFile(data)
	return accounts, err
}

func (m *Manager) ImportFileAccount(providerID string, data []byte, selectionID string) (Status, error) {
	if strings.TrimSpace(providerID) == "" || len(selectionID) != 64 {
		return Status{}, fmt.Errorf("請選擇要匯入的帳號")
	}
	accounts, records, err := readAccountFile(data)
	if err != nil {
		return Status{}, err
	}
	for i, account := range accounts {
		if account.ID != selectionID {
			continue
		}
		if account.Problem != "" {
			return Status{}, fmt.Errorf("%s", account.Problem)
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		// 不與互動 OAuth 回呼競爭同一筆憑證；保留現有流程讓使用者明確完成或取消。
		if (m.pending != nil && m.pending.Status == "pending") || m.listener != nil {
			return Status{}, fmt.Errorf("請先完成或中斷目前的 OAuth 登入，再匯入帳號")
		}
		record := records[i]
		record.ProviderID = strings.TrimSpace(providerID)
		if err := m.putRecord(record); err != nil {
			return Status{}, fmt.Errorf("無法安全儲存所選帳號")
		}
		if m.pending != nil && m.pending.ProviderID == providerID {
			m.pending = nil
		}
		return statusFromRecord(record), nil
	}
	return Status{}, fmt.Errorf("帳號檔已變更，請重新開啟列表選擇")
}
