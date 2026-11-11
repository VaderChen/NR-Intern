package domain

import (
	"strings"
	"sync"
	"time"
)

// RunInputMessage 是 Run 執行中由使用者追加的訊息。它不會中斷進行中的模型回覆，
// 而是在目前這一輪結束、下一輪請求組裝之前寫入對話。
type RunInputMessage struct {
	ID        string    `json:"id"`
	Content   string    `json:"input"`
	ClientID  string    `json:"client_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// RunInputQueue 保存尚未被 Harness 取用的追加訊息。Application 寫入，Harness
// 在回合邊界取出；Close 之後不再接受新訊息，並回傳尚未消化的部分供退回使用者。
type RunInputQueue struct {
	mu      sync.Mutex
	pending []RunInputMessage
	closed  bool
}

// NewRunInputQueue 建立空的追加訊息佇列。
func NewRunInputQueue() *RunInputQueue {
	return &RunInputQueue{}
}

// Push 加入一則訊息；佇列已關閉時回傳 false，呼叫端應改以新 Run 送出。
func (q *RunInputQueue) Push(message RunInputMessage) bool {
	if q == nil || strings.TrimSpace(message.Content) == "" {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	q.pending = append(q.pending, message)
	return true
}

// Take 取出目前所有待插入訊息，依送達順序排列。
func (q *RunInputQueue) Take() []RunInputMessage {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return nil
	}
	taken := q.pending
	q.pending = nil
	return taken
}

// Pending 回報目前是否有待插入訊息，不取出。
func (q *RunInputQueue) Pending() bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending) > 0
}

// Count 回報目前待插入的訊息數。
func (q *RunInputQueue) Count() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// Close 停止接受新訊息並回傳尚未消化的部分。重複呼叫只會回傳空值。
func (q *RunInputQueue) Close() []RunInputMessage {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	remaining := q.pending
	q.pending = nil
	return remaining
}
