package domain

import "encoding/json"

// ModelContinuation 只保存 Provider 回傳的不透明推理狀態，不來自工具或使用者參數。
// 與訊息一起保留／壓縮；Scope 限制只能回送原 Provider 設定實例。
type ModelContinuation struct {
	Protocol string            `json:"protocol"`
	Scope    string            `json:"scope"`
	Model    string            `json:"model"`
	Items    []json.RawMessage `json:"items"`
}

func (s *ModelContinuation) Clone() *ModelContinuation {
	if s == nil {
		return nil
	}
	result := *s
	result.Items = make([]json.RawMessage, len(s.Items))
	for i, item := range s.Items {
		result.Items[i] = append(json.RawMessage(nil), item...)
	}
	return &result
}

func (s *ModelContinuation) Bytes() int {
	if s == nil {
		return 0
	}
	count := 0
	for _, item := range s.Items {
		count += len(item)
	}
	return count
}
