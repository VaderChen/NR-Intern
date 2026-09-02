package openaicompat

import (
	"regexp"
	"strings"
)

// thinkTagPattern 同時涵蓋兩種思考標記：
//   - <think>…</think>：多數推論引擎的慣例。
//   - <|channel|>analysis<|message|>…：harmony 風格的控制 token。本機模型
//     （mlx、llama.cpp）常在沒有對應解析器的情況下把它們原樣吐進 content，
//     使用者就會在畫面上看到 <|channel>thought 這種字串。缺右邊那根直線的
//     殘缺寫法也要收，實測就是這樣吐出來的。
var thinkTagPattern = regexp.MustCompile(`(?i)</?think\s*>|<\|[a-z_]{1,20}\|?>`)

// thinkingChannels 是要當成思考內容的 harmony channel 名稱。
// 未知名稱一律當成正式回答：把答案誤藏起來，比多顯示一段思考嚴重得多。
var thinkingChannels = map[string]bool{
	"analysis": true, "thought": true, "thoughts": true, "thinking": true,
	"commentary": true, "reflection": true, "reasoning": true, "scratchpad": true,
}

type thinkingFragment struct {
	Text     string
	Thinking bool
}

type markerKind int

const (
	markerThinkOpen markerKind = iota
	markerThinkClose
	markerChannel
	// markerRole 之後跟著角色名稱（<|start|>assistant），名稱要吃掉不顯示。
	markerRole
	// markerEnd 是訊息結束。模型若沒補上第二個 channel 就直接續寫，後面那段
	// 幾乎一定是要給使用者的回答，因此結束標記一律回到回答模式——這個方向
	// 只會把文字從思考搬到答案，不會反過來把答案藏起來。
	markerEnd
	markerOther
)

func classifyMarker(tag string) markerKind {
	lower := strings.ToLower(tag)
	switch {
	case strings.HasPrefix(lower, "</"):
		return markerThinkClose
	case strings.HasPrefix(lower, "<t"):
		return markerThinkOpen
	}
	name := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(lower, "<|"), ">"), "|")
	switch name {
	case "channel":
		return markerChannel
	case "start":
		return markerRole
	case "end", "return", "endofturn", "endoftext", "im_end":
		return markerEnd
	default:
		return markerOther
	}
}

