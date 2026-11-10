package httpui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// newControlToken 產生本機 UI 控制 cookie 的權杖。
//
// 桌面程式與後端只透過 HTTP 溝通，不 import 後端的 domain 套件；格式沿用原本的
// "desktop_" 加 24 個十六進位字元。這是驗證用的祕密，亂數來源不可用時回報錯誤，
// 不退回可預測的時間戳。
func newControlToken() (string, error) {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate desktop control token: %w", err)
	}
	return "desktop_" + hex.EncodeToString(buffer), nil
}
