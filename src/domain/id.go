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

// NewEphemeralID 產生帶有 Project 歸屬的 ID。
//
// 格式為 <prefix>_v<projectHex>_<random>，其中 projectHex 是 Project ID 去掉
// project_ 前綴後的部分。Session 與 Run 都用這一套：兩者的儲存都要能只憑 ID
// 找到所屬的 RAM disk，事件檔更是只拿得到 Run ID。
//
// projectID 無法編碼時退回一般 ID——寧可讓資料落在預設根，也不要產生一個
// 解析得出代碼、卻對應不到任何磁碟的 ID，那種紀錄之後每次存取都會失敗。
func NewEphemeralID(prefix, projectID string) string {
	code := projectIDCode(projectID)
	if code == "" {
		return NewID(prefix)
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "id"
	}
	return prefix + "_" + ephemeralSessionMarker + code + "_" + randomIDSuffix()
}

// NewEphemeralSessionID 是 Session 專用的便利包裝。
func NewEphemeralSessionID(projectID string) string {
	return NewEphemeralID("session", projectID)
}

// NewRunIDForSession 讓 Run 沿用所屬 Session 的 Project 歸屬。
//
// 事件檔以 Run ID 命名，讀取時手上只有 Run ID、也沒有記憶體快取可以回頭查
// Session，所以歸屬必須編進 Run ID 本身。直接從 Session ID 取代碼即可，
// 不需要再查一次 Project 儲存。
func NewRunIDForSession(sessionID string) string {
	code := EphemeralProjectCodeFromID(sessionID)
	if code == "" {
		return NewID("run")
	}
	return "run_" + ephemeralSessionMarker + code + "_" + randomIDSuffix()
}

// EphemeralProjectCodeFromID 從 Session 或 Run ID 取回 Project 代碼。
//
// 一般 ID 與所有既有格式都會回傳空字串，呼叫端據此使用預設根目錄。
// 切在第一個 _v 是安全的：ID 的隨機部分是十六進位，不含 v。
func EphemeralProjectCodeFromID(id string) string {
	_, rest, found := strings.Cut(strings.TrimSpace(id), "_"+ephemeralSessionMarker)
	if !found {
		return ""
	}
	code, _, found := strings.Cut(rest, "_")
	if !found {
		return ""
	}
	return projectIDCode(code)
}

// EphemeralProjectCodeFromSessionID 保留原名，語意與 EphemeralProjectCodeFromID 相同。
func EphemeralProjectCodeFromSessionID(sessionID string) string {
	return EphemeralProjectCodeFromID(sessionID)
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
