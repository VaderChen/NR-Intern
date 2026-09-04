package question

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"AgenticService/src/domain"
)

// maxPendingQuestions 是同時等待中的問題上限。
//
// 正常情況只會有一個：工具是序列執行的。這個上限存在只是為了讓失控的情況
// 停在一個看得懂的錯誤上，而不是把記憶體用光。
const maxPendingQuestions = 16

type pendingQuestion struct {
	question domain.UserQuestion
	answer   chan domain.UserQuestionAnswer
}

// Coordinator 是 process 內的等待協調器。
//
// 刻意不持久化：問題只在工具等待的那段時間有意義，Run 結束後就沒有人會回答了。
// 重新連線的補救是工具週期性重送問題（見 ask_user 的 heartbeat），不是把問題寫進資料庫。
type Coordinator struct {
	mu      sync.Mutex
	pending map[string]*pendingQuestion
}

func NewCoordinator() *Coordinator {
	return &Coordinator{pending: map[string]*pendingQuestion{}}
}

func (c *Coordinator) Begin(question domain.UserQuestion) error {
	if c == nil {
		return fmt.Errorf("%w: question coordinator is unavailable", domain.ErrConflict)
	}
	question.ID = strings.TrimSpace(question.ID)
	if question.ID == "" {
		return fmt.Errorf("%w: question id is required", domain.ErrInvalidInput)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.pending[question.ID]; exists {
		return fmt.Errorf("%w: question %q already exists", domain.ErrConflict, question.ID)
	}
	if len(c.pending) >= maxPendingQuestions {
		return fmt.Errorf("%w: too many questions are already waiting for an answer", domain.ErrConflict)
	}
	c.pending[question.ID] = &pendingQuestion{question: question, answer: make(chan domain.UserQuestionAnswer, 1)}
	return nil
}

func (c *Coordinator) Wait(ctx context.Context, questionID string) (domain.UserQuestionAnswer, error) {
	questionID = strings.TrimSpace(questionID)
	c.mu.Lock()
	pending := c.pending[questionID]
	c.mu.Unlock()
	if pending == nil {
		return domain.UserQuestionAnswer{}, fmt.Errorf("%w: question %q", domain.ErrNotFound, questionID)
	}
	select {
	case <-ctx.Done():
		c.Cancel(questionID)
		return domain.UserQuestionAnswer{}, ctx.Err()
	case answer := <-pending.answer:
		c.Cancel(questionID)
		return answer, nil
	}
}

// Answer 送出使用者的回覆。取消也是一種回覆，走同一條路徑。
func (c *Coordinator) Answer(answer domain.UserQuestionAnswer) error {
	if c == nil {
		return fmt.Errorf("%w: question coordinator is unavailable", domain.ErrConflict)
	}
	answer.QuestionID = strings.TrimSpace(answer.QuestionID)
	answer.Selected = strings.TrimSpace(answer.Selected)
	answer.Custom = strings.TrimSpace(answer.Custom)
	if answer.QuestionID == "" {
		return fmt.Errorf("%w: question id is required", domain.ErrInvalidInput)
	}
	c.mu.Lock()
	pending := c.pending[answer.QuestionID]
	if pending == nil {
		c.mu.Unlock()
		return fmt.Errorf("%w: pending question %q", domain.ErrNotFound, answer.QuestionID)
	}
	if !answer.Canceled {
		if answer.Selected == "" && answer.Custom == "" {
			c.mu.Unlock()
			return fmt.Errorf("%w: pick an option or provide custom text", domain.ErrInvalidInput)
		}
		// 選項是 Agent 提供的，回覆必須落在那份清單裡，否則就該走自訂輸入。
		// 少了這道檢查，前端的任何拼字錯誤都會變成模型收到的「使用者選了 X」。
		if answer.Selected != "" && !optionExists(pending.question.Options, answer.Selected) {
			c.mu.Unlock()
			return fmt.Errorf("%w: %q is not one of the offered options", domain.ErrInvalidInput, answer.Selected)
		}
	}
	if answer.AnsweredAt.IsZero() {
		answer.AnsweredAt = time.Now().UTC()
	}
	select {
	case pending.answer <- answer:
	default:
		c.mu.Unlock()
		return fmt.Errorf("%w: question %q already has an answer", domain.ErrConflict, answer.QuestionID)
	}
	c.mu.Unlock()
	return nil
}

func (c *Coordinator) Cancel(questionID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.pending, strings.TrimSpace(questionID))
	c.mu.Unlock()
}

func optionExists(options []domain.UserQuestionOption, label string) bool {
	for _, option := range options {
		if option.Label == label {
			return true
		}
	}
	return false
}
