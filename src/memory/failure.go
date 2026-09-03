package memory

import "strings"

// FailureRecallTracker 判斷「同一個工具連續失敗兩次」這個召回時機。
//
// 這是記憶最有價值、目前卻完全沒被利用的時刻：模型正在重複一個以前解過的錯。
// 只在第二次失敗時觸發一次，之後同一個工具再失敗也不再重複檢索——同樣的記憶
// 每輪重送只是佔提示。
type FailureRecallTracker struct {
	tool     string
	failures int
	recalled map[string]bool
}

// failureRecallThreshold 是觸發門檻。第一次失敗常常只是打錯字，模型自己會修；
// 第二次才代表它沒有頭緒。
const failureRecallThreshold = 2

// Observe 記錄一次工具結果，回傳這次是否該重新檢索記憶。
func (t *FailureRecallTracker) Observe(tool string, failed bool) bool {
	if t == nil {
		return false
	}
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}
	if !failed {
		// 成功就把計數清掉：連續兩次指的是中間沒有成功過。
		if t.tool == tool {
			t.tool = ""
			t.failures = 0
		}
		return false
	}
	if t.tool != tool {
		t.tool = tool
		t.failures = 0
	}
	t.failures++
	if t.failures != failureRecallThreshold || t.recalled[tool] {
		return false
	}
	if t.recalled == nil {
		t.recalled = map[string]bool{}
	}
	t.recalled[tool] = true
	return true
}

// FailureRecallQuery 組出檢索用的字串：工具名稱加上錯誤訊息的前段。
//
// 錯誤訊息尾端常是堆疊或路徑，對檢索沒有幫助又會稀釋關鍵詞。
func FailureRecallQuery(tool, message string) string {
	message = strings.TrimSpace(message)
	if runes := []rune(message); len(runes) > failureQueryRunes {
		message = string(runes[:failureQueryRunes])
	}
	return strings.TrimSpace(tool + " " + message)
}

const failureQueryRunes = 300
