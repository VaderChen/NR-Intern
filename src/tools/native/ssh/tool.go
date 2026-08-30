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

type remoteCommandResult struct {
	stdout             string
	stderr             string
	stdoutCut          bool
	stderrCut          bool
	exitCode           int
	runErr             error
	exitError          bool
	connectionAttempts int
	duration           time.Duration
	timedOut           bool
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
	result, err := t.executeRemoteCommand(runCtx, invocation, profileName, profile, commandText, sink)
	if err != nil {
		return domain.ToolExecution{}, err
	}
	details := remoteCommandDetails(profileName, result)
	content := formatSSHOutput(result.stdout, result.stderr, result.exitCode)
	if result.runErr != nil {
		if errors.Is(result.runErr, context.Canceled) && !result.timedOut {
			return domain.ToolExecution{}, result.runErr
		}
		content = strings.TrimSpace(result.runErr.Error() + "\n" + content)
		return sshFailure(invocation.Call, content, details), nil
	}
	return domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Content: content, Details: details}, nil
}

func (t *Tool) executeRemoteCommand(ctx context.Context, invocation tools.Invocation, profileName string, profile Profile, commandText string, sink ports.ToolUpdateSink) (remoteCommandResult, error) {
	startedAt := time.Now()
	client, attempts, err := dialWithRetry(ctx, profile, func(attempt int) {
		if sink != nil {
			_ = sink(domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Details: map[string]any{"phase": "ssh_connecting", "profile": profileName, "attempt": attempt}})
		}
	})
	if err != nil {
		if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return remoteCommandResult{}, ctx.Err()
		}
		return remoteCommandResult{
			exitCode:           -1,
			runErr:             fmt.Errorf("connect SSH profile %q after %d attempt(s): %w", profileName, attempts, err),
			connectionAttempts: attempts,
			duration:           time.Since(startedAt),
			timedOut:           errors.Is(ctx.Err(), context.DeadlineExceeded),
		}, nil
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return remoteCommandResult{
			exitCode:           -1,
			runErr:             fmt.Errorf("create SSH session: %w", err),
			connectionAttempts: attempts,
			duration:           time.Since(startedAt),
		}, nil
	}
	defer session.Close()
	stdout := toolutil.NewLimitedBuffer(t.maxOutputBytes)
	stderr := toolutil.NewLimitedBuffer(t.maxOutputBytes)
	session.Stdout = stdout
	session.Stderr = stderr
	if sink != nil {
		_ = sink(domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Details: map[string]any{"phase": "ssh_connected", "profile": profileName}})
	}
	done := make(chan error, 1)
	go func() { done <- session.Run(commandText) }()
	keepAliveCtx, stopKeepAlive := context.WithCancel(ctx)
	defer stopKeepAlive()
	keepAliveErrors := make(chan error, 1)
	go keepAlive(keepAliveCtx, client, profile, keepAliveErrors)
	var runErr error
	select {
	case <-ctx.Done():
		_ = session.Close()
		runErr = ctx.Err()
	case runErr = <-done:
	case keepAliveErr := <-keepAliveErrors:
		_ = session.Close()
		_ = client.Close()
		<-done
		runErr = fmt.Errorf("SSH connection lost during command; command was not retried to avoid duplicate side effects: %w", keepAliveErr)
	}
	exitCode := 0
	var exitErr *gossh.ExitError
	exitError := false
	if errors.As(runErr, &exitErr) {
		exitCode = exitErr.ExitStatus()
		exitError = true
	} else if runErr != nil {
		exitCode = -1
	}
	return remoteCommandResult{
		stdout:             stdout.String(),
		stderr:             stderr.String(),
		stdoutCut:          stdout.Truncated(),
		stderrCut:          stderr.Truncated(),
		exitCode:           exitCode,
		runErr:             runErr,
		exitError:          exitError,
		connectionAttempts: attempts,
		duration:           time.Since(startedAt),
		timedOut:           errors.Is(ctx.Err(), context.DeadlineExceeded),
	}, nil
}

func remoteCommandDetails(profileName string, result remoteCommandResult) map[string]any {
	return map[string]any{
		"profile":             profileName,
		"exit_code":           result.exitCode,
		"duration_ms":         result.duration.Milliseconds(),
		"stdout":              result.stdout,
		"stderr":              result.stderr,
		"stdout_cut":          result.stdoutCut,
		"stderr_cut":          result.stderrCut,
		"timed_out":           result.timedOut,
		"connection_attempts": result.connectionAttempts,
	}
}

// WaitTool 只重複執行明確的唯讀檢查命令，不會重跑上傳或部署命令。
// 每次檢查都重新建立 SSH session，讓短暫斷線或遠端檔案尚未完成時能安全輪詢。
type WaitTool struct {
	*Tool
}

