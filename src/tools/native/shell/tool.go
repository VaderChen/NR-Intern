package shell

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type Tool struct {
	MaxOutputBytes int
	MaxTimeout     time.Duration
}

func New(maxOutputBytes int, maxTimeout time.Duration) *Tool {
	if maxOutputBytes <= 0 {
		maxOutputBytes = 512 * 1024
	}
	if maxTimeout <= 0 {
		maxTimeout = 30 * time.Minute
	}
	return &Tool{MaxOutputBytes: maxOutputBytes, MaxTimeout: maxTimeout}
}

func (t *Tool) Definition() domain.ToolDefinition {
	adapter := currentPlatformAdapter()
	return domain.ToolDefinition{
		Name:               "shell_exec",
		Label:              "執行命令",
		Version:            "1.0.0",
		Category:           "system",
		Description:        "在 Project／Session Sandbox 內執行命令。shell 模式會依作業系統選擇命令處理器；direct 模式直接執行程式與參數。",
		Platforms:          []string{"darwin", "linux", "windows"},
		Capabilities:       []string{"shell", "direct-exec", "timeout", "process-tree-cancel", "bounded-output", "workspace-cwd", "safe-environment"},
		RequiresPermission: true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"mode":            map[string]any{"type": "string", "enum": []string{"shell", "direct"}, "default": "shell"},
				"command":         map[string]any{"type": "string", "description": "shell script，或 direct 模式的程式名稱"},
				"args":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "只供 direct 模式使用"},
				"shell":           map[string]any{"type": "string", "default": "auto", "description": "目前平台預設為 " + adapter.DefaultShell()},
				"working_dir":     map[string]any{"type": "string", "default": "."},
				"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": int(t.MaxTimeout.Seconds()), "default": 120},
				"env":             map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			},
			"required": []string{"command"},
		},
	}
}

func (t *Tool) Execute(ctx context.Context, invocation tools.Invocation, sink ports.ToolUpdateSink) (domain.ToolExecution, error) {
	arguments := invocation.Call.Arguments
	sandboxRoots := invocation.SandboxRoots()
	commandText := toolutil.String(arguments, "command")
	if commandText == "" {
		return shellFailure(invocation.Call, "command is required", nil), nil
	}
	workingDirectory, err := toolutil.ResolvePathInRoots(sandboxRoots, toolutil.String(arguments, "working_dir"), true)
	if err != nil {
		return shellFailure(invocation.Call, err.Error(), nil), nil
	}
	info, err := os.Stat(workingDirectory)
	if err != nil || !info.IsDir() {
		return shellFailure(invocation.Call, "working_dir is not a directory", nil), nil
	}
	timeoutSeconds := toolutil.Int(arguments, "timeout_seconds", 120, 1, int(t.MaxTimeout.Seconds()))
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	mode := strings.ToLower(toolutil.String(arguments, "mode"))
	if mode == "" {
		mode = "shell"
	}
	adapter := currentPlatformAdapter()
	var command *exec.Cmd
	switch mode {
	case "shell":
		command, err = adapter.ShellCommand(toolutil.String(arguments, "shell"), commandText)
	case "direct":
		command = exec.Command(commandText, toolutil.StringSlice(arguments, "args")...)
	default:
		err = fmt.Errorf("unsupported execution mode %q", mode)
	}
	if err != nil {
		return shellFailure(invocation.Call, err.Error(), nil), nil
	}
	command.Dir = workingDirectory
	command.Env = mergeEnvironment(safeEnvironment(os.Environ()), arguments["env"])
	adapter.Prepare(command)
	stdout := toolutil.NewLimitedBuffer(t.MaxOutputBytes)
	stderr := toolutil.NewLimitedBuffer(t.MaxOutputBytes)
	command.Stdout = stdout
	command.Stderr = stderr
	startedAt := time.Now().UTC()
	if err := command.Start(); err != nil {
		return shellFailure(invocation.Call, err.Error(), nil), nil
	}
	group := adapter.Attach(command)
	defer group.Close()
	if sink != nil {
		_ = sink(domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Details: map[string]any{"phase": "process_started", "mode": mode, "process_group": group.Name()}})
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	var runErr error
	var terminationErr error
	select {
	case runErr = <-done:
	case <-runCtx.Done():
		terminationErr = group.Terminate()
		runErr = <-done
	}
	duration := time.Since(startedAt)
	exitCode := -1
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	}
	details := map[string]any{
		"mode":          mode,
		"working_dir":   toolutil.DisplayPathInRoots(sandboxRoots, workingDirectory),
		"exit_code":     exitCode,
		"duration_ms":   duration.Milliseconds(),
		"stdout":        stdout.String(),
		"stderr":        stderr.String(),
		"stdout_cut":    stdout.Truncated(),
		"stderr_cut":    stderr.Truncated(),
		"timed_out":     errors.Is(runCtx.Err(), context.DeadlineExceeded),
		"process_group": group.Name(),
	}
	if terminationErr != nil && !errors.Is(terminationErr, os.ErrProcessDone) {
		details["termination_error"] = terminationErr.Error()
	}
	content := formatProcessOutput(stdout.String(), stderr.String(), exitCode)
	if runErr != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			content = fmt.Sprintf("command timed out after %d seconds\n%s", timeoutSeconds, content)
		} else if errors.Is(runCtx.Err(), context.Canceled) {
			return domain.ToolExecution{}, runCtx.Err()
		} else {
			content = strings.TrimSpace(runErr.Error() + "\n" + content)
		}
		return shellFailure(invocation.Call, content, details), nil
	}
	return domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Content: content, Details: details}, nil
}

// safeEnvironment 只繼承執行程式必要的作業系統環境，避免把 LLM API key、
// backend token 等服務秘密無條件傳入模型可呼叫的子程序。
func safeEnvironment(base []string) []string {
	allowed := map[string]struct{}{
		"PATH": {}, "HOME": {}, "USER": {}, "LOGNAME": {}, "SHELL": {},
		"TMPDIR": {}, "TMP": {}, "TEMP": {}, "LANG": {},
		"SYSTEMROOT": {}, "COMSPEC": {}, "PATHEXT": {}, "USERPROFILE": {},
	}
	result := make([]string, 0, len(base))
	for _, item := range base {
		index := strings.IndexByte(item, '=')
		if index <= 0 {
			continue
		}
		key := strings.ToUpper(item[:index])
		if _, ok := allowed[key]; ok || strings.HasPrefix(key, "LC_") {
			result = append(result, item)
		}
	}
	return result
}

func mergeEnvironment(base []string, raw any) []string {
	values := map[string]string{}
	for _, item := range base {
		if index := strings.IndexByte(item, '='); index > 0 {
			values[item[:index]] = item[index+1:]
		}
	}
	if additions, ok := raw.(map[string]any); ok {
		for key, value := range additions {
			key = strings.TrimSpace(key)
			if key != "" && !strings.Contains(key, "=") {
				values[key] = fmt.Sprint(value)
			}
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func formatProcessOutput(stdout, stderr string, exitCode int) string {
	parts := []string{fmt.Sprintf("exit_code: %d", exitCode)}
	if strings.TrimSpace(stdout) != "" {
		parts = append(parts, "stdout:\n"+stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		parts = append(parts, "stderr:\n"+stderr)
	}
	return strings.Join(parts, "\n")
}

func shellFailure(call domain.ToolCall, message string, details map[string]any) domain.ToolExecution {
	return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: strings.TrimSpace(message), Details: details, IsError: true}
}
