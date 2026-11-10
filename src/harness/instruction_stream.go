package harness

import (
	"strings"
	"unicode"
)

// instructionTextStream 只暫存「可能是工具指令」的開頭。一般回答一旦能排除
// 工具協定，就立即逐段轉送；真正的工具 JSON 則完整留在後端解析，不會顯示在
// 對話中。
type instructionTextStream struct {
	buffer      strings.Builder
	candidate   strings.Builder
	leading     int
	holding     bool
	released    bool
	suppressing bool
}

func (stream *instructionTextStream) Push(delta string, emit func(string) error) error {
	if delta == "" {
		return nil
	}
	if stream.released {
		return stream.pushReleased(delta, emit)
	}
	if stream.holding {
		return nil
	}
	stream.buffer.WriteString(delta)
	buffered := stream.buffer.String()
	prefix := strings.TrimLeftFunc(buffered[stream.leading:], unicode.IsSpace)
	stream.leading = len(buffered) - len(prefix)
	possible, matched := instructionPrefixStatus(prefix)
	if matched {
		// 已確認是整段保留的協定／程式碼前綴。最終內容由 Provider 回傳，
		// 這層不用再保存一份完整工具參數；一般 JSON 仍由 Finish 放行。
		stream.holding = true
		stream.buffer.Reset()
		return nil
	}
	if possible {
		return nil
	}
	stream.released = true
	stream.buffer.Reset()
	return stream.pushReleased(buffered, emit)
}

func (stream *instructionTextStream) Released() bool {
	return stream != nil && stream.released
}

// Finish 供未收到串流 delta、或以 JSON／標籤字元開頭但最後確認是一般回答的
// Provider 使用。finalContent 以 adapter 的最終正規化結果為準。
func (stream *instructionTextStream) Finish(finalContent string, emit func(string) error) error {
	if stream == nil {
		return nil
	}
	if stream.released {
		if stream.suppressing || stream.candidate.Len() == 0 {
			stream.candidate.Reset()
			return nil
		}
		pending := stream.candidate.String()
		stream.candidate.Reset()
		return emit(pending)
	}
	if finalContent == "" {
		return nil
	}
	stream.buffer.Reset()
	stream.released = true
	return emit(finalContent)
}

// pushReleased 讓一般答案維持即時串流，同時攔截模型違反協定、在說明文字後
// 才輸出的 tool_use JSON。只在遇到「{」時短暫暫存並辨識固定協定前綴，
// 不會為了工具偵測而延遲整段一般回答。
func (stream *instructionTextStream) pushReleased(delta string, emit func(string) error) error {
	if stream.suppressing || delta == "" {
		return nil
	}
	stream.candidate.WriteString(delta)
	value := stream.candidate.String()
	// 已判定的一般文字只前移游標，不在每個大括號後重複複製剩餘全文。
	for value != "" {
		brace := strings.IndexByte(value, '{')
		if brace < 0 {
			stream.candidate.Reset()
			return emit(value)
		}
		if brace > 0 {
			if err := emit(value[:brace]); err != nil {
				return err
			}
			value = value[brace:]
		}
		possible, matched := embeddedInstructionToolPrefix(value)
		if matched {
			stream.candidate.Reset()
			stream.suppressing = true
			return nil
		}
		if possible {
			if len(value) != stream.candidate.Len() {
				stream.candidate.Reset()
				stream.candidate.WriteString(value)
			}
			return nil
		}
		// 不是工具 JSON；先放行左大括號，再繼續檢查後方是否另有工具指令。
		if err := emit("{"); err != nil {
			return err
		}
		value = value[1:]
	}
	stream.candidate.Reset()
	return nil
}

func embeddedInstructionToolPrefix(value string) (possible, matched bool) {
	const longestMarker = len(`{"type":"tool_call"`)
	var compact [longestMarker]byte
	length := 0
	for _, character := range value {
		if unicode.IsSpace(character) {
			continue
		}
		character = unicode.ToLower(character)
		if character > unicode.MaxASCII {
			return false, false
		}
		compact[length] = byte(character)
		length++
		prefix := string(compact[:length])
		possible = false
		for _, marker := range []string{`{"type":"tool_use"`, `{"type":"tool_call"`, `{"type":"tool"`} {
			if marker == prefix {
				return true, true
			}
			if strings.HasPrefix(marker, prefix) {
				possible = true
			}
		}
		if !possible {
			return false, false
		}
	}
	return true, false
}

func instructionPrefixStatus(value string) (possible, matched bool) {
	trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
	if trimmed == "" {
		return true, false
	}
	// 標記最長只有 10 bytes；不能對累積中的整份工具參數做 ToLower。
	lower := strings.ToLower(trimmed[:min(len(trimmed), len("[tool_call"))])
	for _, marker := range []string{
		"{",
		"```",
		"~~~",
		"<tool_call",
		"<tool_use",
		"[tool_call",
		"[tool_use",
		"to=",
	} {
		if strings.HasPrefix(lower, marker) {
			return true, true
		}
		if strings.HasPrefix(marker, lower) {
			possible = true
		}
	}
	return possible, false
}
