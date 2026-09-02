package openaicompat

import (
	"strings"
	"testing"
)

// 本機模型（mlx、llama.cpp）常在沒有 harmony 解析器的情況下，把控制 token
// 原樣吐進 content。實測畫面上就會出現「<|channel>thought」這一行，而且後面
// 整段思考被當成正式回答顯示給使用者。
func TestSplitTaggedThinkingSeparatesHarmonyChannels(t *testing.T) {
	raw := "<|channel>thought\n사용자의 질문은 내가 한국어로 응답한 이유에 대한 것이며, 이전 응답에 대한 의문입니다.\n" +
		"<|channel>我目前沒有設定特定語言，但如果使用者在中文情境下提問，我會傾向用中文回覆。請問您希望我使用其他語言嗎？"

	content, reasoning, found := splitTaggedThinking(raw)

	if !found {
		t.Fatal("harmony channel markers were not recognised")
	}
	if strings.Contains(content, "<|") || strings.Contains(reasoning, "<|") {
		t.Fatalf("control tokens leaked into the output:\ncontent=%q\nreasoning=%q", content, reasoning)
	}
	if !strings.HasPrefix(content, "我目前沒有設定特定語言") {
		t.Fatalf("final answer = %q", content)
	}
	if !strings.Contains(reasoning, "사용자의 질문은") {
		t.Fatalf("reasoning = %q", reasoning)
	}
	if strings.Contains(content, "사용자의") {
		t.Fatalf("thinking leaked into the answer: %q", content)
	}
	if strings.Contains(reasoning, "thought") {
		t.Fatalf("channel name leaked into the reasoning: %q", reasoning)
	}
}

func TestSplitTaggedThinkingHandlesFullHarmonyEnvelope(t *testing.T) {
	raw := "<|start|>assistant<|channel|>analysis<|message|>我要先確認需求。<|end|>" +
		"<|start|>assistant<|channel|>final<|message|>共有 42 筆製令。<|return|>"

	content, reasoning, found := splitTaggedThinking(raw)

	if !found {
		t.Fatal("harmony envelope was not recognised")
	}
	if content != "共有 42 筆製令。" {
		t.Fatalf("content = %q", content)
	}
	if reasoning != "我要先確認需求。" {
		t.Fatalf("reasoning = %q", reasoning)
	}
	if strings.Contains(content, "assistant") || strings.Contains(reasoning, "assistant") {
		t.Fatalf("role name leaked:\ncontent=%q\nreasoning=%q", content, reasoning)
	}
}

// <think> 是原本就支援的格式，改動不能影響它。
func TestSplitTaggedThinkingStillHandlesThinkTags(t *testing.T) {
	content, reasoning, found := splitTaggedThinking("<think>先想一下</think>答案是 42。")
	if !found || content != "答案是 42。" || reasoning != "先想一下" {
		t.Fatalf("content=%q reasoning=%q found=%v", content, reasoning, found)
	}

	// 聊天模板吃掉開頭標籤時，前面那段仍然是思考內容。
	content, reasoning, found = splitTaggedThinking("先想一下</think>答案是 42。")
	if !found || content != "答案是 42。" || reasoning != "先想一下" {
		t.Fatalf("content=%q reasoning=%q found=%v", content, reasoning, found)
	}
}

func TestSplitTaggedThinkingLeavesPlainContentAlone(t *testing.T) {
	raw := "比較 a < b 與 c > d 的差別。"
	content, reasoning, found := splitTaggedThinking(raw)
	if found || content != raw || reasoning != "" {
		t.Fatalf("plain content was rewritten: content=%q reasoning=%q found=%v", content, reasoning, found)
	}
}

