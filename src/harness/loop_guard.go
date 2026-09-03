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
	// maxRepeatedIdenticalErrors 是不看內容的門檻：同一個目標連續得到同一個錯誤
	// 這麼多次，就算每次內容都不同也視為原地打轉。比策略門檻寬鬆，才留得住
	// 「模型真的修好了」的那幾次嘗試。
	maxRepeatedIdenticalErrors = 4
	maxBlockedCallsPerStrategy = 2
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
	// repeatedErrors 與 blockedErrors 不看內容，只看同一資源重複出現的相同錯誤。
	repeatedErrors map[string]int
	blockedErrors  map[string]string
	forcedReason   string
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
		repeatedErrors:       map[string]int{},
		blockedErrors:        map[string]string{},
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
	attempt := mutationAttemptKey(definition, call)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.forcedReason == "" && g.successfulSignatures[signature] {
		g.forcedReason = fmt.Sprintf("模型重複要求已成功執行的副作用工具 %s，Harness 已略過重複操作。", call.Name)
	}
	if g.forcedReason == "" {
		if reason := g.blockedErrorReason(definition, call); reason != "" {
			return guardedToolFailure(call, reason), true
		}
	}
	if g.forcedReason == "" {
		blocked := g.blockedStrategies[attempt]
		if blocked == nil {
			blocked = g.blockedStrategies[strategy]
		}
		if blocked != nil {
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
		// 失敗要擋在哪一把鑰匙上，取決於錯誤在指責什麼：
		//   - 錯誤指名內容欄位（例如 cell_updates[0]: ...）→ 用含內容的 attempt key，
		//     模型改好內容之後就是不同的嘗試，必須放行。
		//   - 其他錯誤（例如「檔案已存在」）→ 用不含內容的 strategy key，
		//     換一份近似內容再送一次並不會讓結果不同，該擋。
		blockKey := strategy
		if payloadValidationFailure(call, result) {
			blockKey = mutationAttemptKey(definition, call)
		}
		if resource == "" || blockKey == "" {
			return
		}
		failureKey := blockKey + ":" + toolResultFingerprint(result.Content)
		g.mu.Lock()
		defer g.mu.Unlock()
		g.failureCounts[failureKey]++
		// 同一個資源一直得到同一個錯誤，就算每次內容都不同，也是在原地打轉。
		if errorKey := repeatedErrorKey(definition, call, result); errorKey != "" {
			g.repeatedErrors[errorKey]++
			if g.repeatedErrors[errorKey] >= maxRepeatedIdenticalErrors && g.blockedErrors[errorKey] == "" {
				g.blockedErrors[errorKey] = fmt.Sprintf("同一個目標連續 %d 次得到相同錯誤（%s）。換工具或換做法，不要只調整內容再送一次。",
					g.repeatedErrors[errorKey], firstLine(result.Content))
			}
		}
		if g.failureCounts[failureKey] >= maxRepeatedFailuresPerStrategy && g.blockedStrategies[blockKey] == nil {
			g.blockedStrategies[blockKey] = &blockedMutationStrategy{
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
// mutationStrategyKey 是「這次嘗試」的識別碼：工具、目標資源、控制參數，
// 再加上內容欄位的摘要。
//
// 內容欄位一度被排除在外，理由是「換一份近似內容重寫同一個檔案算同一個策略」。
// 但那讓 Harness 擋掉了模型真正的修正：實測模型把 cell 從 "Sheet1!A1" 改成
// {"cell":"A1","sheet":"Sheet1"}——完全正確的修法——卻被判成相同策略而不准執行，
// 接連兩次正確答案都被擋下，最後只好用 shell 寫出一個空殼檔案交差。
//
// 內容變了就是不同的嘗試。真正的原地打轉由 repeatedErrorKey 那條較粗的計數擋，
// 它不看內容、只看「同一個資源一直得到同一個錯誤」。
// blockedErrorReason 回報這個資源是不是已經因為重複相同錯誤而被擋下。
// 呼叫端已持有鎖。
func (g *toolLoopGuard) blockedErrorReason(definition domain.ToolDefinition, call domain.ToolCall) string {
	resource := mutationResource(definition, call.Arguments)
	if resource == "" {
		return ""
	}
	prefix := fmt.Sprintf("%s:%s:", strings.TrimSpace(call.Name), resource)
	for key, reason := range g.blockedErrors {
		if strings.HasPrefix(key, prefix) {
			return reason
		}
	}
	return ""
}

func firstLine(value string) string {
	trimmed := strings.TrimSpace(value)
	if index := strings.IndexAny(trimmed, "\n\r"); index > 0 {
		trimmed = trimmed[:index]
	}
	runes := []rune(trimmed)
	if len(runes) > 120 {
		return string(runes[:120]) + "…"
	}
	return trimmed
}

// mutationStrategyKey 用於「成功但沒有進展」的判定：同一個資源、同一組控制參數，
// 只是換一份近似內容反覆完整覆寫。內容刻意不進 key——那正是要偵測的行為。
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

// mutationPayloadFields 是「內容」類的參數名稱。
var mutationPayloadFields = []string{
	"annotations", "blocks", "body", "cell_updates", "content", "data",
	"new_content", "new_text", "patch", "payload", "replacement", "replacements", "sheets", "slides",
}

// payloadValidationFailure 回報這次失敗是不是在指責內容本身。
//
// 工具驗證內容時會把欄位名稱寫進錯誤（cell_updates[0]: cell "Sheet1!A1" is not a
// valid reference）。這種錯誤改內容就能修好，因此不能用「內容不算」的策略鑰匙去擋。
func payloadValidationFailure(call domain.ToolCall, result domain.ToolExecution) bool {
	lower := strings.ToLower(result.Content)
	if lower == "" {
		return false
	}
	for _, field := range mutationPayloadFields {
		if _, present := call.Arguments[field]; !present {
			continue
		}
		if strings.Contains(lower, field) {
			return true
		}
	}
	return false
}

// mutationAttemptKey 用於「重複失敗」的判定，內容欄位要算進去。
//
// 失敗端一度沿用 mutationStrategyKey（內容不算），結果是 Harness 擋掉了模型真正的
// 修正：實測模型把 cell 從 "Sheet1!A1" 改成 {"cell":"A1","sheet":"Sheet1"}——完全
// 正確的修法——卻被判成「相同策略」不准執行，連兩次正確答案都被擋下，最後只好用
// shell 寫出一個空殼檔案交差。內容變了就是不同的嘗試，該給它跑。
func mutationAttemptKey(definition domain.ToolDefinition, call domain.ToolCall) string {
	resource := mutationResource(definition, call.Arguments)
	if resource == "" {
		return ""
	}
	encoded, _ := json.Marshal(sortedArgumentDigestSource(call.Arguments))
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%s:%s:attempt:%x", strings.TrimSpace(call.Name), resource, digest)
}

// repeatedErrorKey 不看內容，只看「同一個工具對同一個資源一直得到同一個錯誤」。
// 這條用來擋真正的原地打轉：每次都換一點內容、但錯誤訊息一字不差。
func repeatedErrorKey(definition domain.ToolDefinition, call domain.ToolCall, result domain.ToolExecution) string {
	resource := mutationResource(definition, call.Arguments)
	if resource == "" {
		return ""
	}
	return fmt.Sprintf("%s:%s:%s", strings.TrimSpace(call.Name), resource, toolResultFingerprint(result.Content))
}

// sortedArgumentDigestSource 讓摘要與 map 走訪順序無關。
func sortedArgumentDigestSource(arguments map[string]any) []any {
	keys := make([]string, 0, len(arguments))
	for key := range arguments {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]any, 0, len(keys)*2)
	for _, key := range keys {
		encoded, _ := json.Marshal(arguments[key])
		values = append(values, key, string(encoded))
	}
	return values
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
