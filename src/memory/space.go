package memory

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"AgenticService/src/domain"
)

// SpaceConfig 是「回憶空間」的參數：跨對話共用記憶時的准入、視窗與上限。
// 設計背景見 docs/ai-agent/MEMORY_SPACE.md。
type SpaceConfig struct {
	Enabled bool `json:"enabled"`
	// RecallLimit 與 MaxInjectedCharacters 是單次注入的視窗。刻意比一般記憶小：
	// 召回內容每一輪都會重送，塞得多不代表答得好。
	RecallLimit           int `json:"recall_limit"`
	MaxInjectedCharacters int `json:"max_injected_characters"`
	// MinRelevance 是相關度門檻。低於門檻寧可完全不注入，也不要塞雜訊。
	MinRelevance float64 `json:"min_relevance"`
	// ScopeCap 是單一 scope 的記憶則數上限，超過就淘汰價值最低的。
	ScopeCap int `json:"scope_cap"`
	// StaleDays 是「多久沒被召回就視為過期」的天數。
	StaleDays int `json:"stale_days"`
}

const (
	defaultSpaceRecallLimit   = 6
	defaultSpaceInjectedChars = 1_200
	defaultSpaceMinRelevance  = 0.15
	defaultSpaceScopeCap      = 500
	defaultSpaceStaleDays     = 90
)

func (c SpaceConfig) normalized() SpaceConfig {
	if c.RecallLimit <= 0 {
		c.RecallLimit = defaultSpaceRecallLimit
	}
	if c.MaxInjectedCharacters <= 0 {
		c.MaxInjectedCharacters = defaultSpaceInjectedChars
	}
	if c.MinRelevance <= 0 {
		c.MinRelevance = defaultSpaceMinRelevance
	}
	if c.ScopeCap <= 0 {
		c.ScopeCap = defaultSpaceScopeCap
	}
	if c.StaleDays <= 0 {
		c.StaleDays = defaultSpaceStaleDays
	}
	return c
}

// admittedKinds 是可以進入回憶空間的型別。
//
// fact 不收：「這個檔案有 300 行」會過期，而且下次查得到。留下來只會在半年後
// 用一個過時的事實誤導模型。
var admittedKinds = map[domain.MemoryKind]bool{
	domain.MemoryKindPreference: true,
	domain.MemoryKindDecision:   true,
	domain.MemoryKindConstraint: true,
	domain.MemoryKindProcedure:  true,
}

// secretPattern 擋掉憑證。這一條不看模型判斷：它沒有誠實與否的問題，
// 只是根本分辨不出哪些字串是機密。
var secretPattern = regexp.MustCompile(`(?i)(api[_\- ]?key|secret|password|passwd|token|bearer\s+[a-z0-9._\-]{8,}|private[_\- ]?key|credential|-----BEGIN [A-Z ]*PRIVATE KEY-----|sk-[a-zA-Z0-9]{16,}|ghp_[a-zA-Z0-9]{20,})`)

// AdmissionError 說明為什麼這則記憶不被收下，訊息會回給模型。
type AdmissionError struct {
	Reason string
}

func (e *AdmissionError) Error() string { return e.Reason }

// Admit 檢查一則記憶能不能進入回憶空間。
//
// 回憶空間關閉時不設限，維持原本的長期記憶行為；開啟後才套用准入條件——
// 跨對話共用的內容必須經得起「換一個對話、換一天仍然成立」的檢驗。
func (c SpaceConfig) Admit(input domain.RememberMemoryInput) error {
	if !c.Enabled {
		return nil
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return &AdmissionError{Reason: "記憶內容不可為空"}
	}
	if !admittedKinds[input.Kind] {
		return &AdmissionError{Reason: fmt.Sprintf(
			"回憶空間只收 preference、decision、constraint、procedure；%q 這類內容會過期，需要時重新查詢即可", input.Kind)}
	}
	if secretPattern.MatchString(content) {
		return &AdmissionError{Reason: "內容看起來包含金鑰、權杖或密碼，不會寫入記憶"}
	}
	return nil
}

