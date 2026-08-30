package network

import (
	"strings"
	"testing"
)

func TestHTMLToTextKeepsParagraphBoundaries(t *testing.T) {
	title, text := htmlToText(`<html><head><title>  說明文件  </title></head>
<body><h1>安裝</h1><p>第一段。</p><ul><li>項目一</li><li>項目二</li></ul><p>第二段&amp;結尾&nbsp;。</p></body></html>`)

	if title != "說明文件" {
		t.Fatalf("title = %q", title)
	}
	for _, wanted := range []string{"安裝", "第一段。", "項目一", "項目二", "第二段&結尾"} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("text is missing %q: %q", wanted, text)
		}
	}
	if strings.Contains(text, "<") {
		t.Fatalf("markup was left behind: %q", text)
	}
	if strings.Contains(text, "\n\n\n") {
		t.Fatalf("blank lines were not collapsed: %q", text)
	}
}

// 每個要丟棄的元素各有一條規則，成對的內容不會吃到別的元素結尾。
func TestHTMLToTextDropsScriptAndStyleContent(t *testing.T) {
	_, text := htmlToText(`<style>.a{color:red}</style><p>可見</p><script>var secret = 1;</script><template><style>.b{}</style></template><p>也可見</p>`)

	for _, unwanted := range []string{"color:red", "secret", ".b{}"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("text still carries %q: %q", unwanted, text)
		}
	}
	if !strings.Contains(text, "可見") || !strings.Contains(text, "也可見") {
		t.Fatalf("visible text was dropped: %q", text)
	}
}

func TestHTMLToTextHandlesCommentsAndEntities(t *testing.T) {
	_, text := htmlToText(`<!-- 註解 --><p>a &lt; b &#39;c&#39; &#x41;</p>`)

	if strings.Contains(text, "註解") {
		t.Fatalf("comment was kept: %q", text)
	}
	if !strings.Contains(text, "a < b 'c' A") {
		t.Fatalf("entities were not decoded: %q", text)
	}
}
