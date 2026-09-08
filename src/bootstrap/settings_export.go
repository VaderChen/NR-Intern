package bootstrap

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 單一項目的匯出。
//
// 設定包是「換一台機器把整套搬過去」，這裡要解的是另一件事：把**一個**已經設好的
// Provider 或 MCP Server 交給別人。收檔案的人直接拖進去就能用，不必自己挑出要的那一個。
//
// 輸出格式刻意就是拖放匯入吃的格式，兩邊是同一份契約——匯出的檔案必須匯得回去。
// includeSecrets 的語意與設定包一致：預設遮蔽，帶明文必須是呼叫端明確要求的。

// ExportProviderSetting 匯出單一 Provider。
func (r *Runtime) ExportProviderSetting(ctx context.Context, providerID string, includeSecrets bool) ([]byte, error) {
	return r.exportSettingEntry(ctx, providerSettingsFilename, "providers", "providers", providerID, includeSecrets)
}

// ExportMCPSetting 匯出單一 MCP Server。
//
// 輸出用 mcpServers 這個鍵而不是儲存用的 servers：那是 MCP 生態共通的寫法，
// 匯出的檔案因此也餵得進其他工具。匯入端兩種都認得。
func (r *Runtime) ExportMCPSetting(ctx context.Context, serverID string, includeSecrets bool) ([]byte, error) {
	return r.exportSettingEntry(ctx, mcpSettingsFilename, "servers", "mcpServers", serverID, includeSecrets)
}

func (r *Runtime) exportSettingEntry(ctx context.Context, fileName, storedKey, exportKey, id string, includeSecrets bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", domain.ErrInvalidInput)
	}
	r.configMu.RLock()
	dataDir := r.Config.DataDir
	r.configMu.RUnlock()
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("%w: data directory is unavailable", domain.ErrConflict)
	}
	content, err := os.ReadFile(filepath.Join(dataDir, fileName))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s", domain.ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", fileName, err)
	}
	// 直接讀設定檔而不是走記憶體中的設定：管理 API 的那一份是脫敏過的，
	// 金鑰根本不在裡面，從那裡匯出永遠拿不到明文。
	var root map[string]any
	if err := json.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("decode %s: %w", fileName, err)
	}
	stored, _ := root[storedKey].(map[string]any)
	entry, found := stored[id].(map[string]any)
	if !found {
		return nil, fmt.Errorf("%w: %s", domain.ErrNotFound, id)
	}
	// 儲存時 id 是 map 的鍵，匯出要把它寫回物件裡：收檔案的人可能改鍵名，
	// 而匯入端以物件裡的 id 為準。
	exported := map[string]any{}
	for key, value := range entry {
		exported[key] = value
	}
	exported["id"] = id
	// 先遮蔽這一筆，再包上外層。反過來的話 contains_secrets 這個欄位名稱本身
	// 含有 secret，會不會被遮蔽就得依賴 redactSecrets 對布林值的處理細節——
	// 那種依賴以後只要有人調整規則就會安靜地壞掉。
	if !includeSecrets {
		encoded, err := json.Marshal(exported)
		if err != nil {
			return nil, err
		}
		cleaned, _, err := redactSecrets(encoded)
		if err != nil {
			return nil, fmt.Errorf("redact %s: %w", fileName, err)
		}
		exported = map[string]any{}
		if err := json.Unmarshal(cleaned, &exported); err != nil {
			return nil, err
		}
	}
	return json.MarshalIndent(map[string]any{
		exportKey:          map[string]any{id: exported},
		"contains_secrets": includeSecrets,
	}, "", "  ")
}
