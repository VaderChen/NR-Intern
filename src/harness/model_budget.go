package harness

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"errors"
	"strings"
)

// 所有 Run 內的推理（含摘要與備援）共用帳本。Provider 未回報的用量只用於
// 保守煞車，不冒充精確計費。串流限制是估算值，不能承諾上游停止計費的時點。
func streamWithBudget(ctx context.Context, model ports.Model, request domain.ModelRequest, sink ports.ModelEventSink, counter ports.TokenCounter) (domain.ModelResponse, error) {
	tracker, _ := ctx.Value(runBudgetContextKey{}).(*runBudgetTracker)
	if tracker == nil {
		return model.Stream(ctx, request, sink)
	}
	if tracker.tokensExceeded() != nil {
		return domain.ModelResponse{}, errRunBudgetTokens
	}
	input := counter.EstimateText(joinPromptSections(request.SystemPrompt, request.HostPrompt, request.ToolPrompt, request.PhasePrompt, request.ContextPrompt, request.UserPrompt)) + counter.EstimateMessages(request.History) + counter.EstimateTools(request.Tools)
	limit := tracker.budget.MaxTokens
	if limit > 0 && tracker.tokens+input >= limit {
		tracker.tokenStop = tracker.exceeded(domain.RunBudgetResourceTokens, int64(limit), int64(tracker.tokens+input))
		return domain.ModelResponse{}, errRunBudgetTokens
	}
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	streamed := domain.Usage{}
	output := budgetOutputEstimate{counter: counter}
	toolNames := map[int]string{}
	response, err := model.Stream(callCtx, request, func(event domain.ModelEvent) error {
		if event.Type == domain.ModelEventUsage && event.Usage != nil {
			streamed.Add(*event.Usage)
		}
		if observableBudgetOutput(event.Type) {
			if event.ToolCall != nil {
				previous := toolNames[event.ToolCall.Index]
				if event.ToolCall.Name != "" && previous != event.ToolCall.Name {
					output.append(strings.TrimPrefix(event.ToolCall.Name, previous))
					toolNames[event.ToolCall.Index] = event.ToolCall.Name
				}
			}
			output.append(event.Delta)
		}
		consumed := max(input+output.total(), streamed.Total())
		if limit > 0 && tracker.tokens+consumed >= limit {
			tracker.tokenStop = tracker.exceeded(domain.RunBudgetResourceTokens, int64(limit), int64(tracker.tokens+consumed))
			cancel()
			return errRunBudgetTokens
		}
		if sink != nil {
			return sink(event)
		}
		return nil
	})
	usage := response.Usage
	if streamed.Total() > usage.Total() {
		usage = streamed
	}
	tracker.addReportedUsage(usage)
	consumed := usage.Total()
	if consumed == 0 {
		estimatedOutput := output.total()
		if estimatedOutput == 0 {
			estimatedOutput = counter.EstimateMessages([]domain.Message{{Role: "assistant", Content: response.Content + response.Reasoning, ToolCalls: response.ToolCalls}})
		}
		consumed = input + estimatedOutput
	}
	tracker.tokens += consumed
	providerID, modelID := response.ProviderID, response.Model
	if providerID == "" {
		providerID = request.ProviderID
	}
	if modelID == "" {
		modelID = request.Model
	}
	if usage.Total() > 0 {
		key := providerID + "\x00" + modelID
		value := tracker.byModel[key]
		value.ProviderID, value.Model = providerID, modelID
		value.InputTokens += usage.InputTokens
		value.OutputTokens += usage.OutputTokens
		value.TotalTokens += usage.Total()
		tracker.byModel[key] = value
	}
	if tracker.tokenStop != nil {
		return response, errors.Join(errRunBudgetTokens, err)
	}
	return response, err
}

// 以固定字元區塊累計，避免每個網路片段各自進位；記憶體與單次估算成本有界。
// 分塊依字元數而非 event 邊界，同一輸出不因封包切割方式不同而大幅超計。
type budgetOutputEstimate struct {
	counter        ports.TokenCounter
	pending        strings.Builder
	runes, settled int
}

func (e *budgetOutputEstimate) append(value string) {
	for _, value := range value {
		e.pending.WriteRune(value)
		e.runes++
		if e.runes == 1024 {
			e.settled += e.counter.EstimateText(e.pending.String())
			e.pending.Reset()
			e.runes = 0
		}
	}
}

func (e *budgetOutputEstimate) total() int {
	return e.settled + e.counter.EstimateText(e.pending.String())
}

func observableBudgetOutput(eventType string) bool {
	return eventType == domain.ModelEventTextDelta || eventType == domain.ModelEventThinkingDelta || eventType == domain.ModelEventToolCallDelta
}
