package bootstrap

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"AgenticService/src/domain"
)

// configBundleFiles 是「設定包」收錄的檔案：Provider、MCP、反向代理與服務設定。
// 刻意不收 sessions、workspaces、projects、runs 等資料——設定包是拿去換一台機器
// 重建環境用的，不是備份對話內容。
var configBundleFiles = []string{
	providerSettingsFilename,
	mcpSettingsFilename,
	"netpass.json",
	serviceSettingsFilename,
}

// secretFieldHints 是要遮蔽的欄位名稱片段。用片段比對而不是逐一列舉完整路徑：
// 設定結構之後會長出新欄位，漏掉一個就是把金鑰明文寫進使用者的下載資料夾。
var secretFieldHints = []string{
	"api_key", "apikey", "token", "secret", "password", "passphrase",
	"authorization", "credential", "private_key", "client_secret",
}

// secretContainerFields 的內容整包都是機密（值全部遮蔽，鍵保留），
// 讓使用者知道還原時要補哪些變數。
var secretContainerFields = []string{"headers", "environment", "env"}

type configBundleManifest struct {
	Format    string   `json:"format"`
	Version   int      `json:"version"`
	CreatedAt string   `json:"created_at"`
	Included  []string `json:"included"`
	Excluded  []string `json:"excluded"`
	Redacted  []string `json:"redacted"`
	Note      string   `json:"note"`
}

// ConfigBundle 匯出 Provider、MCP 與服務設定，金鑰一律遮蔽。
//
// 這個產品的每一處都把憑證當成不離開後端的東西：管理 API 只回傳「有沒有設定」
// 的布林值，安全備份也刻意排除這幾個檔案。一鍵下載明文金鑰會推翻上述所有決定，
// 而下載檔會留在下載資料夾、被同步、被轉寄。因此設定包只帶結構，金鑰另外補。
func (r *Runtime) ConfigBundle(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.configMu.RLock()
	dataDir := r.Config.DataDir
	r.configMu.RUnlock()
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("%w: data directory is unavailable", domain.ErrConflict)
	}

	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	manifest := configBundleManifest{
		Format:    "nr-intern-config-bundle",
		Version:   1,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Excluded:  []string{"workspaces", "projects", "sessions", "runs", "events", "plans", "attachments", "memories", "notifications"},
		Note:      "只含 Provider、MCP、反向代理與服務設定。金鑰、Token、密碼與 Header／環境變數的值一律遮蔽為空字串，還原後需要重新輸入。",
	}
	redacted := map[string]bool{}
	for _, name := range configBundleFiles {
		path := filepath.Join(dataDir, name)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			_ = archive.Close()
			return nil, fmt.Errorf("inspect %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			_ = archive.Close()
			return nil, fmt.Errorf("%s is not a regular file", name)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			_ = archive.Close()
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		cleaned, fields, err := redactSecrets(content)
		if err != nil {
			_ = archive.Close()
			return nil, fmt.Errorf("redact %s: %w", name, err)
		}
		for _, field := range fields {
			redacted[name+":"+field] = true
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o600)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			_ = archive.Close()
			return nil, err
		}
		if _, err := writer.Write(cleaned); err != nil {
			_ = archive.Close()
			return nil, err
		}
		manifest.Included = append(manifest.Included, name)
	}
	for field := range redacted {
		manifest.Redacted = append(manifest.Redacted, field)
	}
	sort.Strings(manifest.Redacted)

	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		_ = archive.Close()
		return nil, err
	}
	writer, err := archive.Create("manifest.json")
	if err != nil {
		_ = archive.Close()
		return nil, err
	}
	if _, err := writer.Write(encoded); err != nil {
		_ = archive.Close()
		return nil, err
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// redactSecrets 走過整份 JSON，把機密欄位換成空字串，並回傳被遮蔽的欄位名稱。
// 保留鍵、只清值：使用者才知道原本設定過哪些項目要補回來。
func redactSecrets(content []byte) ([]byte, []string, error) {
	var value any
	if err := json.Unmarshal(content, &value); err != nil {
		return nil, nil, err
	}
	found := map[string]bool{}
	cleaned := redactValue(value, found)
	encoded, err := json.MarshalIndent(cleaned, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	fields := make([]string, 0, len(found))
	for field := range found {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return append(encoded, '\n'), fields, nil
}

func redactValue(value any, found map[string]bool) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			lower := strings.ToLower(key)
			if isSecretField(lower) && isSecretValue(item) {
				if text, ok := item.(string); !ok || strings.TrimSpace(text) != "" {
					found[key] = true
				}
				result[key] = redactedPlaceholder(item)
				continue
			}
			if isSecretContainer(lower) {
				if nested, ok := item.(map[string]any); ok {
					blanked := make(map[string]any, len(nested))
					for nestedKey := range nested {
						blanked[nestedKey] = ""
					}
					if len(nested) > 0 {
						found[key] = true
					}
					result[key] = blanked
					continue
				}
			}
			result[key] = redactValue(item, found)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = redactValue(item, found)
		}
		return result
	default:
		return value
	}
}

// redactedPlaceholder 保留原本的型別。
func redactedPlaceholder(value any) any {
	if _, ok := value.([]any); ok {
		return []any{}
	}
	return ""
}

// isSecretValue 只把字串與字串陣列視為可能的機密。
//
// 名稱比對是片段比對，會誤傷同樣含有這些字的設定：max_tokens 帶著「token」，
// 但它是數字上限，不是憑證。實測第一版就把使用者的 Token 上限清成空字串了。
// 憑證一定是字串，數字與布林值一律原樣保留。
func isSecretValue(value any) bool {
	switch typed := value.(type) {
	case string:
		return true
	case []any:
		for _, item := range typed {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return len(typed) > 0
	default:
		return false
	}
}

func isSecretField(lowerKey string) bool {
	for _, hint := range secretFieldHints {
		if strings.Contains(lowerKey, hint) {
			return true
		}
	}
	return false
}

func isSecretContainer(lowerKey string) bool {
	for _, field := range secretContainerFields {
		if lowerKey == field {
			return true
		}
	}
	return false
}
