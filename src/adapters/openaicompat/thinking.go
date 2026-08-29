package openaicompat

import (
	"regexp"
	"strings"
)

var thinkTagPattern = regexp.MustCompile(`(?i)</?think\s*>`)

type thinkingFragment struct {
	Text     string
	Thinking bool
}

// splitTaggedThinking 將部分 OpenAI-compatible 服務塞在 content 內的 THINK
// 區段拆出。除了標準 <think>...</think>，也支援聊天模板已吃掉開頭標籤、
// 只留下「思考內容</think>正式回答」的格式。
func splitTaggedThinking(value string) (content, reasoning string, found bool) {
	matches := thinkTagPattern.FindAllStringIndex(value, -1)
	if len(matches) == 0 {
		return strings.TrimSpace(value), "", false
	}
	var contentBuilder strings.Builder
	var reasoningBuilder strings.Builder
	inThinking := false
	sawMarker := false
	cursor := 0
	for _, match := range matches {
		segment := value[cursor:match[0]]
		tag := strings.ToLower(value[match[0]:match[1]])
		closing := strings.HasPrefix(tag, "</")
		if closing && !inThinking && !sawMarker {
			reasoningBuilder.WriteString(segment)
		} else if inThinking {
			reasoningBuilder.WriteString(segment)
		} else {
			contentBuilder.WriteString(segment)
		}
		sawMarker = true
		inThinking = !closing
		cursor = match[1]
	}
	if inThinking {
		reasoningBuilder.WriteString(value[cursor:])
	} else {
		contentBuilder.WriteString(value[cursor:])
	}
	return strings.TrimSpace(contentBuilder.String()), strings.TrimSpace(reasoningBuilder.String()), true
}

func mergeReasoning(primary, embedded string) string {
	primary = strings.TrimSpace(primary)
	embedded = strings.TrimSpace(embedded)
	switch {
	case primary == "":
		return embedded
	case embedded == "" || embedded == primary:
		return primary
	default:
		return primary + "\n\n" + embedded
	}
}

// taggedThinkingStream 將完整出現在串流中的 THINK 標籤即時分流，並保留
// 可能跨 chunk 的標籤尾端。若 Provider 直到後續 chunk 才送出孤立的
// </think>，最終 ModelResponse 仍會由 splitTaggedThinking 完整校正。
type taggedThinkingStream struct {
	pending    string
	inThinking bool
	sawMarker  bool
}

func (stream *taggedThinkingStream) Push(value string, final bool) []thinkingFragment {
	data := stream.pending + value
	stream.pending = ""
	fragments := []thinkingFragment{}
	for len(data) > 0 {
		match := thinkTagPattern.FindStringIndex(data)
		if match == nil {
			emitLength := len(data)
			if !final {
				hold := thinkMarkerSuffixLength(data)
				emitLength -= hold
				stream.pending = data[emitLength:]
			}
			fragments = appendThinkingFragment(fragments, data[:emitLength], stream.inThinking)
			break
		}
		segment := data[:match[0]]
		tag := strings.ToLower(data[match[0]:match[1]])
		closing := strings.HasPrefix(tag, "</")
		if closing && !stream.inThinking && !stream.sawMarker {
			fragments = appendThinkingFragment(fragments, segment, true)
		} else {
			fragments = appendThinkingFragment(fragments, segment, stream.inThinking)
		}
		stream.sawMarker = true
		stream.inThinking = !closing
		data = data[match[1]:]
	}
	return fragments
}

func appendThinkingFragment(values []thinkingFragment, text string, thinking bool) []thinkingFragment {
	if text == "" {
		return values
	}
	if len(values) > 0 && values[len(values)-1].Thinking == thinking {
		values[len(values)-1].Text += text
		return values
	}
	return append(values, thinkingFragment{Text: text, Thinking: thinking})
}

func thinkMarkerSuffixLength(value string) int {
	markers := []string{"<think>", "</think>"}
	maximum := len("</think>") - 1
	if len(value) < maximum {
		maximum = len(value)
	}
	for length := maximum; length > 0; length-- {
		suffix := strings.ToLower(value[len(value)-length:])
		for _, marker := range markers {
			if strings.HasPrefix(marker, suffix) {
				return length
			}
		}
	}
	return 0
}