func NewWaitTool(profiles map[string]Profile, maxOutputBytes int, maxTimeout time.Duration) *WaitTool {
	return &WaitTool{Tool: New(profiles, maxOutputBytes, maxTimeout)}
}

func (t *WaitTool) Definition() domain.ToolDefinition {
	maxSeconds := int(t.maxTimeout.Seconds())
	if maxSeconds < 1 {
		maxSeconds = 1
	}
	defaultTimeoutSeconds := 120
	if defaultTimeoutSeconds > maxSeconds {
		defaultTimeoutSeconds = maxSeconds
	}
	defaultCheckTimeoutSeconds := 30
	if defaultCheckTimeoutSeconds > maxSeconds {
		defaultCheckTimeoutSeconds = maxSeconds
	}
	return domain.ToolDefinition{
		Name:               "ssh_wait",
		Label:              "等待 SSH 狀態",
		Version:            "1.0.0",
		Category:           "remote",
		Description:        "以後端預先設定的 SSH profile 輪詢唯讀狀態命令，直到遠端檢查成功、輸出符合條件或逾時。適合確認非同步上傳、檔案大小／雜湊或服務就緒；command 必須是冪等且唯讀的檢查命令，不可放入部署、上傳、刪除或其他副作用。每次輪詢都重新建立 SSH session。",
		Platforms:          []string{"darwin", "linux", "windows"},
		Capabilities:       []string{"ssh", "polling", "remote-readiness-check", "host-key-verification", "profile-credentials", "connect-retry", "keepalive", "timeout", "bounded-output"},
		ReadOnly:           true,
		RequiresPermission: true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"profile":               map[string]any{"type": "string", "description": "後端管理者設定的 SSH profile 名稱"},
				"command":               map[string]any{"type": "string", "description": "只讀、冪等的遠端狀態檢查命令；不可放部署或上傳命令"},
				"timeout_seconds":       map[string]any{"type": "integer", "minimum": 1, "maximum": maxSeconds, "default": defaultTimeoutSeconds, "description": "整體輪詢逾時"},
				"interval_seconds":      map[string]any{"type": "integer", "minimum": 1, "maximum": 300, "default": 5, "description": "檢查失敗後再次輪詢前的等待秒數"},
				"check_timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": maxSeconds, "default": defaultCheckTimeoutSeconds, "description": "單次 SSH 檢查逾時"},
				"expected_exit_code":    map[string]any{"type": "integer", "minimum": -255, "maximum": 255, "default": 0},
				"output_contains":       map[string]any{"type": "string", "maxLength": 4096, "description": "stdout 必須包含的文字"},
				"output_equals":         map[string]any{"type": "string", "maxLength": 4096, "description": "stdout 去除首尾空白後必須完全相等的文字"},
				"stable_checks":         map[string]any{"type": "integer", "minimum": 1, "maximum": 5, "default": 1, "description": "條件連續符合幾次才算成功"},
			},
			"required": []string{"profile", "command"},
		},
	}
}

