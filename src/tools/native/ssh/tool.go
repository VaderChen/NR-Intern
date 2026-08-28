package ssh

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Profile struct {
	Address               string `json:"address"`
	User                  string `json:"user"`
	Password              string `json:"password,omitempty"`
	PrivateKeyPath        string `json:"private_key_path,omitempty"`
	PrivateKeyPassphrase  string `json:"private_key_passphrase,omitempty"`
	KnownHostsFile        string `json:"known_hosts_file,omitempty"`
	HostKeySHA256         string `json:"host_key_sha256,omitempty"`
	InsecureIgnoreHostKey bool   `json:"insecure_ignore_host_key,omitempty"`
	ConnectTimeoutSeconds int    `json:"connect_timeout_seconds,omitempty"`
	ConnectAttempts       int    `json:"connect_attempts,omitempty"`
	KeepAliveSeconds      int    `json:"keep_alive_seconds,omitempty"`
}

type Tool struct {
	profiles       map[string]Profile
	maxOutputBytes int
	maxTimeout     time.Duration
}

func New(profiles map[string]Profile, maxOutputBytes int, maxTimeout time.Duration) *Tool {
	if maxOutputBytes <= 0 {
		maxOutputBytes = 512 * 1024
	}
	if maxTimeout <= 0 {
		maxTimeout = 30 * time.Minute
	}
	copyProfiles := make(map[string]Profile, len(profiles))
	for name, profile := range profiles {
		if name = strings.TrimSpace(name); name != "" {
			copyProfiles[name] = profile
		}
	}
	return &Tool{profiles: copyProfiles, maxOutputBytes: maxOutputBytes, maxTimeout: maxTimeout}
}

func (t *Tool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:               "ssh_exec",
		Label:              "SSH 遠端命令",
		Version:            "1.0.0",
		Category:           "remote",
		Description:        "使用 Go 原生 SSH client，透過後端預先設定的連線 profile 執行遠端命令；模型不接觸連線密碼或私鑰。",
		Platforms:          []string{"darwin", "linux", "windows"},
		Capabilities:       []string{"ssh", "host-key-verification", "profile-credentials", "connect-retry", "keepalive", "timeout", "bounded-output"},
		RequiresPermission: true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"profile":         map[string]any{"type": "string", "description": "後端管理者設定的 SSH profile 名稱"},
				"command":         map[string]any{"type": "string"},
				"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": int(t.maxTimeout.Seconds()), "default": 120},
			},
			"required": []string{"profile", "command"},
		},
	}
}

func (t *Tool) Execute(ctx context.Context, invocation tools.Invocation, sink ports.ToolUpdateSink) (domain.ToolExecution, error) {
	profileName := toolutil.String(invocation.Call.Arguments, "profile")
	commandText := toolutil.String(invocation.Call.Arguments, "command")
	if profileName == "" || commandText == "" {
		return sshFailure(invocation.Call, "profile and command are required", nil), nil
	}
	profile, exists := t.profiles[profileName]
	if !exists {
		return sshFailure(invocation.Call, "SSH profile is unavailable", nil), nil
	}
	timeoutSeconds := toolutil.Int(invocation.Call.Arguments, "timeout_seconds", 120, 1, int(t.maxTimeout.Seconds()))
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	client, attempts, err := dialWithRetry(runCtx, profile, func(attempt int) {
		if sink != nil {
			_ = sink(domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Details: map[string]any{"phase": "ssh_connecting", "profile": profileName, "attempt": attempt}})
		}
	})
	if err != nil {
		if runCtx.Err() != nil && !errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return domain.ToolExecution{}, runCtx.Err()
		}
		return sshFailure(invocation.Call, fmt.Sprintf("connect SSH profile %q after %d attempt(s): %v", profileName, attempts, err), map[string]any{"profile": profileName, "connection_attempts": attempts}), nil
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return sshFailure(invocation.Call, fmt.Sprintf("create SSH session: %v", err), nil), nil
	}
	defer session.Close()
	stdout := toolutil.NewLimitedBuffer(t.maxOutputBytes)
	stderr := toolutil.NewLimitedBuffer(t.maxOutputBytes)
	session.Stdout = stdout
	session.Stderr = stderr
	if sink != nil {
		_ = sink(domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Details: map[string]any{"phase": "ssh_connected", "profile": profileName}})
	}
	startedAt := time.Now()
	done := make(chan error, 1)
	go func() { done <- session.Run(commandText) }()
	keepAliveCtx, stopKeepAlive := context.WithCancel(runCtx)
	defer stopKeepAlive()
	keepAliveErrors := make(chan error, 1)
	go keepAlive(keepAliveCtx, client, profile, keepAliveErrors)
	var runErr error
	select {
	case <-runCtx.Done():
		_ = session.Close()
		runErr = runCtx.Err()
	case runErr = <-done:
	case keepAliveErr := <-keepAliveErrors:
		_ = session.Close()
		_ = client.Close()
		<-done
		runErr = fmt.Errorf("SSH connection lost during command; command was not retried to avoid duplicate side effects: %w", keepAliveErr)
	}
	exitCode := 0
	var exitErr *gossh.ExitError
	if errors.As(runErr, &exitErr) {
		exitCode = exitErr.ExitStatus()
	} else if runErr != nil {
		exitCode = -1
	}
	details := map[string]any{
		"profile":             profileName,
		"exit_code":           exitCode,
		"duration_ms":         time.Since(startedAt).Milliseconds(),
		"stdout":              stdout.String(),
		"stderr":              stderr.String(),
		"stdout_cut":          stdout.Truncated(),
		"stderr_cut":          stderr.Truncated(),
		"timed_out":           errors.Is(runCtx.Err(), context.DeadlineExceeded),
		"connection_attempts": attempts,
	}
	content := formatSSHOutput(stdout.String(), stderr.String(), exitCode)
	if runErr != nil {
		if errors.Is(runCtx.Err(), context.Canceled) && !errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return domain.ToolExecution{}, runCtx.Err()
		}
		return sshFailure(invocation.Call, strings.TrimSpace(runErr.Error()+"\n"+content), details), nil
	}
	return domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Content: content, Details: details}, nil
}

