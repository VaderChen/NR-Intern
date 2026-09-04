package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
)

const (
	// AskUserToolName 是問答選單工具。
	AskUserToolName = "ask_user"
	// questionHeartbeatInterval 週期性重送問題。
	//
	// 問題只存在於記憶體，不落地。使用者重新整理或斷線重連之後，畫面上的對話框
	// 就沒了，但工具還在等——沒有這個重送，Run 會一直卡到逾時。
	questionHeartbeatInterval = 5 * time.Second
	// defaultQuestionTimeout 是等待上限。
	//
	// 使用者可能根本不在電腦前。逾時不是錯誤：Agent 收到「沒有回應」之後應該
	// 自己做決定或說明卡在哪，而不是把 Run 永遠掛著。
	defaultQuestionTimeout = 10 * time.Minute
	maxQuestionOptions     = 8
	maxQuestionRunes       = 500
)

// Tool 讓 Agent 在工作進行中請使用者做一次抉擇。
//
// 為什麼要阻塞而不是結束 Run 再開一輪：需要抉擇的時候 Agent 正做到一半，
// 中斷再重來會讓它把已經確認過的事重新查一遍。阻塞在工具裡則是「等一個答案」，
// 對模型來說就只是一個比較慢的工具。
type Tool struct {
	Questions ports.QuestionCoordinator
	Timeout   time.Duration
}

func New(coordinator ports.QuestionCoordinator, timeout time.Duration) *Tool {
	if timeout <= 0 {
		timeout = defaultQuestionTimeout
	}
	return &Tool{Questions: coordinator, Timeout: timeout}
}

func (t *Tool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:     AskUserToolName,
		Label:    "詢問使用者",
		Version:  "1.0.0",
		Category: "interaction",
		Description: "在工作進行中請使用者做一次抉擇：跳出選單讓他挑一個選項，或自己輸入答案。" +
			"只在答案會改變接下來要做什麼、而且你無法自行合理判斷時使用——例如有多個都說得通的做法、" +
			"或需要確認對象、範圍、格式。不要用它來確認你已經知道的事、要求核准，或代替自己查資料。" +
			"使用者可以取消：取消或逾時就自行選一個合理做法繼續，並說明你的假設，不要重複追問同一件事。",
		Platforms: []string{"darwin", "linux", "windows"},
		// ReadOnly 讓它免於人工核准，也讓第一階段就能使用：詢問本身沒有副作用，
		// 而一個問不到問題的問答工具沒有意義。
		ReadOnly:     true,
		Capabilities: []string{"user-choice", "free-text-answer", "cancelable"},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{"type": "string", "description": "要問使用者的問題，一句話講完"},
				"context":  map[string]any{"type": "string", "description": "為什麼需要這個決定，讓使用者不必翻對話就能判斷"},
				"options": map[string]any{
					"type":        "array",
					"maxItems":    maxQuestionOptions,
					"description": "可選項目；使用者仍可自行輸入其他答案",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"label"},
						"properties": map[string]any{
							"label":       map[string]any{"type": "string", "description": "選項文字，簡短明確"},
							"description": map[string]any{"type": "string", "description": "選這個會發生什麼"},
						},
					},
				},
				"custom_label": map[string]any{"type": "string", "description": "自訂輸入欄的提示文字"},
			},
			"required": []string{"question", "options"},
		},
	}
}

func (t *Tool) Execute(ctx context.Context, invocation tools.Invocation, sink ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if err := ctx.Err(); err != nil {
		return domain.ToolExecution{}, err
	}
	if t == nil || t.Questions == nil {
		return failure(invocation.Call, "問答選單目前無法使用"), nil
	}
	question, err := questionFromArguments(invocation)
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	if err := t.Questions.Begin(question); err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	defer t.Questions.Cancel(question.ID)

	// Begin 必須先於事件送出：使用者可能在事件送達的同一瞬間就回答。
	if err := emitQuestion(sink, invocation.Call, question, 0); err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()
	stopHeartbeat := startHeartbeat(waitCtx, sink, invocation.Call, question)
	answer, err := t.Questions.Wait(waitCtx, question.ID)
	stopHeartbeat()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return unanswered(invocation.Call, question, "使用者未在時限內回答"), nil
		}
		return domain.ToolExecution{}, err
	}
	if answer.Canceled {
		return unanswered(invocation.Call, question, "使用者取消了這次選擇"), nil
	}
	resolved := answer.Resolved()
	source := "option"
	if answer.Custom != "" {
		source = "custom"
	}
	return jsonExecution(invocation.Call, map[string]any{
		"question": question.Question,
		"answer":   resolved,
		"source":   source,
		"answered": true,
	})
}

