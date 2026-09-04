package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

func NewID(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "id"
	}
	return prefix + "_" + randomIDSuffix()
}

// randomIDSuffix 產生 ID 的隨機部分；亂數不可用時退回時間戳。
func randomIDSuffix() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}

// ephemeralSessionMarker 標示這個 Session 屬於記憶體隔離 Project。
//
// 放在 session_ 之後、專案代碼之前，讓 ID 一眼看得出歸屬，也讓解析只需要
// 字串處理：Session 建立後不會換 Project（見 validateEphemeralSessionMove），
// 所以編進 ID 的歸屬永遠成立，不需要額外的映射檔或啟動掃描。
//
// 刻意選 v（volatile）而不是 e：一般 ID 的隨機部分是十六進位，開頭有十六分之一
// 的機率就是 e，光看前綴會分不出兩者。v 不在十六進位字元集內，不可能誤判。
const ephemeralSessionMarker = "v"

// NewEphemeralSessionID 產生帶有 Project 歸屬的 Session ID。
//
// 格式為 session_e<projectHex>_<random>，其中 projectHex 是 Project ID 去掉
// project_ 前綴後的部分。projectID 無法編碼時退回一般 ID——寧可讓對話落在
// 預設根，也不要產生一個解析不出根目錄、之後每次存取都失敗的 ID。
func NewEphemeralSessionID(projectID string) string {
	code := projectIDCode(projectID)
	if code == "" {
		return NewID("session")
	}
	return "session_" + ephemeralSessionMarker + code + "_" + randomIDSuffix()
}

// EphemeralProjectCodeFromSessionID 從 Session ID 取回 Project 代碼。
//
// 一般 Session 與所有既有格式都會回傳空字串，呼叫端據此使用預設根目錄。
func EphemeralProjectCodeFromSessionID(sessionID string) string {
	rest, found := strings.CutPrefix(strings.TrimSpace(sessionID), "session_"+ephemeralSessionMarker)
	if !found {
		return ""
	}
	code, _, found := strings.Cut(rest, "_")
	if !found {
		return ""
	}
	return projectIDCode(code)
}

// projectIDCode 取出 Project ID 中可用於編碼的部分，並確認它只含安全字元。
//
// 這個值會成為目錄名稱的一部分，因此非十六進位字元一律拒絕，不做清理後放行：
// 悄悄改寫過的代碼會對應到錯誤的 RAM disk。
func projectIDCode(projectID string) string {
	code := strings.TrimSpace(projectID)
	code = strings.TrimPrefix(code, "project_")
	if code == "" {
		return ""
	}
	for _, value := range code {
		if (value < '0' || value > '9') && (value < 'a' || value > 'f') {
			return ""
		}
	}
	return code
}
