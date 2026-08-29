package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var ErrExternalBackend = errors.New("backend is not owned by desktop")

type Config struct {
	BackendURL     string
	Executable     string
	Arguments      []string
	WorkingDir     string
	Environment    []string
	StartupTimeout time.Duration
	StopTimeout    time.Duration
	LogMaxBytes    int
}

type Status struct {
	State      string     `json:"state"`
	BackendURL string     `json:"backend_url"`
	Healthy    bool       `json:"healthy"`
	Owned      bool       `json:"owned"`
	PID        int        `json:"pid,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
}

type Supervisor struct {
	config Config
	client *http.Client
	logs   *LogBuffer

	mu        sync.Mutex
	command   *exec.Cmd
	done      chan error
	startedAt *time.Time
	lastError string
	starting  bool
}

func New(config Config) (*Supervisor, error) {
	backendURL := strings.TrimRight(strings.TrimSpace(config.BackendURL), "/")
	parsed, err := url.Parse(backendURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid backend URL %q", config.BackendURL)
	}
	config.BackendURL = backendURL
	if config.StartupTimeout <= 0 {
		config.StartupTimeout = 20 * time.Second
	}
	if config.StopTimeout <= 0 {
		config.StopTimeout = 10 * time.Second
	}
	return &Supervisor{
		config: config,
		client: &http.Client{Timeout: 2 * time.Second},
		logs:   NewLogBuffer(config.LogMaxBytes),
	}, nil
}

func (s *Supervisor) Start(ctx context.Context) error {
	if s.healthy(ctx) {
		return nil
	}
	s.mu.Lock()
	if s.command != nil || s.starting {
		s.mu.Unlock()
		return nil
	}
	if strings.TrimSpace(s.config.Executable) == "" {
		s.mu.Unlock()
		return fmt.Errorf("backend executable is not configured")
	}
	s.starting = true
	s.lastError = ""
	s.mu.Unlock()

	command := exec.Command(s.config.Executable, s.config.Arguments...)
	configureChildProcess(command)
	command.Dir = s.config.WorkingDir
	if len(s.config.Environment) > 0 {
		command.Env = append(os.Environ(), s.config.Environment...)
	}
	command.Stdout = s.logs
	command.Stderr = s.logs
	if err := command.Start(); err != nil {
		s.setStartFailure(err)
		return fmt.Errorf("start backend: %w", err)
	}
	now := time.Now().UTC()
	done := make(chan error, 1)
	s.mu.Lock()
	s.command = command
	s.done = done
	s.startedAt = &now
	s.starting = false
	s.mu.Unlock()
	go s.wait(command, done)

	deadline := time.NewTimer(s.config.StartupTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if s.healthy(ctx) {
			return nil
		}
		select {
		case err := <-done:
			if err == nil {
				err = errors.New("backend exited before becoming healthy")
			}
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("backend did not become healthy within %s", s.config.StartupTimeout)
		case <-ticker.C:
		}
	}
}

func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	command := s.command
	done := s.done
	s.mu.Unlock()
	if command == nil || command.Process == nil {
		if s.healthy(ctx) {
			return ErrExternalBackend
		}
		return nil
	}
	_ = command.Process.Signal(os.Interrupt)
	timer := time.NewTimer(s.config.StopTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill backend: %w", err)
		}
		<-done
		return nil
	}
}

func (s *Supervisor) Restart(ctx context.Context) error {
	if err := s.Stop(ctx); err != nil && !errors.Is(err, ErrExternalBackend) {
		return err
	}
	if s.healthy(ctx) {
		return ErrExternalBackend
	}
	return s.Start(ctx)
}

func (s *Supervisor) Status(ctx context.Context) Status {
	healthy := s.healthy(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	status := Status{BackendURL: s.config.BackendURL, Healthy: healthy, LastError: s.lastError, StartedAt: s.startedAt}
	switch {
	case s.starting:
		status.State = "starting"
	case s.command != nil:
		status.State = "running"
		status.Owned = true
		if s.command.Process != nil {
			status.PID = s.command.Process.Pid
		}
	case healthy:
		status.State = "external"
	default:
		status.State = "stopped"
	}
	return status
}

func (s *Supervisor) Logs() string { return s.logs.String() }

func (s *Supervisor) BackendURL() string { return s.config.BackendURL }

func (s *Supervisor) wait(command *exec.Cmd, done chan error) {
	err := command.Wait()
	done <- err
	close(done)
	s.mu.Lock()
	if s.command == command {
		s.command = nil
		s.done = nil
		s.startedAt = nil
		if err != nil {
			s.lastError = err.Error()
		}
	}
	s.mu.Unlock()
}

func (s *Supervisor) healthy(ctx context.Context) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.config.BackendURL+"/healthz", nil)
	if err != nil {
		return false
	}
	response, err := s.client.Do(request)
	if err != nil {
		return false
	}
	_ = response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 300
}

func (s *Supervisor) setStartFailure(err error) {
	s.mu.Lock()
	s.starting = false
	s.lastError = err.Error()
	s.mu.Unlock()
}
