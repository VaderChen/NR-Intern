package documents

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func createDOCX(ctx context.Context, request documentCreateRequest) (authoredDocument, error) {
	if strings.TrimSpace(request.Title) == "" && len(request.Blocks) == 0 {
		return authoredDocument{}, fmt.Errorf("DOCX requires title or at least one content block")
	}
	var body strings.Builder
	paragraphs := 0
	tables := 0
	if strings.TrimSpace(request.Title) != "" {
		body.WriteString(wordParagraph(request.Title, "Title", 0, 0))
		paragraphs++
	}
	for index, block := range request.Blocks {
		if err := ctx.Err(); err != nil {
			return authoredDocument{}, err
		}
		switch strings.ToLower(strings.TrimSpace(block.Type)) {
		case "heading":
			level := block.Level
			if level < 1 || level > 3 {
				level = 1
			}
			body.WriteString(wordParagraph(block.Text, "Heading"+strconv.Itoa(level), 0, 0))
			paragraphs++
		case "paragraph":
			body.WriteString(wordParagraph(block.Text, "Normal", 0, 0))
			paragraphs++
		case "bullet":
			level := block.Level - 1
			if level < 0 {
				level = 0
			}
			body.WriteString(wordParagraph(block.Text, "Normal", 1, level))
			paragraphs++
		case "numbered":
			level := block.Level - 1
			if level < 0 {
				level = 0
			}
			body.WriteString(wordParagraph(block.Text, "Normal", 2, level))
			paragraphs++
		case "table":
			if len(block.Rows) == 0 {
				return authoredDocument{}, fmt.Errorf("blocks[%d] table requires at least one row", index)
			}
			table, err := wordTable(block.Rows)
			if err != nil {
				return authoredDocument{}, fmt.Errorf("blocks[%d]: %w", index, err)
			}
			body.WriteString(table)
			tables++
		case "page_break":
			body.WriteString(`<w:p><w:r><w:br w:type="page"/></w:r></w:p>`)
			paragraphs++
		default:
			return authoredDocument{}, fmt.Errorf("blocks[%d].type must be heading, paragraph, bullet, numbered, table or page_break", index)
		}
	}
	body.WriteString(`<w:sectPr><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr>`)
	documentXML := []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body>` + body.String() + `</w:body></w:document>`)
	entries := map[string][]byte{
		"[Content_Types].xml":          docxContentTypes(),
		"_rels/.rels":                  rootRelationships("word/document.xml"),
		"docProps/core.xml":            officeCoreProperties(request.Title, request.Subject, request.Author),
		"docProps/app.xml":             docxAppProperties(paragraphs),
		"word/document.xml":            documentXML,
		"word/styles.xml":              docxStyles(),
		"word/numbering.xml":           docxNumbering(),
		"word/settings.xml":            docxSettings(),
		"word/_rels/document.xml.rels": docxDocumentRelationships(),
	}
	data, err := buildOpenXMLPackage(ctx, entries)
	if err != nil {
		return authoredDocument{}, err
	}
	return authoredDocument{Data: data, Details: map[string]any{"paragraph_count": paragraphs, "table_count": tables}}, nil
}

func wordParagraph(text, style string, numberingID, level int) string {
	var output strings.Builder
	output.WriteString(`<w:p><w:pPr>`)
	if style != "" {
		output.WriteString(`<w:pStyle w:val="` + xmlAttributeText(style) + `"/>`)
	}
	if numberingID > 0 {
		if level > 8 {
			level = 8
		}
		output.WriteString(`<w:numPr><w:ilvl w:val="` + strconv.Itoa(level) + `"/><w:numId w:val="` + strconv.Itoa(numberingID) + `"/></w:numPr>`)
	}
	output.WriteString(`</w:pPr>`)
	lines := strings.Split(validXMLText(text), "\n")
	for index, line := range lines {
		if index > 0 {
			output.WriteString(`<w:r><w:br/></w:r>`)
		}
		output.WriteString(`<w:r><w:t xml:space="preserve">` + xmlText(line) + `</w:t></w:r>`)
	}
	output.WriteString(`</w:p>`)
	return output.String()
}

func wordTable(rows [][]string) (string, error) {
	columns := 0
	for _, row := range rows {
		if len(row) > columns {
			columns = len(row)
		}
	}
	if columns == 0 {
		return "", fmt.Errorf("table rows require at least one cell")
	}
	widths := make([]int, columns)
	remaining := 9360
	for column := range widths {
		widths[column] = remaining / (columns - column)
		remaining -= widths[column]
	}
	var output strings.Builder
	output.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="9360" w:type="dxa"/><w:tblInd w:w="120" w:type="dxa"/><w:tblLayout w:type="fixed"/>`)
	output.WriteString(`<w:tblCellMar><w:top w:w="100" w:type="dxa"/><w:left w:w="120" w:type="dxa"/><w:bottom w:w="100" w:type="dxa"/><w:right w:w="120" w:type="dxa"/></w:tblCellMar>`)
	output.WriteString(`<w:tblBorders><w:top w:val="single" w:sz="4" w:color="B8C2CC"/><w:left w:val="single" w:sz="4" w:color="B8C2CC"/><w:bottom w:val="single" w:sz="4" w:color="B8C2CC"/><w:right w:val="single" w:sz="4" w:color="B8C2CC"/><w:insideH w:val="single" w:sz="4" w:color="D6DCE2"/><w:insideV w:val="single" w:sz="4" w:color="D6DCE2"/></w:tblBorders></w:tblPr><w:tblGrid>`)
	for _, width := range widths {
		output.WriteString(`<w:gridCol w:w="` + strconv.Itoa(width) + `"/>`)
	}
	output.WriteString(`</w:tblGrid>`)
	for rowIndex, row := range rows {
		output.WriteString(`<w:tr>`)
		if rowIndex == 0 {
			output.WriteString(`<w:trPr><w:tblHeader/></w:trPr>`)
		}
		for column := 0; column < columns; column++ {
			value := ""
			if column < len(row) {
				value = row[column]
			}
			output.WriteString(`<w:tc><w:tcPr><w:tcW w:w="` + strconv.Itoa(widths[column]) + `" w:type="dxa"/><w:vAlign w:val="center"/>`)
			if rowIndex == 0 {
				output.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="DCE6F1"/>`)
			}
			output.WriteString(`</w:tcPr><w:p><w:r>`)
			if rowIndex == 0 {
				output.WriteString(`<w:rPr><w:b/></w:rPr>`)
			}
			output.WriteString(`<w:t xml:space="preserve">` + xmlText(value) + `</w:t></w:r></w:p></w:tc>`)
		}
		output.WriteString(`</w:tr>`)
	}
	output.WriteString(`</w:tbl>`)
	return output.String(), nil
}

func docxContentTypes() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>` +
		`<Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/>` +
		`<Override PartName="/word/settings.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"/>` +
		`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>` +
		`<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/></Types>`)
}

func docxDocumentRelationships() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering" Target="numbering.xml"/>` +
		`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/settings" Target="settings.xml"/></Relationships>`)
}

