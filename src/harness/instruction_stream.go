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
	stream.buffer.WriteString(delta)
	if instructionPrefixMayBeTool(stream.buffer.String()) {
		return nil
	}
	stream.released = true
	buffered := stream.buffer.String()
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
	for stream.candidate.Len() > 0 {
		value := stream.candidate.String()
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
			stream.candidate.Reset()
			stream.candidate.WriteString(value)
		}
		possible, matched := embeddedInstructionToolPrefix(stream.candidate.String())
		if matched {
			stream.candidate.Reset()
			stream.suppressing = true
			return nil
		}
		if possible {
			return nil
		}
		// 不是工具 JSON；先放行左大括號，再繼續檢查後方是否另有工具指令。
		if err := emit("{"); err != nil {
			return err
		}
		value = stream.candidate.String()
		stream.candidate.Reset()
		stream.candidate.WriteString(strings.TrimPrefix(value, "{"))
	}
	return nil
}

func embeddedInstructionToolPrefix(value string) (possible, matched bool) {
	var compact strings.Builder
	for _, character := range value {
		if unicode.IsSpace(character) {
			continue
		}
		compact.WriteRune(unicode.ToLower(character))
	}
	prefix := compact.String()
	for _, marker := range []string{`{"type":"tool_use"`, `{"type":"tool_call"`, `{"type":"tool"`} {
		if strings.HasPrefix(marker, prefix) {
			possible = true
		}
		if strings.HasPrefix(prefix, marker) {
			return true, true
		}
	}
	return possible, false
}

func instructionPrefixMayBeTool(value string) bool {
	trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
	if trimmed == "" {
		return true
	}
	lower := strings.ToLower(trimmed)
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
		if strings.HasPrefix(marker, lower) || strings.HasPrefix(lower, marker) {
			return true
		}
	}
	return false
}
