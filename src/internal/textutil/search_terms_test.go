package textutil

import (
	"math/rand"
	"slices"
	"strings"
	"testing"
	"unicode"
)

// 舊版 rune 分詞器保留作參考，驗證詞彙順序、CJK 單字與 bigram 均相同。
func referenceSearchTerms(text string) []string {
	var terms []string
	var word, cjk []rune
	flushWord := func() {
		if len(word) >= 2 {
			terms = append(terms, string(word))
		}
		word = word[:0]
	}
	flushCJK := func() {
		if len(cjk) == 1 {
			terms = append(terms, string(cjk))
		}
		for i := 0; i+1 < len(cjk); i++ {
			terms = append(terms, string(cjk[i:i+2]))
		}
		cjk = cjk[:0]
	}
	for _, value := range strings.ToLower(text) {
		switch {
		case unicode.Is(unicode.Han, value) || unicode.Is(unicode.Hiragana, value) || unicode.Is(unicode.Katakana, value) || unicode.Is(unicode.Hangul, value):
			flushWord()
			cjk = append(cjk, value)
		case unicode.IsLetter(value) || unicode.IsDigit(value):
			flushCJK()
			word = append(word, value)
		default:
			flushWord()
			flushCJK()
		}
	}
	flushWord()
	flushCJK()
	return terms
}

func TestSearchTermsCompatibilitySmoke(t *testing.T) {
	inputs := []string{"", "中", "中文", "中文字", "A 中B中文 C12", "ÜBER Éé İİ ßΩ", "한글カタカナあいうえ", "甲\xff\xfe乙", strings.Repeat("中文漢字", 1024), strings.Repeat("A", 8192)}
	rng := rand.New(rand.NewSource(719))
	alphabet := []string{"a", "B", "9", "中", "文", "あ", "한", "é", "Ω", "🙂", "_", " ", "\t", "\u2003", "\xff"}
	for range 500 {
		var text strings.Builder
		for range rng.Intn(128) {
			text.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		inputs = append(inputs, text.String())
	}
	for _, input := range inputs {
		var got []string
		VisitSearchTerms(input, func(term string) { got = append(got, term) })
		if want := referenceSearchTerms(input); !slices.Equal(got, want) {
			t.Fatalf("分詞不同：%q\n實際=%q\n預期=%q", input, got, want)
		}
	}
}
