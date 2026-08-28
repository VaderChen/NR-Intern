// Package tokens 提供不依賴特定 Provider tokenizer 的 token 估算實作。
package tokens

import (
	"AgenticService/src/domain"
	"encoding/json"
	"math"
	"unicode"
)

// HeuristicCounter 以字元類別加權估算 token 數。
//
// 權重來源是常見 BPE tokenizer 的實測範圍：ASCII 文字約 4 字元 1 token；
// CJK 字元依模型不同約 0.6～1.0 token，這裡預設取上界 1.0。
// 低估會讓請求超出 Provider context window 而被拒絕，高估只會提早 compaction，
// 因此預設值刻意偏保守；需要更貼近特定模型時可覆寫個別權重。
type HeuristicCounter struct {
	CJKTokensPerRune   float64
	ASCIITokensPerRune float64
	OtherTokensPerRune float64

	// MessageOverhead 是每則訊息的角色與分隔符成本。
	MessageOverhead int
	// ToolCallOverhead 是每個 tool call 的結構成本，不含名稱與參數本身。
	ToolCallOverhead int
	// ToolDefinitionOverhead 是每個工具定義的結構成本，不含 schema 內容。
	ToolDefinitionOverhead int
}

func NewHeuristicCounter() *HeuristicCounter {
	return &HeuristicCounter{
		CJKTokensPerRune:       1.0,
		ASCIITokensPerRune:     0.25,
		OtherTokensPerRune:     0.5,
		MessageOverhead:        4,
		ToolCallOverhead:       10,
		ToolDefinitionOverhead: 8,
	}
}

func (c *HeuristicCounter) normalized() HeuristicCounter {
	value := HeuristicCounter{}
	if c != nil {
		value = *c
	}
	defaults := HeuristicCounter{
		CJKTokensPerRune:       1.0,
		ASCIITokensPerRune:     0.25,
		OtherTokensPerRune:     0.5,
		MessageOverhead:        4,
		ToolCallOverhead:       10,
		ToolDefinitionOverhead: 8,
	}
	if value.CJKTokensPerRune <= 0 {
		value.CJKTokensPerRune = defaults.CJKTokensPerRune
	}
	if value.ASCIITokensPerRune <= 0 {
		value.ASCIITokensPerRune = defaults.ASCIITokensPerRune
	}
	if value.OtherTokensPerRune <= 0 {
		value.OtherTokensPerRune = defaults.OtherTokensPerRune
	}
	if value.MessageOverhead < 0 {
		value.MessageOverhead = defaults.MessageOverhead
	}
	if value.ToolCallOverhead < 0 {
		value.ToolCallOverhead = defaults.ToolCallOverhead
	}
	if value.ToolDefinitionOverhead < 0 {
		value.ToolDefinitionOverhead = defaults.ToolDefinitionOverhead
	}
	return value
}

func (c *HeuristicCounter) EstimateText(text string) int {
	return int(math.Ceil(c.normalized().weight(text)))
}

func (c *HeuristicCounter) EstimateMessages(messages []domain.Message) int {
	config := c.normalized()
	total := 0.0
	for _, message := range messages {
		total += float64(config.MessageOverhead)
		total += config.weight(message.Content)
		total += config.weight(message.Role)
		total += config.weight(message.ToolName)
		for _, call := range message.ToolCalls {
			total += float64(config.ToolCallOverhead)
			total += config.weight(call.Name)
			if encoded, err := json.Marshal(call.Arguments); err == nil {
				total += config.weight(string(encoded))
			}
		}
	}
	return int(math.Ceil(total))
}

func (c *HeuristicCounter) EstimateTools(definitions []domain.ToolDefinition) int {
	config := c.normalized()
	total := 0.0
	for _, definition := range definitions {
		total += float64(config.ToolDefinitionOverhead)
		total += config.weight(definition.Name)
		total += config.weight(definition.Description)
		if encoded, err := json.Marshal(definition.InputSchema); err == nil {
			total += config.weight(string(encoded))
		}
	}
	return int(math.Ceil(total))
}

func (c HeuristicCounter) weight(text string) float64 {
	total := 0.0
	for _, value := range text {
		switch {
		case isCJK(value):
			total += c.CJKTokensPerRune
		case value < unicode.MaxASCII:
			total += c.ASCIITokensPerRune
		default:
			total += c.OtherTokensPerRune
		}
	}
	return total
}

// isCJK 涵蓋中日韓文字與全形標點；這些字元在主流 tokenizer 中
// 幾乎都是每字一個以上的 token，不能套用 ASCII 的 4 字元比例。
func isCJK(value rune) bool {
	switch {
	case value >= 0x2E80 && value <= 0x9FFF: // 部首補充～CJK 統一表意文字
		return true
	case value >= 0xA960 && value <= 0xA97F: // 諺文字母擴充 A
		return true
	case value >= 0xAC00 && value <= 0xD7FF: // 諺文音節
		return true
	case value >= 0xF900 && value <= 0xFAFF: // CJK 相容表意文字
		return true
	case value >= 0xFE30 && value <= 0xFE4F: // CJK 相容形式
		return true
	case value >= 0xFF00 && value <= 0xFF60: // 全形 ASCII 變體與標點
		return true
	case value >= 0xFFE0 && value <= 0xFFE6: // 全形貨幣與符號
		return true
	case value >= 0x20000 && value <= 0x3FFFF: // CJK 擴充 B 以後
		return true
	default:
		return false
	}
}