func docxStyles() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:eastAsia="Microsoft JhengHei"/><w:sz w:val="22"/><w:szCs w:val="22"/><w:lang w:val="en-US" w:eastAsia="zh-TW"/></w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="276" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>` +
		wordStyle("Normal", "Normal", 22, false, "1F2933", 0, 120) +
		wordStyle("Title", "Title", 40, true, "172B4D", 0, 240) +
		wordStyle("Heading1", "Heading 1", 32, true, "1D4ED8", 240, 120) +
		wordStyle("Heading2", "Heading 2", 28, true, "1E3A5F", 200, 100) +
		wordStyle("Heading3", "Heading 3", 24, true, "334E68", 160, 80) + `</w:styles>`)
}

func wordStyle(id, name string, size int, bold bool, color string, before, after int) string {
	b := ""
	if bold {
		b = `<w:b/>`
	}
	basedOn := ""
	keepNext := ""
	if id != "Normal" {
		basedOn = `<w:basedOn w:val="Normal"/>`
		keepNext = `<w:keepNext/>`
	}
	return `<w:style w:type="paragraph" w:styleId="` + id + `"><w:name w:val="` + name + `"/>` + basedOn + `<w:qFormat/><w:pPr>` + keepNext + `<w:spacing w:before="` + strconv.Itoa(before) + `" w:after="` + strconv.Itoa(after) + `"/></w:pPr><w:rPr>` + b + `<w:color w:val="` + color + `"/><w:sz w:val="` + strconv.Itoa(size) + `"/><w:szCs w:val="` + strconv.Itoa(size) + `"/></w:rPr></w:style>`
}

func docxNumbering() []byte {
	var bullets strings.Builder
	var numbers strings.Builder
	for level := 0; level < 9; level++ {
		indent := 720 + level*360
		bullets.WriteString(`<w:lvl w:ilvl="` + strconv.Itoa(level) + `"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="•"/><w:lvlJc w:val="left"/><w:pPr><w:tabs><w:tab w:val="num" w:pos="` + strconv.Itoa(indent) + `"/></w:tabs><w:ind w:left="` + strconv.Itoa(indent) + `" w:hanging="360"/></w:pPr></w:lvl>`)
		numbers.WriteString(`<w:lvl w:ilvl="` + strconv.Itoa(level) + `"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%` + strconv.Itoa(level+1) + `."/><w:lvlJc w:val="left"/><w:pPr><w:tabs><w:tab w:val="num" w:pos="` + strconv.Itoa(indent) + `"/></w:tabs><w:ind w:left="` + strconv.Itoa(indent) + `" w:hanging="360"/></w:pPr></w:lvl>`)
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:abstractNum w:abstractNumId="0">` + bullets.String() + `</w:abstractNum><w:abstractNum w:abstractNumId="1">` + numbers.String() + `</w:abstractNum>` +
		`<w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num><w:num w:numId="2"><w:abstractNumId w:val="1"/></w:num></w:numbering>`)
}

func docxSettings() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:settings xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:zoom w:percent="100"/><w:defaultTabStop w:val="720"/><w:characterSpacingControl w:val="doNotCompress"/></w:settings>`)
}

func docxAppProperties(paragraphs int) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"><Application>NR-Intern</Application><DocSecurity>0</DocSecurity><ScaleCrop>false</ScaleCrop><Company></Company><LinksUpToDate>false</LinksUpToDate><SharedDoc>false</SharedDoc><HyperlinksChanged>false</HyperlinksChanged><AppVersion>1.0</AppVersion><Paragraphs>` + strconv.Itoa(paragraphs) + `</Paragraphs></Properties>`)
}
