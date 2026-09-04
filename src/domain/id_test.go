package domain

import (
	"strings"
	"testing"
)

// 隔離 Session 的 ID 要能還原出 Project 代碼，這是根目錄解析的唯一依據。
func TestEphemeralSessionIDRoundTrip(t *testing.T) {
	for _, projectID := range []string{"project_abc123", "abc123", "project_0f9e8d7c6b5a"} {
		sessionID := NewEphemeralSessionID(projectID)
		if !strings.HasPrefix(sessionID, "session_v") {
			t.Fatalf("%s 產生的 ID 缺少標記：%s", projectID, sessionID)
		}
		want := strings.TrimPrefix(projectID, "project_")
		if got := EphemeralProjectCodeFromSessionID(sessionID); got != want {
			t.Fatalf("%s 還原出 %q，應為 %q（ID=%s）", projectID, got, want, sessionID)
		}
	}
}

// 同一個 Project 的兩個 Session 不能撞 ID。
func TestEphemeralSessionIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for index := 0; index < 200; index++ {
		id := NewEphemeralSessionID("project_abc123")
		if seen[id] {
			t.Fatalf("重複的 ID：%s", id)
		}
		seen[id] = true
	}
}

// 所有既有格式都必須被判定為「非隔離」，否則升級後舊對話會被導到錯誤的根目錄。
func TestExistingIDFormatsResolveToNoProject(t *testing.T) {
	cases := []string{
		NewID("session"),
		"session_0f9e8d7c6b5a4938271605f4",
		"session_",
		"session",
		"",
		"   ",
		"run_abc123",
		"project_abc123",
		// 有標記但沒有分隔線，或代碼含非十六進位字元：都不可信，一律當一般 Session。
		"session_vabc123",
		"session_v_abc123",
		"session_vzzz_abc",
		"session_v../../etc_abc",
		// 一般 ID 的隨機部分是十六進位，開頭可能是 e：不能被當成標記。
		"session_ecb071cadb6401a08b0a2a67",
	}
	for _, sessionID := range cases {
		if code := EphemeralProjectCodeFromSessionID(sessionID); code != "" {
			t.Fatalf("%q 不該解析出 Project 代碼，卻得到 %q", sessionID, code)
		}
	}
}

// 代碼會成為目錄名稱的一部分，含路徑字元的輸入必須拒絕而不是清理後放行。
func TestEphemeralSessionIDRejectsUnsafeProjectID(t *testing.T) {
	for _, projectID := range []string{"../escape", "proj/../../etc", "project_ab/cd", "  ", "project_", "非十六進位"} {
		sessionID := NewEphemeralSessionID(projectID)
		if strings.HasPrefix(sessionID, "session_v") {
			t.Fatalf("%q 不該產生隔離 ID，卻得到 %s", projectID, sessionID)
		}
		if strings.ContainsAny(sessionID, `/\`) {
			t.Fatalf("%q 產生的 ID 含路徑字元：%s", projectID, sessionID)
		}
	}
}