func (t *WaitTool) Execute(ctx context.Context, invocation tools.Invocation, sink ports.ToolUpdateSink) (domain.ToolExecution, error) {
	profileName := toolutil.String(invocation.Call.Arguments, "profile")
	commandText := toolutil.String(invocation.Call.Arguments, "command")
	if profileName == "" || commandText == "" {
		return sshFailure(invocation.Call, "profile and command are required", nil), nil
	}
	profile, exists := t.profiles[profileName]
	if !exists {
		return sshFailure(invocation.Call, "SSH profile is unavailable", nil), nil
	}
	maxSeconds := int(t.maxTimeout.Seconds())
	if maxSeconds < 1 {
		maxSeconds = 1
	}
	defaultTimeoutSeconds := 120
	if defaultTimeoutSeconds > maxSeconds {
		defaultTimeoutSeconds = maxSeconds
	}
	timeoutSeconds := toolutil.Int(invocation.Call.Arguments, "timeout_seconds", defaultTimeoutSeconds, 1, maxSeconds)
	checkTimeoutMaximum := timeoutSeconds
	if checkTimeoutMaximum > maxSeconds {
		checkTimeoutMaximum = maxSeconds
	}
	defaultCheckTimeoutSeconds := 30
	if defaultCheckTimeoutSeconds > checkTimeoutMaximum {
		defaultCheckTimeoutSeconds = checkTimeoutMaximum
	}
	checkTimeoutSeconds := toolutil.Int(invocation.Call.Arguments, "check_timeout_seconds", defaultCheckTimeoutSeconds, 1, checkTimeoutMaximum)
	intervalSeconds := toolutil.Int(invocation.Call.Arguments, "interval_seconds", 5, 1, 300)
	expectedExitCode := toolutil.Int(invocation.Call.Arguments, "expected_exit_code", 0, -255, 255)
	outputContains := toolutil.String(invocation.Call.Arguments, "output_contains")
	outputEquals := toolutil.String(invocation.Call.Arguments, "output_equals")
	stableChecks := toolutil.Int(invocation.Call.Arguments, "stable_checks", 1, 1, 5)

	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	startedAt := time.Now()
	checkAttempts := 0
	totalConnectionAttempts := 0
	matchingChecks := 0
	var last remoteCommandResult
	haveLast := false
	for {
		if waitCtx.Err() != nil {
			return sshWaitTimeout(invocation.Call, profileName, timeoutSeconds, checkAttempts, totalConnectionAttempts, last, haveLast), nil
		}
		checkCtx, stopCheck := context.WithTimeout(waitCtx, time.Duration(checkTimeoutSeconds)*time.Second)
		result, err := t.executeRemoteCommand(checkCtx, invocation, profileName, profile, commandText, sink)
		stopCheck()
		if err != nil {
			if ctx.Err() != nil {
				return domain.ToolExecution{}, ctx.Err()
			}
			return domain.ToolExecution{}, err
		}
		checkAttempts++
		totalConnectionAttempts += result.connectionAttempts
		last = result
		haveLast = true
		matched := remoteCheckMatches(result, expectedExitCode, outputContains, outputEquals)
		if matched {
			matchingChecks++
		} else {
			matchingChecks = 0
		}
		if sink != nil {
			_ = sink(domain.ToolExecution{
				ToolCallID: invocation.Call.ID,
				ToolName:   invocation.Call.Name,
				Details: map[string]any{
					"phase":           "ssh_wait_check",
					"profile":         profileName,
					"check_attempt":   checkAttempts,
					"matched":         matched,
					"matching_checks": matchingChecks,
					"exit_code":       result.exitCode,
					"elapsed_ms":      time.Since(startedAt).Milliseconds(),
				},
			})
		}
		if matchingChecks >= stableChecks {
			details := remoteCommandDetails(profileName, result)
			details["check_attempts"] = checkAttempts
			details["total_connection_attempts"] = totalConnectionAttempts
			details["expected_exit_code"] = expectedExitCode
			details["output_contains_matched"] = outputContains != ""
			details["output_equals_matched"] = outputEquals != ""
			details["stable_checks"] = stableChecks
			details["polling_elapsed_ms"] = time.Since(startedAt).Milliseconds()
			return domain.ToolExecution{
				ToolCallID: invocation.Call.ID,
				ToolName:   invocation.Call.Name,
				Content:    fmt.Sprintf("SSH check succeeded after %d check(s)\n%s", checkAttempts, formatRemoteCommandResult(result)),
				Details:    details,
			}, nil
		}
		timer := time.NewTimer(time.Duration(intervalSeconds) * time.Second)
		select {
		case <-timer.C:
		case <-waitCtx.Done():
			timer.Stop()
			return sshWaitTimeout(invocation.Call, profileName, timeoutSeconds, checkAttempts, totalConnectionAttempts, last, haveLast), nil
		}
	}
}

func remoteCheckMatches(result remoteCommandResult, expectedExitCode int, outputContains, outputEquals string) bool {
	if (result.runErr != nil && !result.exitError) || result.exitCode != expectedExitCode {
		return false
	}
	output := strings.TrimSpace(result.stdout)
	if outputContains != "" && !strings.Contains(output, outputContains) {
		return false
	}
	if outputEquals != "" && output != outputEquals {
		return false
	}
	return true
}

func formatRemoteCommandResult(result remoteCommandResult) string {
	content := formatSSHOutput(result.stdout, result.stderr, result.exitCode)
	if result.runErr != nil && !result.exitError {
		content = strings.TrimSpace(result.runErr.Error() + "\n" + content)
	}
	return content
}

func sshWaitTimeout(call domain.ToolCall, profileName string, timeoutSeconds, checkAttempts, totalConnectionAttempts int, last remoteCommandResult, haveLast bool) domain.ToolExecution {
	details := map[string]any{
		"profile":                   profileName,
		"timed_out":                 true,
		"check_attempts":            checkAttempts,
		"total_connection_attempts": totalConnectionAttempts,
		"timeout_seconds":           timeoutSeconds,
		"last_check":                haveLast,
	}
	content := fmt.Sprintf("SSH check timed out after %d second(s) and %d check(s)", timeoutSeconds, checkAttempts)
	if haveLast {
		details["last_exit_code"] = last.exitCode
		details["last_stdout"] = last.stdout
		details["last_stderr"] = last.stderr
		details["last_check_timed_out"] = last.timedOut
		details["last_connection_attempts"] = last.connectionAttempts
		content += "\nlast check:\n" + formatRemoteCommandResult(last)
	}
	return sshFailure(call, content, details)
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
