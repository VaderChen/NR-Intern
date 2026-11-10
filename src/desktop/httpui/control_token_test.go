package httpui

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestNewControlTokenFormatAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		token, err := newControlToken()
		if err != nil {
			t.Fatal(err)
		}
		suffix, ok := strings.CutPrefix(token, "desktop_")
		if !ok || len(suffix) != 24 {
			t.Fatalf("權杖格式不符：%q", token)
		}
		if _, err := hex.DecodeString(suffix); err != nil {
			t.Fatalf("權杖不是十六進位：%q", token)
		}
		if seen[token] {
			t.Fatalf("權杖重複：%q", token)
		}
		seen[token] = true
	}
}
