package domain

import "errors"

var (
	ErrInvalidInput = errors.New("invalid input")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrCanceled     = errors.New("canceled")
	// ErrProviderProtocol 表示 Provider 有回應，但未遵循 Harness 所需的原生協定。
	// 這類錯誤無法靠重送相同 Run 修復，必須調整 Provider、模型或代理轉換器。
	ErrProviderProtocol = errors.New("provider protocol incompatible")
)
