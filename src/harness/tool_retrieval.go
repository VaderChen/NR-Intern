package harness

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"

	"AgenticService/src/domain"
)

const (
	// findToolsToolName 是檢索模式下的「找工具」入口：目錄裡沒列出的工具
	// 都靠它取回名稱與參數，因此它必須永遠存在，否則檢索失準就是死路。
	findToolsToolName = "find_tools"
	// mcpRetrievalThreshold 以下的可選工具整份公開。目錄小的時候 schema 成本
	// 本來就低，過濾只會多一層失準風險。
	mcpRetrievalThreshold = 12
	// mcpRetrievalLimit 是第一層過濾後保留的可選工具數。
	mcpRetrievalLimit = 8
	// findToolsResultLimit 是 find_tools 單次回傳的工具數。
	findToolsResultLimit = 6
	// findToolsSchemaLimit 是單一工具 schema 的字元上限，超過就只保留形狀。
	findToolsSchemaLimit = 1200
	// findToolsNameHintLimit 是查無結果時提示的工具名稱數量上限。
	findToolsNameHintLimit = 40
	// commonTermRatio 是「共通詞」的判定門檻：出現在這個比例以上的工具裡，
	// 這個詞就不再具有區分力，權重歸零。
	commonTermRatio = 0.6
)

// coreToolNames 永遠留在目錄裡，不受檢索影響。
//
// 這些是 Harness 自己的階段控制與最基本的探索能力：階段提示直接點名它們，
// 少了任何一個，模型會在「該用什麼」這件事上先卡一輪。它們數量固定且 schema
// 很小，留著的成本遠低於檢索失準的代價。
var coreToolNames = map[string]bool{
	systemShellToolName: true,
	waitToolName:        true,
	sshWaitToolName:     true,
	"file_read":         true,
	"directory_list":    true,
	"file_search":       true,
	"plan_get":          true,
	"plan_create":       true,
	"plan_step_update":  true,
}

// toolRetriever 是工具目錄的第一層過濾，內建工具與 MCP 工具都適用。
//
// 外掛型 MCP Server 動輒公開數十上百個工具，而工具定義（名稱、說明、JSON schema）
// 每一次請求都會整份送給模型：實測一個「HELLO」就送出 111,172 tokens，本地模型直接
// 卡死。真正的問題不是「哪些工具用不到」——使用者全部都要用——而是「這一輪用得到哪些」。
//
// 因此改成檢索：依這次的需求先挑出最相關的少數工具進入目錄，其餘工具仍然存在、
// 仍然可以呼叫，只是要透過 find_tools 取回。沒有任何工具被關掉。
//
// 內建工具同樣納入：擴充工具集打開後有近二十個內建工具，document_*、ssh_* 這些
// schema 都不小，而任何一次需求通常只會用到其中一兩個。核心工具（coreToolNames）
// 不參與檢索。
type toolRetriever struct {
	active  bool
	entries []toolIndexEntry
	idf     map[string]float64
	// known 是所有 MCP 工具，檢索沒命中也仍然可以被呼叫：讓模型多跑一輪去
	// 「重新找一次已經知道名字的工具」，就是把省下來的 token 又賠回去。
	known map[string]domain.ToolDefinition

	mu sync.Mutex
	// revealed 是本 run 已進入目錄的 MCP 工具：第一層命中的、探索取回的、
	// 以及實際呼叫過的。後續每一輪都會繼續公開，模型才能連續使用同一個工具。
	revealed map[string]bool
}

type toolIndexEntry struct {
	definition domain.ToolDefinition
	terms      map[string]float64
}

func newToolRetriever(definitions []domain.ToolDefinition, query string, enabled bool) *toolRetriever {
	retriever := &toolRetriever{
		known:    map[string]domain.ToolDefinition{},
		revealed: map[string]bool{},
	}
	for _, definition := range definitions {
		if retrievalExempt(definition.Name) {
			continue
		}
		retriever.known[definition.Name] = definition
		retriever.entries = append(retriever.entries, toolIndexEntry{
			definition: definition,
			terms:      indexToolTerms(definition),
		})
	}
	if !enabled || len(retriever.entries) <= mcpRetrievalThreshold {
		return retriever
	}
	retriever.active = true
	retriever.idf = inverseDocumentFrequency(retriever.entries)
	for _, definition := range retriever.search(query, mcpRetrievalLimit) {
		retriever.revealed[definition.Name] = true
	}
	return retriever
}

// enabled 回報這個 run 是否真的啟動了檢索（目錄夠小就不會）。
func (t *toolRetriever) enabled() bool { return t != nil && t.active }

// stage 從本輪目錄中移除尚未公開的工具，並補上 find_tools。
func (t *toolRetriever) stage(definitions []domain.ToolDefinition) []domain.ToolDefinition {
	if !t.enabled() {
		return definitions
	}
	result := make([]domain.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if !retrievalExempt(definition.Name) && !t.isRevealed(definition.Name) {
			continue
		}
		result = append(result, definition)
	}
	return append(result, findToolsDefinition(len(t.entries)))
}