func dialWithRetry(ctx context.Context, profile Profile, onAttempt func(int)) (*gossh.Client, int, error) {
	attempts := profile.ConnectAttempts
	if attempts <= 0 {
		attempts = 3
	}
	if attempts > 3 {
		attempts = 3
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if onAttempt != nil {
			onAttempt(attempt)
		}
		client, err := dial(ctx, profile)
		if err == nil {
			return client, attempt, nil
		}
		lastErr = err
		if attempt == attempts {
			break
		}
		timer := time.NewTimer(time.Duration(400*(1<<(attempt-1))) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, attempt, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, attempts, lastErr
}

func dial(ctx context.Context, profile Profile) (*gossh.Client, error) {
	address := strings.TrimSpace(profile.Address)
	if address == "" || strings.TrimSpace(profile.User) == "" {
		return nil, fmt.Errorf("profile address and user are required")
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		address = net.JoinHostPort(address, "22")
	}
	auth, err := authMethods(profile)
	if err != nil {
		return nil, err
	}
	hostKeyCallback, err := hostKeyCallback(profile)
	if err != nil {
		return nil, err
	}
	connectTimeout := time.Duration(profile.ConnectTimeoutSeconds) * time.Second
	if connectTimeout <= 0 {
		connectTimeout = 15 * time.Second
	}
	if connectTimeout > 2*time.Minute {
		connectTimeout = 2 * time.Minute
	}
	configuration := &gossh.ClientConfig{
		User:            strings.TrimSpace(profile.User),
		Auth:            auth,
		HostKeyCallback: hostKeyCallback,
		Timeout:         connectTimeout,
	}
	connection, err := (&net.Dialer{Timeout: configuration.Timeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(connectTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		_ = connection.Close()
		return nil, err
	}
	stopCancellation := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopCancellation()
	clientConnection, channels, requests, err := gossh.NewClientConn(connection, address, configuration)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		_ = clientConnection.Close()
		return nil, err
	}
	return gossh.NewClient(clientConnection, channels, requests), nil
}

func keepAlive(ctx context.Context, client *gossh.Client, profile Profile, failures chan<- error) {
	interval := time.Duration(profile.KeepAliveSeconds) * time.Second
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				select {
				case failures <- err:
				default:
				}
				return
			}
		}
	}
}

func authMethods(profile Profile) ([]gossh.AuthMethod, error) {
	methods := []gossh.AuthMethod{}
	if profile.PrivateKeyPath != "" {
		data, err := os.ReadFile(profile.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("read private key: %w", err)
		}
		var signer gossh.Signer
		if profile.PrivateKeyPassphrase != "" {
			signer, err = gossh.ParsePrivateKeyWithPassphrase(data, []byte(profile.PrivateKeyPassphrase))
		} else {
			signer, err = gossh.ParsePrivateKey(data)
		}
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		methods = append(methods, gossh.PublicKeys(signer))
	}
	if profile.Password != "" {
		methods = append(methods, gossh.Password(profile.Password))
	}
	if len(methods) == 0 {
		return nil, fmt.Errorf("profile has no authentication method")
	}
	return methods, nil
}

func hostKeyCallback(profile Profile) (gossh.HostKeyCallback, error) {
	if profile.KnownHostsFile != "" {
		callback, err := knownhosts.New(profile.KnownHostsFile)
		if err != nil {
			return nil, fmt.Errorf("load known_hosts: %w", err)
		}
		return callback, nil
	}
	expected := strings.TrimSpace(profile.HostKeySHA256)
	if expected != "" {
		return func(_ string, _ net.Addr, key gossh.PublicKey) error {
			actual := gossh.FingerprintSHA256(key)
			if len(actual) != len(expected) || subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
				return fmt.Errorf("SSH host key fingerprint mismatch")
			}
			return nil
		}, nil
	}
	if profile.InsecureIgnoreHostKey {
		return gossh.InsecureIgnoreHostKey(), nil
	}
	return nil, fmt.Errorf("profile must configure known_hosts or host_key_sha256")
}

func formatSSHOutput(stdout, stderr string, exitCode int) string {
	parts := []string{fmt.Sprintf("exit_code: %d", exitCode)}
	if strings.TrimSpace(stdout) != "" {
		parts = append(parts, "stdout:\n"+stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		parts = append(parts, "stderr:\n"+stderr)
	}
	return strings.Join(parts, "\n")
}

func sshFailure(call domain.ToolCall, message string, details map[string]any) domain.ToolExecution {
	return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: strings.TrimSpace(message), Details: details, IsError: true}
}
