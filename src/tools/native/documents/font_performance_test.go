package documents

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"
)

func syntheticPerformanceFont(t testing.TB, size int64) fontCandidate {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic.ttf")
	if err := os.WriteFile(path, goregular.TTF, 0o600); err != nil {
		t.Fatal(err)
	}
	if size > int64(len(goregular.TTF)) {
		if err := os.Truncate(path, size); err != nil {
			t.Fatal(err)
		}
	}
	return fontCandidate{Path: path, Scope: "synthetic"}
}

func BenchmarkPerformanceFontCoverage(b *testing.B) {
	font := syntheticPerformanceFont(b, 32*1024*1024)
	characters := []rune("Synthetic Font Coverage")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		values, err := inspectFontCandidate(font, characters)
		if err != nil || len(values) != 1 || !values[0].Complete {
			b.Fatalf("字型覆蓋=%+v err=%v", values, err)
		}
	}
}

func TestFontReaderAtCoverageSmoke(t *testing.T) {
	font := syntheticPerformanceFont(t, 32*1024*1024)
	characters := []rune("Ab中🙂")
	values, err := inspectFontCandidate(font, characters)
	if err != nil || len(values) != 1 {
		t.Fatalf("字型讀取=%+v err=%v", values, err)
	}
	collection, err := sfnt.ParseCollection(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := collection.Font(0)
	if err != nil {
		t.Fatal(err)
	}
	var buffer sfnt.Buffer
	name, _ := reference.Name(&buffer, sfnt.NameIDFull)
	covered := 0
	for _, character := range characters {
		glyph, err := reference.GlyphIndex(&buffer, character)
		if err == nil && glyph != 0 {
			covered++
		}
	}
	if values[0].Name != name || values[0].CoveredRunes != covered || values[0].Complete != (covered == len(characters)) || values[0].RequiredRunes != len(characters) {
		t.Fatalf("字型覆蓋結果改變：%+v", values[0])
	}
	if err := os.Truncate(font.Path, maxFontFileBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectFontCandidate(font, characters); err == nil {
		t.Fatal("未在讀取前拒絕超出上限的字型")
	}
	if _, err := inspectFontCandidate(fontCandidate{Path: t.TempDir()}, characters); err == nil {
		t.Fatal("未拒絕非一般檔案")
	}
}
