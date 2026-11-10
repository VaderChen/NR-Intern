package textutil

import (
	"strings"
	"unicode/utf8"
)

// TruncateMiddle 保留開頭與結尾，marker 也計入 rune 上限。
// 不先配置整份 []rune；掃描／配置成本由保留的長度決定，而不是原始工具輸出的大小。
// 截斷結果擁有自己的字串，不會因小段切片持續保留大型原文。
func TruncateMiddle(value string, maxRunes int, marker string) string {
	if maxRunes <= 0 {
		return value
	}
	end := runePrefixEnd(value, maxRunes)
	if end == len(value) {
		return value
	}
	available := maxRunes - utf8.RuneCountInString(marker)
	if available <= 0 {
		return strings.Clone(validRuneString(value[:end]))
	}
	head := available * 2 / 3
	tail := available - head
	start := len(value)
	for range tail {
		_, size := utf8.DecodeLastRuneInString(value[:start])
		start -= size
	}
	return validRuneString(value[:runePrefixEnd(value, head)]) + marker + validRuneString(value[start:])
}

func runePrefixEnd(value string, count int) int {
	for index := range value {
		if count == 0 {
			return index
		}
		count--
	}
	return len(value)
}

func validRuneString(value string) string {
	if utf8.ValidString(value) {
		return value
	}
	// 與原本 []rune 的行為一致：每個無效 byte 各自成為 RuneError。
	return string([]rune(value))
}
