package harness

import (
	"AgenticService/src/domain"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const (
	maxSuccessfulMutationsPerStrategy = 2
	maxRepeatedFailuresPerStrategy    = 2
	maxBlockedCallsPerStrategy        = 2
)

type blockedMutationStrategy struct {
	reason       string
	blockedCalls int
}

// toolLoopGuard 防止模型在沒有新進展時重複執行副作用工具。它不解讀自然語言，
// 只使用工具定義、結構化參數與實際成功結果，因此對 Provider 與語言無關。
type toolLoopGuard struct {
	mu                   sync.Mutex
	definitions          map[string]domain.ToolDefinition
	successfulSignatures map[string]bool
	strategySuccesses    map[string]int
	mutationSummaries    map[string]string
	failureCounts        map[string]int
	blockedStrategies    map[string]*blockedMutationStrategy
	forcedReason         string
}

func newToolLoopGuard(definitions []domain.ToolDefinition) *toolLoopGuard {
	byName := make(map[string]domain.ToolDefinition, len(definitions))
	for _, definition := range definitions {
		byName[strings.TrimSpace(definition.Name)] = definition
	}
	return &toolLoopGuard{
		definitions:          byName,
		successfulSignatures: map[string]bool{},
		strategySuccesses:    map[string]int{},
		mutationSummaries:    map[string]string{},
		failureCounts:        map[string]int{},
		blockedStrategies:    map[string]*blockedMutationStrategy{},
	}
}

func (g *toolLoopGuard) before(call domain.ToolCall) (domain.ToolExecution, bool) {
	if g == nil {
		return domain.ToolExecution{}, false
	}
	definition, exists := g.definitions[strings.TrimSpace(call.Name)]
	if !exists || definition.ReadOnly {
		return domain.ToolExecution{}, false
	}
	signature := toolCallSignature(call)
	strategy := mutationStrategyKey(definition, call)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.forcedReason == "" && g.successfulSignatures[signature] {
		g.forcedReason = fmt.Sprintf("模型重複要求已成功執行的副作用工具 %s，Harness 已略過重複操作。", call.Name)
	}
	if g.forcedReason == "" && strategy != "" {
		if blocked := g.blockedStrategies[strategy]; blocked != nil {
			blocked.blockedCalls++
			if blocked.blockedCalls >= maxBlockedCallsPerStrategy {
				g.forcedReason = fmt.Sprintf("同一資源與操作策略在重複失敗後仍被要求執行 %d 次，Harness 判定工作未收斂。", blocked.blockedCalls)
			}
			return guardedToolFailure(call, blocked.reason), true
		}
	}
	if g.forcedReason == "" {
		return domain.ToolExecution{}, false
	}
	return guardedToolResult(call, g.forcedReason), true
}

func (g *toolLoopGuard) observe(call domain.ToolCall, result domain.ToolExecution) {
	if g == nil {
		return
	}
	definition, exists := g.definitions[strings.TrimSpace(call.Name)]
	if !exists || definition.ReadOnly {
		return
	}
	resource := mutationResource(definition, call.Arguments)
	strategy := mutationStrategyKey(definition, call)
	if result.IsError {
		if resource == "" || strategy == "" {
			return
		}
		failureKey := strategy + ":" + toolResultFingerprint(result.Content)
		g.mu.Lock()
		defer g.mu.Unlock()
		g.failureCounts[failureKey]++
		if g.failureCounts[failureKey] >= maxRepeatedFailuresPerStrategy && g.blockedStrategies[strategy] == nil {
			g.blockedStrategies[strategy] = &blockedMutationStrategy{
				reason: fmt.Sprintf("同一資源使用相同操作策略已失敗 %d 次（最近結果：%s）。請改變控制參數或改用其他方法，不要再次提交相同策略。",
					g.failureCounts[failureKey], truncateLoopGuardText(result.Content, 240)),
			}
		}
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.successfulSignatures[toolCallSignature(call)] = true
	if resource == "" {
		return
	}
	g.mutationSummaries[resource] = fmt.Sprintf("%s → %s：%s",
		strings.TrimSpace(call.Name), strings.TrimPrefix(resource, "atomic-resource:"), truncateLoopGuardText(result.Content, 400))
	if strategy == "" {
		return
	}
	g.strategySuccesses[strategy]++
	if g.strategySuccesses[strategy] >= maxSuccessfulMutationsPerStrategy && g.forcedReason == "" {
		g.forcedReason = fmt.Sprintf("同一資源已使用相同控制策略成功改寫 %d 次，Harness 判定操作開始重複並停止繼續改寫。", g.strategySuccesses[strategy])
	}
}

// successfulMutationSummary 回傳本次 Run 已確認成功的最新副作用結果。
// 收斂輪會把它當成不可否認的執行事實，避免 callable tools 被停用後，
// Provider 誤稱整個 Session 從未提供工具或檔案沒有更新。
func (g *toolLoopGuard) successfulMutationSummary() string {
	if g == nil {
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.mutationSummaries) == 0 {
		return ""
	}
	resources := make([]string, 0, len(g.mutationSummaries))
	for resource := range g.mutationSummaries {
		resources = append(resources, resource)
	}
	sort.Strings(resources)
	lines := make([]string, 0, len(resources))
	for _, resource := range resources {
		lines = append(lines, "- "+g.mutationSummaries[resource])
	}
	return strings.Join(lines, "\n")
}

func (g *toolLoopGuard) reason() string {
	if g == nil {
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.forcedReason
}

func toolCallSignature(call domain.ToolCall) string {
	encoded, _ := json.Marshal(call.Arguments)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%s:%x", strings.TrimSpace(call.Name), digest)
}

// mutationStrategyKey 刻意排除大型輸出本文，只保留工具、資源與控制參數。
// 模型只改寫內容但沒有修正 overwrite、前置條件等策略時，仍會被辨識為同一策略；
// 真正變更控制參數後則可繼續嘗試。這個規則以結構化欄位運作，與語言無關。
func mutationStrategyKey(definition domain.ToolDefinition, call domain.ToolCall) string {
	resource := mutationResource(definition, call.Arguments)
	if resource == "" {
		return ""
	}
	controls := make(map[string]any, len(call.Arguments))
	for key, value := range call.Arguments {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "annotations", "blocks", "body", "cell_updates", "content", "data", "new_content", "new_text", "patch", "payload", "replacement", "replacements", "sheets", "slides":
			continue
		default:
			controls[key] = value
		}
	}
	encoded, _ := json.Marshal(controls)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%s:%s:%x", strings.TrimSpace(call.Name), resource, digest)
}

func toolResultFingerprint(value string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(value), " "))
	digest := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", digest)
}

func truncateLoopGuardText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}

func mutationResource(definition domain.ToolDefinition, arguments map[string]any) string {
	if !hasCapability(definition.Capabilities, "atomic-replace") {
		return ""
	}
	for _, key := range []string{"output_path", "path", "target_path", "destination", "remote_path"} {
		if value, ok := arguments[key].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return "atomic-resource:" + value
			}
		}
	}
	return ""
}

func hasCapability(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), wanted) {
			return true
		}
	}
	return false
}

func guardedToolResult(call domain.ToolCall, reason string) domain.ToolExecution {
	return domain.ToolExecution{
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    "Harness 已略過這次工具呼叫：" + reason + " 請直接使用目前結果整理最終答案。",
		Details: map[string]any{
			"skipped":    true,
			"loop_guard": true,
		},
	}
}

func guardedToolFailure(call domain.ToolCall, reason string) domain.ToolExecution {
	return domain.ToolExecution{
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    "Harness 已略過這次重複失敗的工具呼叫：" + reason,
		IsError:    true,
		Details: map[string]any{
			"skipped":          true,
			"loop_guard":       true,
			"repeated_failure": true,
		},
	}
}
