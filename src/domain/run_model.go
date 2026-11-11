package domain

import "sync"

// RunModelSelection 只在回合邊界取用；連續選擇以最後一次為準，不中斷已派送工作。
type RunModelSelection struct {
	mu       sync.Mutex
	model    string
	thinking *string
}

func (s *RunModelSelection) Set(model string) {
	s.Update(&model, nil)
}

// Update 合併不同欄位的更新；空字串 Thinking 表示恢復自動，不能當成未修改。
func (s *RunModelSelection) Update(model, thinking *string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if model != nil {
		s.model = *model
	}
	if thinking != nil {
		value := *thinking
		s.thinking = &value
	}
}

func (s *RunModelSelection) Take() string {
	model, _ := s.TakeSettings()
	return model
}

func (s *RunModelSelection) TakeSettings() (string, *string) {
	if s == nil {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	model := s.model
	thinking := s.thinking
	s.model = ""
	s.thinking = nil
	return model, thinking
}
