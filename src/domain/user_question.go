package domain

import "time"

// UserQuestion 是 Agent 在工作進行中請使用者做的一次抉擇。
//
// 與工具核准的差別在語意：核准是「這件事可不可以做」，只有是與否；抉擇是
// 「該往哪個方向做」，答案本身就是工作的一部分。因此兩者不共用同一組型別。
type UserQuestion struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	ToolCallID string `json:"tool_call_id"`
	Question   string `json:"question"`
	// Context 說明為什麼需要這個決定，讓使用者不必回頭翻對話就能判斷。
	Context string               `json:"context,omitempty"`
	Options []UserQuestionOption `json:"options"`
	// CustomLabel 是自訂輸入欄的提示文字；空字串時使用預設提示。
	// 自訂欄一律存在——選項是 Agent 想到的，使用者的答案不必被限制在裡面。
	CustomLabel string    `json:"custom_label,omitempty"`
	AskedAt     time.Time `json:"asked_at"`
}

type UserQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// UserQuestionAnswer 是使用者的回覆。三種結果：選了某個選項、自己輸入、或取消。
//
// 取消不是錯誤：使用者本來就沒有義務回答。Agent 收到取消時應該自己做決定或
// 說明為什麼卡住，而不是重複追問。
type UserQuestionAnswer struct {
	QuestionID string    `json:"question_id"`
	Selected   string    `json:"selected,omitempty"`
	Custom     string    `json:"custom,omitempty"`
	Canceled   bool      `json:"canceled,omitempty"`
	AnsweredAt time.Time `json:"answered_at"`
}

// Resolved 回傳使用者實際選擇的內容；取消時為空字串。
func (a UserQuestionAnswer) Resolved() string {
	if a.Canceled {
		return ""
	}
	if a.Custom != "" {
		return a.Custom
	}
	return a.Selected
}
