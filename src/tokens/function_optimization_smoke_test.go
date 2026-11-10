package tokens

import (
	"math"
	"strings"
	"testing"
	"unicode"
)

func referenceFunctionWeight(c HeuristicCounter, text string) float64 {
	total := 0.0
	for _, value := range text {
		switch {
		case isCJK(value):
			total += c.CJKTokensPerRune
		case value < unicode.MaxASCII:
			total += c.ASCIITokensPerRune
		default:
			total += c.OtherTokensPerRune
		}
	}
	return total
}

func TestFunctionTokenWeightCompatibilitySmoke(t *testing.T) {
	text := strings.Repeat("ASCII\x00\x7f\xff繁體中文全形，日文かな한글é🙂𠀀", 512)
	for _, counter := range []*HeuristicCounter{nil, {}, NewHeuristicCounter(), {CJKTokensPerRune: .1, ASCIITokensPerRune: .7, OtherTokensPerRune: .3}} {
		want := int(math.Ceil(referenceFunctionWeight(counter.normalized(), text)))
		if got := counter.EstimateText(text); got != want {
			t.Fatalf("估算或浮點累加次序不同：%d != %d", got, want)
		}
	}
}

func BenchmarkFunctionTokenWeightReference(b *testing.B) {
	for _, input := range []struct{ name, value string }{
		{"ASCII", strings.Repeat("synthetic tool output ", 4096)},
		{"CJK", strings.Repeat("合成文字工具輸出", 4096)},
		{"Mixed", strings.Repeat("synthetic 合成🙂é output ", 4096)},
	} {
		b.Run(input.name, func(b *testing.B) {
			counter := NewHeuristicCounter().normalized()
			b.ReportAllocs()
			for b.Loop() {
				_ = referenceFunctionWeight(counter, input.value)
			}
		})
	}
}
