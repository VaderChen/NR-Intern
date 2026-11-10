package harness

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTruncationRecoverySmoke(t *testing.T) {
	for _, mode := range []ToolCallMode{ToolCallModeNative, ToolCallModeInstruction} {
		for _, recover := range []bool{true, false} {
			t.Run(string(mode)+map[bool]string{true: "/recovered", false: "/exhausted"}[recover], func(t *testing.T) {
				truncated := domain.ModelResponse{StopReason: "length", Content: `{"type":"tool_use","tool":"mutate","input":{}}`, ToolCalls: []domain.ToolCall{{ID: "incomplete", Name: "mutate"}}, Usage: domain.Usage{OutputTokens: 1024}}
				last := truncated
				if recover {
					last = domain.ModelResponse{Content: "簡短且完整的回答", StopReason: "stop"}
				}
				model := &scriptedModel{responses: []domain.ModelResponse{truncated, last}}
				sessions := newMemorySessions(testSession())
				tools := &fakeTools{definitions: []domain.ToolDefinition{{Name: "mutate"}}, execute: func(context.Context, domain.Session, domain.ToolCall) (domain.ToolExecution, error) {
					t.Fatal("截斷的工具不應執行")
					return domain.ToolExecution{}, nil
				}}
				runner := newTestRunner(sessions, model, tools)
				runner.Logger = logging.Discard()
				runner.ToolCallMode = mode
				emptyRetries, ended := 0, 0
				result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "執行工作"}, func(e domain.EngineEvent) error {
					if e.Type == "run.empty_answer_retry" {
						emptyRetries++
					}
					if e.Type == "message.end" {
						if message, ok := e.Payload["message"].(domain.Message); ok && message.Role == "assistant" {
							ended++
						}
					}
					return nil
				})
				if recover && (err != nil || result.Message.Content != "簡短且完整的回答") {
					t.Fatalf("無法恢復：%+v %v", result, err)
				}
				if !recover && (err == nil || !strings.Contains(err.Error(), "上限") || strings.Contains(err.Error(), "neither text")) {
					t.Fatalf("未保留截斷原因：%v", err)
				}
				if len(model.requests) != 2 || emptyRetries != 0 || ended != 2 {
					t.Fatalf("次數或收尾錯誤：requests=%d empty=%d ends=%d", len(model.requests), emptyRetries, ended)
				}
				request := model.requests[1]
				if !strings.Contains(request.SystemPrompt+request.HostPrompt+request.PhasePrompt+request.ContextPrompt, "不可重做") {
					t.Fatal("缺少有界恢復指令")
				}
				messages, _ := sessions.ListMessages(context.Background(), testSession().ID)
				for _, m := range messages {
					if m.StopReason == "length" && (len(m.ToolCalls) > 0 || m.Continuation != nil || m.Metadata["internal"] != true) {
						t.Fatal("截斷訊息未隔離")
					}
				}
				if _, err := sessions.LatestEntryOfType(context.Background(), testSession().ID, domain.SessionEntryTurnFinished); err != nil {
					t.Fatal("缺少回合收尾")
				}
			})
		}
	}
}

func TestTruncationRecoveryCancellationSmoke(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := &scriptedModel{responses: []domain.ModelResponse{{StopReason: "length"}}}
	runner := newTestRunner(newMemorySessions(testSession()), model, &fakeTools{})
	runner.Logger = logging.Discard()
	_, err := runner.Run(ctx, Input{Session: testSession(), UserInput: "工作"}, func(e domain.EngineEvent) error {
		if e.Type == "agent.progress" {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, domain.ErrCanceled) || len(model.requests) != 1 {
		t.Fatalf("取消後仍重新請求：%v requests=%d", err, len(model.requests))
	}
}
