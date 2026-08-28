package textutil

import "testing"

func TestNormalizeFullwidthASCIIPreservesNonASCIIText(t *testing.T) {
	input := "請建立　ＨＥＬＬＯ．ＭＤ，內容為：測試①"
	want := "請建立 HELLO.MD,內容為:測試①"
	if got := NormalizeFullwidthASCII(input); got != want {
		t.Fatalf("NormalizeFullwidthASCII() = %q, want %q", got, want)
	}
}

func TestNormalizeFullwidthASCIILeavesHalfwidthTextUnchanged(t *testing.T) {
	const input = "HELLO.MD 測試"
	if got := NormalizeFullwidthASCII(input); got != input {
		t.Fatalf("NormalizeFullwidthASCII() = %q, want unchanged input", got)
	}
}