// recognizable 是「允許被呼叫」的工具集合：本輪目錄再加上所有 MCP 工具。
// 目錄決定模型看得到什麼，這份集合決定執行端接不接受，兩者刻意不同。
func (t *toolRetriever) recognizable(active []domain.ToolDefinition) []domain.ToolDefinition {
	if !t.enabled() {
		return active
	}
	listed := make(map[string]bool, len(active))
	for _, definition := range active {
		listed[definition.Name] = true
	}
	result := make([]domain.ToolDefinition, 0, len(active)+len(t.entries))
	result = append(result, active...)
	for _, entry := range t.entries {
		if !listed[entry.definition.Name] {
			result = append(result, entry.definition)
		}
	}
	return result
}

// reveal 把工具加進目錄，讓後續回合看得到它。
func (t *toolRetriever) reveal(names ...string) {
	if !t.enabled() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, name := range names {
		if _, ok := t.known[name]; ok {
			t.revealed[name] = true
		}
	}
}

func (t *toolRetriever) isRevealed(name string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.revealed[name]
}

func (t *toolRetriever) revealedCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.revealed)
}

// search 依相關度排序回傳工具，分數為 0 的不回傳。
func (t *toolRetriever) search(query string, limit int) []domain.ToolDefinition {
	if t == nil || len(t.entries) == 0 || limit <= 0 {
		return nil
	}
	terms := map[string]bool{}
	for _, term := range tokenizeForRetrieval(query) {
		terms[term] = true
	}
	if len(terms) == 0 {
		return nil
	}
	idf := t.idf
	if idf == nil {
		idf = inverseDocumentFrequency(t.entries)
	}
	type scored struct {
		definition domain.ToolDefinition
		score      float64
	}
	matches := make([]scored, 0, len(t.entries))
	for _, entry := range t.entries {
		score := 0.0
		for term := range terms {
			if weight, ok := entry.terms[term]; ok {
				score += weight * idf[term]
			}
		}
		if score > 0 {
			matches = append(matches, scored{definition: entry.definition, score: score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].definition.Name < matches[j].definition.Name
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	result := make([]domain.ToolDefinition, 0, len(matches))
	for _, match := range matches {
		result = append(result, match.definition)
	}
	return result
}

// execute 處理 find_tools 呼叫，回傳工具名稱與呼叫參數，並把命中的工具
// 加進目錄，讓模型下一輪可以直接呼叫。
func (t *toolRetriever) execute(call domain.ToolCall) domain.ToolExecution {
	result := domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name}
	query := ""
	for _, key := range []string{"query", "keywords", "q", "text"} {
		if value, ok := call.Arguments[key]; ok {
			query = strings.TrimSpace(fmt.Sprint(value))
			if query != "" {
				break
			}
		}
	}
	if query == "" {
		result.Content = "find_tools 需要 query 參數，例如 {\"query\": \"製令 狀態\"}。"
		result.IsError = true
		return result
	}
	matches := t.search(query, findToolsResultLimit)
	if len(matches) == 0 {
		result.Content = fmt.Sprintf("找不到符合「%s」的工具。%s", query, t.nameHint())
		result.Details = map[string]any{"query": query, "matched": 0}
		return result
	}
	names := make([]string, 0, len(matches))
	payload := make([]map[string]any, 0, len(matches))
	for _, definition := range matches {
		names = append(names, definition.Name)
		entry := map[string]any{"name": definition.Name}
		if description := strings.TrimSpace(definition.Description); description != "" {
			entry["description"] = description
		}
		if schema := compactSchema(definition.InputSchema); schema != nil {
			entry["input_schema"] = schema
		}
		payload = append(payload, entry)
	}
	t.reveal(names...)
	encoded, err := json.Marshal(payload)
	if err != nil {
		result.Content = "工具目錄序列化失敗：" + err.Error()
		result.IsError = true
		return result
	}
	result.Content = "以下工具現在可以直接呼叫：\n" + string(encoded)
	result.Details = map[string]any{"query": query, "matched": len(matches), "tools": names}
	return result
}

func (t *toolRetriever) nameHint() string {
	names := make([]string, 0, len(t.entries))
	for _, entry := range t.entries {
		names = append(names, entry.definition.Name)
	}
	sort.Strings(names)
	total := len(names)
	if total == 0 {
		return "目前沒有可用的 MCP 工具。"
	}
	if total > findToolsNameHintLimit {
		names = names[:findToolsNameHintLimit]
		return fmt.Sprintf("可用工具共 %d 個，前 %d 個為：%s。請換關鍵字再找一次。", total, len(names), strings.Join(names, "、"))
	}
	return fmt.Sprintf("可用工具共 %d 個：%s。", total, strings.Join(names, "、"))
}

func findToolsDefinition(total int) domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:     findToolsToolName,
		Label:    "搜尋 MCP 工具",
		Category: "mcp",
		Description: fmt.Sprintf(
			"依關鍵字搜尋目錄未列出的工具（內建與 MCP 共 %d 個可用），回傳工具名稱與呼叫參數，取回後即可直接呼叫。目錄只列出與這次需求最相關的工具，需要其他能力或其他資料時先用這個搜尋，不要因為目錄沒有就回答做不到。",
			total,
		),
		ReadOnly: true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "關鍵字，可用中文，例如「製令 狀態」、「部門 人員」或「PDF 讀取」。",
				},
			},
			"required":             []any{"query"},
			"additionalProperties": false,
		},
	}
}

