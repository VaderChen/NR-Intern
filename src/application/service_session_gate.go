package application

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"errors"
	"fmt"
	"strings"
)

func (s *Service) resolveSession(ctx context.Context, sessionID string) (ports.AgentEngine, domain.Session, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, domain.Session{}, fmt.Errorf("%w: session id is required", domain.ErrInvalidInput)
	}
	for _, engine := range s.registry.Engines() {
		session, err := engine.GetSession(ctx, sessionID)
		if err == nil {
			return engine, session, nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, domain.Session{}, err
		}
	}
	return nil, domain.Session{}, fmt.Errorf("%w: session %q", domain.ErrNotFound, sessionID)
}

func (s *Service) acquireSession(ctx context.Context, sessionID, runID string) (func(), error) {
	for {
		s.mu.Lock()
		gate := s.sessionGates[sessionID]
		if gate == nil {
			gate = make(chan struct{}, 1)
			gate <- struct{}{}
			s.sessionGates[sessionID] = gate
		}
		signal := s.sessionSignals[sessionID]
		if signal == nil {
			signal = make(chan struct{})
			s.sessionSignals[sessionID] = signal
		}
		pausedBy := s.pausedSessions[sessionID]
		s.mu.Unlock()
		if pausedBy != "" && pausedBy != runID {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-signal:
				continue
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-gate:
		}
		// pause 可能在取得 token 前一刻建立，因此持有 token 後必須再確認一次。
		s.mu.Lock()
		pausedBy = s.pausedSessions[sessionID]
		signal = s.sessionSignals[sessionID]
		s.mu.Unlock()
		if pausedBy == "" || pausedBy == runID {
			return func() { gate <- struct{}{} }, nil
		}
		gate <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-signal:
		}
	}
}

func (s *Service) pauseSession(sessionID, runID string) {
	s.mu.Lock()
	s.pausedSessions[sessionID] = runID
	s.notifySessionStateLocked(sessionID)
	s.mu.Unlock()
}

func (s *Service) clearSessionPause(sessionID, runID string) {
	s.mu.Lock()
	if s.pausedSessions[sessionID] == runID {
		delete(s.pausedSessions, sessionID)
		s.notifySessionStateLocked(sessionID)
	}
	s.mu.Unlock()
}

func (s *Service) notifySessionStateLocked(sessionID string) {
	if signal := s.sessionSignals[sessionID]; signal != nil {
		close(signal)
	}
	s.sessionSignals[sessionID] = make(chan struct{})
}

func (s *Service) clearActive(runID string) {
	s.mu.Lock()
	if item, ok := s.active[runID]; ok {
		item.cancel()
		delete(s.active, runID)
	}
	s.clearRunPauseLocked(runID)
	s.mu.Unlock()
}

func (s *Service) clearRunPause(runID string) {
	s.mu.Lock()
	s.clearRunPauseLocked(runID)
	s.mu.Unlock()
}

func (s *Service) clearRunPauseLocked(runID string) {
	delete(s.pausedRuns, runID)
	if signal := s.runSignals[runID]; signal != nil {
		close(signal)
	}
	delete(s.runSignals, runID)
}

func (s *Service) waitIfRunPaused(ctx context.Context, runID string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		paused := s.pausedRuns[runID]
		signal := s.runSignals[runID]
		s.mu.Unlock()
		if !paused {
			return nil
		}
		if signal == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-signal:
		}
	}
}

func (s *Service) hasActiveSession(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.active {
		if run.sessionID == sessionID {
			return true
		}
	}
	return false
}

func (s *Service) notifyRun(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, channel := range s.watchers[runID] {
		select {
		case channel <- struct{}{}:
		default:
		}
	}
}
