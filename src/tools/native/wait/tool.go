package wait

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"context"
	"fmt"
	"time"
)

const defaultDurationSeconds = 5

// Tool 提供不執行任何命令的可取消等待，讓 Agent 能處理非同步上傳、部署或
// 外部服務啟動，而不必把 sleep 指令交給目前作業系統的 Shell。
type Tool struct {
	maxDuration time.Duration
}

func New(maxDuration time.Duration) *Tool {
	if maxDuration <= 0 {
		maxDuration = 30 * time.Minute
	}
	return &Tool{maxDuration: maxDuration}
}

func (t *Tool) Definition() domain.ToolDefinition {
	maxSeconds := int(t.maxDuration.Seconds())
	if maxSeconds < 1 {
		maxSeconds = 1
	}
	return domain.ToolDefinition{
		Name:         "wait_for",
		Label:        "等待",
		Version:      "1.0.0",
		Category:     "system",
		Description:  "不執行命令，只等待指定秒數並回報可取消的進度。適合非同步上傳、部署或服務啟動；等待後仍必須使用檢查工具確認結果，不得把等待本身當成完成證據。",
		Platforms:    []string{"darwin", "linux", "windows"},
		Capabilities: []string{"wait", "progress", "cancellation", "bounded-duration"},
		ReadOnly:     true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"duration_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": maxSeconds, "default": defaultDurationSeconds, "description": "等待秒數；完成後仍須重新檢查目標狀態"},
				"reason":           map[string]any{"type": "string", "maxLength": 240, "description": "等待原因，供進度顯示使用"},
			},
			"required": []string{"duration_seconds"},
		},
	}
}

func (t *Tool) Execute(ctx context.Context, invocation tools.Invocation, sink ports.ToolUpdateSink) (domain.ToolExecution, error) {
	maxSeconds := int(t.maxDuration.Seconds())
	if maxSeconds < 1 {
		maxSeconds = 1
	}
	durationSeconds := toolutil.Int(invocation.Call.Arguments, "duration_seconds", defaultDurationSeconds, 1, maxSeconds)
	reason := toolutil.String(invocation.Call.Arguments, "reason")
	duration := time.Duration(durationSeconds) * time.Second

	startedAt := time.Now()
	if sink != nil {
		_ = sink(domain.ToolExecution{
			ToolCallID: invocation.Call.ID,
			ToolName:   invocation.Call.Name,
			Details: map[string]any{
				"phase":            "wait_started",
				"duration_seconds": durationSeconds,
				"reason":           reason,
			},
		})
	}

	timer := time.NewTimer(duration)
	defer timer.Stop()
	progressInterval := 5 * time.Second
	if duration < progressInterval {
		progressInterval = duration
	}
	progressTicker := time.NewTicker(progressInterval)
	defer progressTicker.Stop()
	for {
		select {
		case <-timer.C:
			elapsed := time.Since(startedAt)
			return domain.ToolExecution{
				ToolCallID: invocation.Call.ID,
				ToolName:   invocation.Call.Name,
				Content:    fmt.Sprintf("waited for %d second(s)", durationSeconds),
				Details: map[string]any{
					"phase":            "wait_completed",
					"duration_seconds": durationSeconds,
					"elapsed_ms":       elapsed.Milliseconds(),
					"reason":           reason,
				},
			}, nil
		case <-progressTicker.C:
			elapsed := time.Since(startedAt)
			remaining := duration - elapsed
			if remaining < 0 {
				remaining = 0
			}
			if sink != nil {
				_ = sink(domain.ToolExecution{
					ToolCallID: invocation.Call.ID,
					ToolName:   invocation.Call.Name,
					Details: map[string]any{
						"phase":            "waiting",
						"duration_seconds": durationSeconds,
						"elapsed_ms":       elapsed.Milliseconds(),
						"remaining_ms":     remaining.Milliseconds(),
						"reason":           reason,
					},
				})
			}
		case <-ctx.Done():
			return domain.ToolExecution{}, ctx.Err()
		}
	}
}
