package harness

import (
	"strings"
	"unicode"
)

// instructionTextStream 只暫存「可能是工具指令」的開頭。一般回答一旦能排除
// 工具協定，就立即逐段轉送；真正的工具 JSON 則完整留在後端解析，不會顯示在
// 對話中。
type instructionTextStream struct {
	buffer   strings.Builder
	released bool
}

func (stream *instructionTextStream) Push(delta string, emit func(string) error) error {
	if delta == "" {
		return nil
	}
	if stream.released {
		return emit(delta)
	}
	stream.buffer.WriteString(delta)
	if instructionPrefixMayBeTool(stream.buffer.String()) {
		return nil
	}
	stream.released = true
	buffered := stream.buffer.String()
	stream.buffer.Reset()
	return emit(buffered)
}

func (stream *instructionTextStream) Released() bool {
	return stream != nil && stream.released
}

// Finish 供未收到串流 delta、或以 JSON／標籤字元開頭但最後確認是一般回答的
// Provider 使用。finalContent 以 adapter 的最終正規化結果為準。
func (stream *instructionTextStream) Finish(finalContent string, emit func(string) error) error {
	if stream == nil || stream.released || finalContent == "" {
		return nil
	}
	stream.buffer.Reset()
	stream.released = true
	return emit(finalContent)
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
