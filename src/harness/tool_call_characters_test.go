package harness

import (
	"AgenticService/src/domain"
	"strings"
	"testing"
)

// 工具呼叫的參數一樣會送進模型，字元閘門就不能當它不存在。
//
// 實測整份對話紀錄：Content 共 315 萬字，工具呼叫參數 45.6 萬字——12% 的預算
// 是隱形的。而且分布不平均：file_write 與 apply_patch 那種訊息的 Content 是空的，
// 整個內容都在 arguments 裡，只算 Content 的話那一輪等於零。
func TestCharacterGatesCountToolCallArguments(t *testing.T) {
	payload := strings.Repeat("內容", 500) // 1,000 個字元，全都在參數裡
	messages := []domain.Message{{
		Role: "assistant",
		ToolCalls: []domain.ToolCall{{
			ID: "call_1", Name: "file_write",
			Arguments: map[string]any{"path": "/tmp/a.txt", "content": payload},
		}},
	}}

	counted := historyCharacters(messages)
	if counted <= 1000 {
		t.Fatalf("只算 Content 會得到 0；含參數應超過 1000，得到 %d", counted)
	}

	sequenced := []sequencedMessage{{Message: messages[0]}}
	if shaped := shapedCharacters(sequenced, 24_000); shaped <= 1000 {
		t.Fatalf("shapedCharacters 也要含參數，得到 %d", shaped)
	}
	// 上限比這一則還小時，保留則數不該因為「看起來是空的」而多留。
	if retained := retainCountWithinCharacters(sequenced, 100); retained != 1 {
		t.Fatalf("至少保留一則，得到 %d", retained)
	}
}

// 工具結果的截斷仍然只作用在 Content 上：參數不是工具輸出，不套那個上限。
func TestShapedCharactersStillCapsToolResultsOnly(t *testing.T) {
	long := strings.Repeat("x", 50_000)
	sequenced := []sequencedMessage{
		{Message: domain.Message{Role: "tool", Content: long}},
	}
	if shaped := shapedCharacters(sequenced, 24_000); shaped != 24_000 {
		t.Fatalf("超長工具結果應截到上限，得到 %d", shaped)
	}
}

// 沒有工具呼叫時行為不變，避免這個改動悄悄改動既有的壓縮時機。
func TestToolCallCharactersIsZeroWithoutCalls(t *testing.T) {
	if got := toolCallCharacters(nil); got != 0 {
		t.Fatalf("沒有工具呼叫應為 0，得到 %d", got)
	}
	if got := toolCallCharacters([]domain.ToolCall{{ID: "call_1", Name: "x"}}); got != 1 {
		t.Fatalf("只有名稱時應只算名稱，得到 %d", got)
	}
}
