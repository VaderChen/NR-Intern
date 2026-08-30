package network

import (
	"html"
	"regexp"
	"strings"
)

// RE2 沒有反向參考，因此每個要整段丟棄的元素各自編譯一條規則，
// 避免用單一規則配對到別的元素結尾而吃掉正文。
var htmlDroppedElements = compileDroppedElements("script", "style", "noscript", "template", "svg", "canvas")

var (
	htmlComments      = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlTitle         = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</\s*title\s*>`)
	htmlBlockBoundary = regexp.MustCompile(`(?i)</?\s*(p|div|section|article|header|footer|nav|main|aside|ul|ol|li|dl|dt|dd|table|thead|tbody|tr|h[1-6]|blockquote|pre|figure|figcaption|form|hr|br)\b[^>]*>`)
	htmlAnyTag        = regexp.MustCompile(`(?s)<[^>]*>`)
	htmlBlankLines    = regexp.MustCompile(`\n{3,}`)
	htmlInlineSpaces  = regexp.MustCompile(`[ \t\x{00a0}]+`)
)

// htmlToText 把 HTML 轉成模型可讀的純文字。
//
// 網頁原始碼裡多數位元組是標記、腳本與樣式；直接塞進上下文只會浪費預算又難以閱讀。
// 這裡不引入 HTML 解析器：抽取需求只到「移除標記、保留段落邊界」，正則已足夠，
// 也避免為了一個工具增加相依套件。
func htmlToText(source string) (title string, text string) {
	if match := htmlTitle.FindStringSubmatch(source); len(match) == 2 {
		title = collapseText(html.UnescapeString(htmlAnyTag.ReplaceAllString(match[1], " ")))
	}
	cleaned := source
	for _, pattern := range htmlDroppedElements {
		cleaned = pattern.ReplaceAllString(cleaned, "\n")
	}
	cleaned = htmlComments.ReplaceAllString(cleaned, "\n")
	cleaned = htmlBlockBoundary.ReplaceAllString(cleaned, "\n")
	cleaned = htmlAnyTag.ReplaceAllString(cleaned, " ")
	cleaned = html.UnescapeString(cleaned)
	lines := strings.Split(cleaned, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		kept = append(kept, strings.TrimSpace(htmlInlineSpaces.ReplaceAllString(line, " ")))
	}
	text = strings.TrimSpace(htmlBlankLines.ReplaceAllString(strings.Join(kept, "\n"), "\n\n"))
	return title, text
}

func compileDroppedElements(names ...string) []*regexp.Regexp {
	patterns := make([]*regexp.Regexp, 0, len(names))
	for _, name := range names {
		patterns = append(patterns, regexp.MustCompile(`(?is)<`+name+`\b[^>]*>.*?<\s*/\s*`+name+`\s*>`))
	}
	return patterns
}

func collapseText(value string) string {
	return strings.TrimSpace(htmlInlineSpaces.ReplaceAllString(strings.ReplaceAll(value, "\n", " "), " "))
}