// consumeLeadingWord 取出區段開頭的 ASCII 單字（channel 或角色名稱）。
// 標記後面直接接非 ASCII 內容時回傳 ok=false，代表這個標記沒有帶名稱。
func consumeLeadingWord(segment string) (word, rest string, ok bool) {
	index := 0
	for index < len(segment) {
		value := segment[index]
		isWord := (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') ||
			(value >= '0' && value <= '9') || value == '_' || value == '-'
		if !isWord {
			break
		}
		index++
	}
	if index == 0 {
		return "", segment, false
	}
	return strings.ToLower(segment[:index]), segment[index:], true
}

// channelSegment 依 channel 名稱決定模式，並回傳去掉名稱後的內容。
func channelSegment(segment string, current bool) (rest string, thinking bool) {
	word, rest, ok := consumeLeadingWord(segment)
	if !ok {
		// <|channel>後面直接是內容：harmony 的最終訊息常是這種形狀，
		// 當成正式回答而不是延續上一個思考頻道。
		return segment, false
	}
	if thinkingChannels[word] {
		return rest, true
	}
	if word == "final" || word == "message" || word == "answer" || word == "response" || word == "output" {
		return rest, false
	}
	return rest, current
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
	// pending 是「這個標記後面要吃掉名稱」的狀態：channel 與 start 都是先出現
	// 標記、名稱才跟在後面的內容裡。
	pendingKind := markerOther
	cursor := 0
	write := func(segment string, thinking bool) {
		if thinking {
			reasoningBuilder.WriteString(segment)
			return
		}
		contentBuilder.WriteString(segment)
	}
	for _, match := range matches {
		segment := value[cursor:match[0]]
		switch pendingKind {
		case markerChannel:
			segment, inThinking = channelSegment(segment, inThinking)
		case markerRole:
			_, segment, _ = consumeLeadingWord(segment)
		}
		pendingKind = markerOther
		kind := classifyMarker(value[match[0]:match[1]])
		// 只有結尾標記、沒有開頭標記時，前面的內容是被聊天模板吃掉開頭的思考段。
		write(segment, inThinking || (kind == markerThinkClose && !sawMarker))
		switch kind {
		case markerThinkOpen:
			inThinking = true
		case markerThinkClose, markerEnd:
			inThinking = false
		case markerChannel, markerRole:
			pendingKind = kind
		}
		sawMarker = true
		cursor = match[1]
	}
	trailing := value[cursor:]
	switch pendingKind {
	case markerChannel:
		trailing, inThinking = channelSegment(trailing, inThinking)
	case markerRole:
		_, trailing, _ = consumeLeadingWord(trailing)
	}
	write(trailing, inThinking)
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
	// pendingKind 記住上一個標記是否還在等名稱（channel／角色）。名稱可能跨
	// chunk 抵達，因此要等到看得見字尾邊界才處理。
	pendingKind markerKind
}

func (stream *taggedThinkingStream) Push(value string, final bool) []thinkingFragment {
	data := stream.pending + value
	stream.pending = ""
	fragments := []thinkingFragment{}
	for len(data) > 0 {
		if stream.pendingKind == markerChannel || stream.pendingKind == markerRole {
			consumed, rest, ok := consumeMarkerName(data, final)
			if !ok {
				stream.pending = data
				return fragments
			}
			if stream.pendingKind == markerChannel {
				_, stream.inThinking = channelSegment(consumed, stream.inThinking)
			}
			stream.pendingKind = markerOther
			data = rest
			continue
		}
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
		kind := classifyMarker(data[match[0]:match[1]])
		thinking := stream.inThinking || (kind == markerThinkClose && !stream.sawMarker)
		fragments = appendThinkingFragment(fragments, segment, thinking)
		switch kind {
		case markerThinkOpen:
			stream.inThinking = true
		case markerThinkClose, markerEnd:
			stream.inThinking = false
		case markerChannel, markerRole:
			stream.pendingKind = kind
		}
		stream.sawMarker = true
		data = data[match[1]:]
	}
	return fragments
}

// consumeMarkerName 從 chunk 開頭取出標記名稱。名稱尚未完整抵達時回傳
// ok=false，讓呼叫端把資料留到下一個 chunk——先送出去就會在畫面上閃出
// 「thought」這種殘字。
func consumeMarkerName(data string, final bool) (name, rest string, ok bool) {
	word, remainder, matched := consumeLeadingWord(data)
	if !matched {
		return "", data, true
	}
	if len(remainder) == 0 && !final {
		return "", data, false
	}
	return word, remainder, true
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

// thinkMarkerSuffixLength 回報結尾有多少字元可能是還沒收完的標記，必須留到
// 下一個 chunk 再判斷。涵蓋 <think>／</think> 與 <|channel|> 這類控制 token。
func thinkMarkerSuffixLength(value string) int {
	maximum := maxMarkerHold
	if len(value) < maximum {
		maximum = len(value)
	}
	tail := value[len(value)-maximum:]
	index := strings.LastIndexByte(tail, '<')
	if index < 0 {
		return 0
	}
	candidate := tail[index:]
	// 已經出現 '>' 代表標記收完了（真是標記的話上面就會比對到），不必再留。
	if strings.ContainsRune(candidate, '>') {
		return 0
	}
	return len(candidate)
}

// maxMarkerHold 是等待標記收尾時最多保留的字元數。內容裡的 '<' 很常見
// （程式碼、比較運算），沒有上限就會把整段輸出卡到 chunk 結束。
const maxMarkerHold = 24
