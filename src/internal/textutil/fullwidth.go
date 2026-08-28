package textutil

import "strings"

// NormalizeFullwidthASCII 將全形 ASCII 可見字元與全形空白轉成半形。
// 只處理 U+FF01–U+FF5E 與 U+3000，避免 NFKC 連帶改寫中文相容字、數學符號或其他語意。
func NormalizeFullwidthASCII(value string) string {
	if value == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(value))
	for _, character := range value {
		switch {
		case character == '\u3000':
			builder.WriteByte(' ')
		case character >= '\uFF01' && character <= '\uFF5E':
			builder.WriteRune(character - 0xFEE0)
		default:
			builder.WriteRune(character)
		}
	}
	return builder.String()
}
