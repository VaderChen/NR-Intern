package openaicompat

import "encoding/json"

func validCodexReasoning(raw json.RawMessage) bool {
	var item struct {
		Type             string `json:"type"`
		EncryptedContent string `json:"encrypted_content"`
	}
	return len(raw) <= 8*1024*1024 && json.Unmarshal(raw, &item) == nil && item.Type == "reasoning" && item.EncryptedContent != ""
}

func captureCodexReasoning(state *codexStreamState, index int, item any) {
	raw, err := json.Marshal(item)
	if err != nil || !validCodexReasoning(raw) {
		return
	}
	if state.reasoningItems == nil {
		state.reasoningItems = map[int]json.RawMessage{}
	}
	state.reasoningItems[index] = raw
}
