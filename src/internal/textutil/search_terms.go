package textutil

import (
	"strings"
	"unicode"
)

// VisitSearchTerms 依順序送出小寫詞彙：非 CJK 連續英數需至少兩字，
// CJK 連續文字取 bigram，單一 CJK 字也保留。回呼取得的子字串可能
// 共用輸入儲存；需要長期保存的呼叫端應只對實際新增的詞使用 strings.Clone。
func VisitSearchTerms(text string, visit func(string)) {
	text = strings.ToLower(text)
	wordStart, wordLength := -1, 0
	cjkStart, cjkLength, previous := -1, 0, 0
	flush := func(end int) {
		if wordLength >= 2 {
			visit(text[wordStart:end])
		}
		if cjkLength == 1 {
			visit(text[cjkStart:end])
		} else if cjkLength > 1 {
			visit(text[previous:end])
		}
		wordStart, wordLength, cjkStart, cjkLength = -1, 0, -1, 0
	}
	for index, value := range text {
		switch {
		case isSearchCJK(value):
			if wordLength > 0 {
				flush(index)
			}
			if cjkLength == 0 {
				cjkStart = index
			} else {
				if cjkLength >= 2 {
					visit(text[previous:index])
				}
				previous = cjkStart
				cjkStart = index
			}
			cjkLength++
		case unicode.IsLetter(value) || unicode.IsDigit(value):
			if cjkLength > 0 {
				flush(index)
			}
			if wordLength == 0 {
				wordStart = index
			}
			wordLength++
		default:
			flush(index)
		}
	}
	flush(len(text))
}

func isSearchCJK(value rune) bool {
	// ASCII 不需逐一查詢 Unicode 腳本表；DEL 不屬於任何 CJK 腳本。
	return value > unicode.MaxASCII && (unicode.Is(unicode.Han, value) ||
		unicode.Is(unicode.Hiragana, value) || unicode.Is(unicode.Katakana, value) ||
		unicode.Is(unicode.Hangul, value))
}
