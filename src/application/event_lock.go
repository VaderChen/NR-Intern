package application

import "sync"

func (s *Service) eventLock(runID string) *sync.Mutex {
	var hash uint64 = 14695981039346656037
	for i := 0; i < len(runID); i++ {
		hash = (hash ^ uint64(runID[i])) * 1099511628211
	}
	return &s.eventMu[hash%uint64(len(s.eventMu))]
}