// ScopeForSessionWithSpace 決定記憶的共享範圍。
//
// 回憶空間開啟時預設收斂到專案：跨對話共用不等於全域共用，甲專案的決策不該
// 出現在乙專案的對話裡。session metadata 明確指定 memory_scope 時仍以它為準。
func ScopeForSessionWithSpace(session domain.Session, spaceEnabled bool) string {
	if session.Metadata != nil {
		if value, ok := session.Metadata["memory_scope"].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	if spaceEnabled {
		if value := strings.TrimSpace(session.ProjectID); value != "" {
			return "project:" + value
		}
		if value := strings.TrimSpace(session.WorkspaceID); value != "" {
			return "workspace:" + value
		}
	}
	if value := strings.TrimSpace(session.AgentID); value != "" {
		return value
	}
	return "default"
}

// queryTerms 把文字切成可比對的詞：ASCII 取連續英數字，CJK 取連續兩字。
// 與工具檢索用同一套判讀，中文不需要斷詞器。
func queryTerms(text string) map[string]bool {
	terms := map[string]bool{}
	ascii := make([]rune, 0, 16)
	cjk := make([]rune, 0, 16)
	flushASCII := func() {
		if len(ascii) >= 2 {
			terms[string(ascii)] = true
		}
		ascii = ascii[:0]
	}
	flushCJK := func() {
		switch {
		case len(cjk) == 1:
			terms[string(cjk)] = true
		default:
			for index := 0; index+1 < len(cjk); index++ {
				terms[string(cjk[index:index+2])] = true
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

// scoredMemory 是排序用的中間結果。
type scoredMemory struct {
	memory domain.Memory
	score  float64
}

// rankMemories 依相關度、新鮮度與命中率排序，並濾掉低於門檻的項目。
//
// 權重刻意讓相關度主導：新鮮或常被引用都不能讓不相關的內容擠進視窗。
func rankMemories(values []domain.Memory, query string, config SpaceConfig, now time.Time) []domain.Memory {
	terms := queryTerms(query)
	if len(terms) == 0 {
		return nil
	}
	scored := make([]scoredMemory, 0, len(values))
	for _, value := range values {
		relevance := memoryRelevance(value, terms)
		if relevance <= 0 {
			continue
		}
		score := relevance*0.6 + memoryFreshness(value, now, config)*0.2 + memoryHitRate(value)*0.2
		if score < config.MinRelevance {
			continue
		}
		scored = append(scored, scoredMemory{memory: value, score: score})
	}
	sort.SliceStable(scored, func(first, second int) bool {
		if scored[first].score != scored[second].score {
			return scored[first].score > scored[second].score
		}
		return scored[first].memory.ID < scored[second].memory.ID
	})
	if len(scored) > config.RecallLimit {
		scored = scored[:config.RecallLimit]
	}
	result := make([]domain.Memory, 0, len(scored))
	for _, item := range scored {
		result = append(result, item.memory)
	}
	return result
}

// memoryRelevance 是查詢詞在這則記憶裡的覆蓋率。中文以 bigram 比對，
// 與工具檢索同一套判讀，不需要斷詞器。
func memoryRelevance(value domain.Memory, terms map[string]bool) float64 {
	haystack := queryTerms(value.Content + " " + strings.Join(value.Tags, " "))
	if len(haystack) == 0 {
		return 0
	}
	matched := 0
	for term := range terms {
		if haystack[term] {
			matched++
		}
	}
	if matched == 0 {
		return 0
	}
	return float64(matched) / float64(len(terms))
}

func memoryFreshness(value domain.Memory, now time.Time, config SpaceConfig) float64 {
	reference := value.UpdatedAt
	if value.LastAccessedAt != nil && value.LastAccessedAt.After(reference) {
		reference = *value.LastAccessedAt
	}
	if reference.IsZero() {
		return 0
	}
	age := now.Sub(reference).Hours() / 24
	if age <= 0 {
		return 1
	}
	stale := float64(config.StaleDays)
	if age >= stale {
		return 0
	}
	return 1 - age/stale
}

// memoryHitRate 以 confidence 代表「這則記憶被驗證過的程度」。
func memoryHitRate(value domain.Memory) float64 {
	if value.Confidence <= 0 {
		return 0
	}
	if value.Confidence > 1 {
		return 1
	}
	return value.Confidence
}