// unanswered 是取消與逾時共用的結果。
//
// 刻意不標成錯誤：使用者沒有義務回答，把它當失敗會讓迴圈防護開始計數，
// 也會讓模型以為自己做錯了什麼而重試。
func unanswered(call domain.ToolCall, question domain.UserQuestion, reason string) domain.ToolExecution {
	payload, _ := json.Marshal(map[string]any{
		"question": question.Question,
		"answered": false,
		"reason":   reason,
		"guidance": "使用者沒有回答。請自行選一個合理做法繼續，並在最終答案裡說明你依據什麼假設，不要重複詢問同一件事。",
	})
	return domain.ToolExecution{
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    string(payload),
		Details:    map[string]any{"answered": false, "reason": reason},
	}
}

func questionFromArguments(invocation tools.Invocation) (domain.UserQuestion, error) {
	text := strings.TrimSpace(toolutil.String(invocation.Call.Arguments, "question"))
	if text == "" {
		return domain.UserQuestion{}, fmt.Errorf("question is required")
	}
	if len([]rune(text)) > maxQuestionRunes {
		return domain.UserQuestion{}, fmt.Errorf("question exceeds %d characters", maxQuestionRunes)
	}
	options, err := optionsFromArguments(invocation.Call.Arguments["options"])
	if err != nil {
		return domain.UserQuestion{}, err
	}
	return domain.UserQuestion{
		ID:          domain.NewID("question"),
		SessionID:   invocation.Session.ID,
		ToolCallID:  invocation.Call.ID,
		Question:    text,
		Context:     strings.TrimSpace(toolutil.String(invocation.Call.Arguments, "context")),
		Options:     options,
		CustomLabel: strings.TrimSpace(toolutil.String(invocation.Call.Arguments, "custom_label")),
		AskedAt:     time.Now().UTC(),
	}, nil
}

func optionsFromArguments(value any) ([]domain.UserQuestionOption, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("options must be a non-empty array")
	}
	if len(raw) > maxQuestionOptions {
		return nil, fmt.Errorf("options cannot exceed %d entries", maxQuestionOptions)
	}
	options := make([]domain.UserQuestionOption, 0, len(raw))
	seen := map[string]bool{}
	for index, entry := range raw {
		label, description := "", ""
		switch typed := entry.(type) {
		case string:
			// 模型常常直接給字串陣列。這是它想表達的意思，沒有理由退回去要它重寫。
			label = strings.TrimSpace(typed)
		case map[string]any:
			label = strings.TrimSpace(toolutil.String(typed, "label"))
			description = strings.TrimSpace(toolutil.String(typed, "description"))
		default:
			return nil, fmt.Errorf("option %d must be a string or an object with a label", index+1)
		}
		if label == "" {
			return nil, fmt.Errorf("option %d has no label", index+1)
		}
		// 選項是使用者要點的東西，重複的兩個一模一樣的按鈕沒有意義，
		// 而且回覆用 label 比對，重複會讓答案變得沒辦法分辨。
		if seen[label] {
			return nil, fmt.Errorf("option %d repeats the label %q", index+1, label)
		}
		seen[label] = true
		options = append(options, domain.UserQuestionOption{Label: label, Description: description})
	}
	return options, nil
}

func emitQuestion(sink ports.ToolUpdateSink, call domain.ToolCall, question domain.UserQuestion, elapsed time.Duration) error {
	if sink == nil {
		return fmt.Errorf("問答選單需要可用的事件通道")
	}
	content := question.Question
	if elapsed > 0 {
		content = fmt.Sprintf("%s（已等待 %s）", question.Question, elapsed.Round(time.Second))
	}
	return sink(domain.ToolExecution{
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    content,
		Details: map[string]any{
			"phase":           "user_question",
			"question":        question,
			"elapsed_seconds": int(elapsed.Seconds()),
		},
	})
}

// startHeartbeat 週期性重送問題，讓重新連線的介面能把對話框叫回來。
func startHeartbeat(ctx context.Context, sink ports.ToolUpdateSink, call domain.ToolCall, question domain.UserQuestion) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(questionHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				_ = emitQuestion(sink, call, question, time.Since(question.AskedAt))
			}
		}
	}()
	return func() { close(done) }
}

func failure(call domain.ToolCall, message string) domain.ToolExecution {
	return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: message, IsError: true}
}

func jsonExecution(call domain.ToolCall, payload map[string]any) (domain.ToolExecution, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return domain.ToolExecution{}, err
	}
	return domain.ToolExecution{
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    string(data),
		Details:    payload,
	}, nil
}