// compactSchema 在 schema 過大時只保留形狀（型別、必填、參數名與型別），
// 捨棄說明與列舉值。截斷 JSON 會產生無法解析的 schema，因此改成降級而非截斷。
func compactSchema(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return nil
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	if len(encoded) <= findToolsSchemaLimit {
		return schema
	}
	compact := map[string]any{"type": "object"}
	if required, ok := schema["required"]; ok {
		compact["required"] = required
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return compact
	}
	shapes := make(map[string]any, len(properties))
	for name, value := range properties {
		property, ok := value.(map[string]any)
		if !ok {
			shapes[name] = map[string]any{}
			continue
		}
		shape := map[string]any{}
		if kind, ok := property["type"]; ok {
			shape["type"] = kind
		}
		shapes[name] = shape
	}
	compact["properties"] = shapes
	return compact
}

func indexToolTerms(definition domain.ToolDefinition) map[string]float64 {
	terms := map[string]float64{}
	add := func(text string, weight float64) {
		for _, term := range tokenizeForRetrieval(text) {
			if terms[term] < weight {
				terms[term] = weight
			}
		}
	}
	add(definition.Description, 1)
	add(definition.Label, 2)
	add(definition.Name, 3)
	return terms
}

// inverseDocumentFrequency 壓低共通詞的權重。外掛型 Server 的工具名稱高度重複
// （每一個都叫 *_query），沒有這層加權，任何含「query」的問題都會命中整份目錄。
//
// 出現在大半個目錄裡的詞直接歸零：它區分不出任何東西，卻會讓排序變成隨機取樣，
// 把真正相關的工具擠出名單。這種詞不該影響選擇，該由問題裡比較明確的詞決定。
func inverseDocumentFrequency(entries []toolIndexEntry) map[string]float64 {
	frequency := map[string]int{}
	for _, entry := range entries {
		for term := range entry.terms {
			frequency[term]++
		}
	}
	total := float64(len(entries))
	weights := make(map[string]float64, len(frequency))
	for term, count := range frequency {
		if total > 0 && float64(count)/total >= commonTermRatio {
			weights[term] = 0
			continue
		}
		weights[term] = math.Log(1 + total/float64(1+count))
	}
	return weights
}

// tokenizeForRetrieval 把文字切成可比對的詞：ASCII 取連續英數字，CJK 取連續兩字。
// 中文沒有空白分詞，bigram 是不需要詞庫就能用的最小單位——「製令狀態」會產生
// 「製令」「令狀」「狀態」，足以對上工具說明裡的同一組字。
func tokenizeForRetrieval(text string) []string {
	terms := []string{}
	ascii := make([]rune, 0, 16)
	cjk := make([]rune, 0, 16)
	flushASCII := func() {
		if len(ascii) >= 2 {
			terms = append(terms, string(ascii))
		}
		ascii = ascii[:0]
	}
	flushCJK := func() {
		switch {
		case len(cjk) == 1:
			terms = append(terms, string(cjk))
		default:
			for index := 0; index+1 < len(cjk); index++ {
				terms = append(terms, string(cjk[index:index+2]))
			}
		}
		cjk = cjk[:0]
	}
	for _, value := range strings.ToLower(text) {
		switch {
		case isCJKRune(value):
			flushASCII()
			cjk = append(cjk, value)
		case unicode.IsLetter(value) || unicode.IsDigit(value):
			flushCJK()
			ascii = append(ascii, value)
		default:
			flushASCII()
			flushCJK()
		}
	}
	flushASCII()
	flushCJK()
	return terms
}

func isCJKRune(value rune) bool {
	return unicode.Is(unicode.Han, value) ||
		unicode.Is(unicode.Hiragana, value) ||
		unicode.Is(unicode.Katakana, value) ||
		unicode.Is(unicode.Hangul, value)
}

// retrievalExempt 回報這個工具是否不參與檢索（核心工具與 find_tools 本身）。
func retrievalExempt(name string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	return coreToolNames[trimmed] || trimmed == findToolsToolName
}

func isMCPToolName(name string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "mcp__")
}