func collectStream(t *testing.T, chunks []string) (content, reasoning string) {
	t.Helper()
	stream := &taggedThinkingStream{}
	var contentBuilder, reasoningBuilder strings.Builder
	emit := func(fragments []thinkingFragment) {
		for _, fragment := range fragments {
			if fragment.Thinking {
				reasoningBuilder.WriteString(fragment.Text)
				continue
			}
			contentBuilder.WriteString(fragment.Text)
		}
	}
	for _, chunk := range chunks {
		emit(stream.Push(chunk, false))
	}
	emit(stream.Push("", true))
	return contentBuilder.String(), reasoningBuilder.String()
}

// 標記可能被切在任意 chunk 邊界上，串流路徑不能因此把控制 token 送到畫面。
func TestTaggedThinkingStreamSplitsHarmonyAcrossChunks(t *testing.T) {
	content, reasoning := collectStream(t, []string{
		"<|chan", "nel|>analy", "sis<|mess", "age|>先確認需求。", "<|channel|>fin", "al<|message|>共有 42 筆。",
	})

	if strings.Contains(content, "<|") || strings.Contains(reasoning, "<|") {
		t.Fatalf("control tokens leaked:\ncontent=%q\nreasoning=%q", content, reasoning)
	}
	if strings.TrimSpace(content) != "共有 42 筆。" {
		t.Fatalf("content = %q", content)
	}
	if strings.TrimSpace(reasoning) != "先確認需求。" {
		t.Fatalf("reasoning = %q", reasoning)
	}
	if strings.Contains(content, "analysis") || strings.Contains(content, "final") {
		t.Fatalf("channel name leaked into the answer: %q", content)
	}
}

func TestTaggedThinkingStreamStillSplitsThinkTags(t *testing.T) {
	content, reasoning := collectStream(t, []string{"<thi", "nk>先想", "一下</th", "ink>答案是 42。"})
	if strings.TrimSpace(content) != "答案是 42。" || strings.TrimSpace(reasoning) != "先想一下" {
		t.Fatalf("content=%q reasoning=%q", content, reasoning)
	}
}

// 內容裡的 '<' 很常見，不能因為它把輸出無限期卡住。
func TestTaggedThinkingStreamDoesNotHoldPlainAngleBrackets(t *testing.T) {
	content, reasoning := collectStream(t, []string{"比較 a < b", " 與 c > d。"})
	if reasoning != "" {
		t.Fatalf("plain text became reasoning: %q", reasoning)
	}
	if content != "比較 a < b 與 c > d。" {
		t.Fatalf("content = %q", content)
	}
}

// 整條 adapter 路徑：Provider 把 harmony 控制 token 留在 content 裡時，
// ModelResponse 交給 Harness 的必須已經是乾淨的答案與思考。
func TestDecodeJSONResponseSeparatesHarmonyChannels(t *testing.T) {
	payload := `{"model":"m","choices":[{"message":{"content":"<|channel>thought\n先確認需求。<|channel>共有 42 筆製令。"},"finish_reason":"stop"}]}`

	response, err := decodeJSONResponse(strings.NewReader(payload), "fallback", "req_1", "client_1", nil)
	if err != nil {
		t.Fatalf("decodeJSONResponse: %v", err)
	}
	if response.Content != "共有 42 筆製令。" {
		t.Fatalf("content = %q", response.Content)
	}
	if response.Reasoning != "先確認需求。" {
		t.Fatalf("reasoning = %q", response.Reasoning)
	}
}

// 模型常常只吐一半的 harmony：思考頻道結束後直接續寫答案，沒有補上
// <|channel|>final。那段文字必須落在回答，否則使用者看到的是空白回覆。
func TestSplitTaggedThinkingTreatsTextAfterEndAsTheAnswer(t *testing.T) {
	content, reasoning, found := splitTaggedThinking("<|channel>thought\n先想一下。<|end|>共有 42 筆製令。")
	if !found {
		t.Fatal("markers were not recognised")
	}
	if content != "共有 42 筆製令。" {
		t.Fatalf("content = %q", content)
	}
	if reasoning != "先想一下。" {
		t.Fatalf("reasoning = %q", reasoning)
	}
}
