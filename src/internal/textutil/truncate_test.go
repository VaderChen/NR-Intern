package textutil

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func referenceTruncateMiddle(value string, maximum int, marker string) string {
	runes := []rune(value)
	if maximum <= 0 || len(runes) <= maximum {
		return value
	}
	remaining := maximum - len([]rune(marker))
	if remaining <= 0 {
		return string(runes[:maximum])
	}
	head := remaining * 2 / 3
	return string(runes[:head]) + marker + string(runes[len(runes)-(remaining-head):])
}

func TestTruncateMiddleCompatibilitySmoke(t *testing.T) {
	samples := []string{"", "a", "繁體中文與 😀 emoji", "a\xff\xfe中\xff末尾", strings.Repeat("長內容🙂abc", 100)}
	random := rand.New(rand.NewPCG(1, 2))
	units := []string{"a", "中", "🙂", "\u0301", "\xff", "\xe4\xb8", "\n", " "}
	for range 250 {
		var value strings.Builder
		for range random.IntN(120) {
			value.WriteString(units[random.IntN(len(units))])
		}
		samples = append(samples, value.String())
	}
	for _, sample := range samples {
		for maximum := -1; maximum < 80; maximum++ {
			for _, marker := range []string{"", "…", "\n…[content omitted]…\n"} {
				got, want := TruncateMiddle(sample, maximum, marker), referenceTruncateMiddle(sample, maximum, marker)
				if got != want {
					t.Fatalf("maximum=%d sample=%q got=%q want=%q", maximum, sample, got, want)
				}
			}
		}
	}
}
