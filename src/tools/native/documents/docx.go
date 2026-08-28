package documents

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

func inspectDOCX(ctx context.Context, filePath string, base documentInspection) (documentInspection, error) {
	archive, err := openOfficeArchive(filePath)
	if err != nil {
		return documentInspection{}, err
	}
	defer archive.Close()
	sections := docxSections(archive)
	body, err := archive.read("word/document.xml")
	if err != nil {
		return documentInspection{}, err
	}
	paragraphs, err := extractWordParagraphs(ctx, body)
	if err != nil {
		return documentInspection{}, fmt.Errorf("inspect DOCX body: %w", err)
	}
	base.ParagraphCount = len(paragraphs)
	base.Metadata = officeMetadata(archive)
	base.Sections = make([]documentSection, 0, len(sections))
	for index, section := range sections {
		base.Sections = append(base.Sections, documentSection{Name: section.name, Kind: section.kind, Index: index + 1})
	}
	return base, nil
}

func readDOCX(ctx context.Context, filePath string, options readOptions) (documentReadResult, error) {
	archive, err := openOfficeArchive(filePath)
	if err != nil {
		return documentReadResult{}, err
	}
	defer archive.Close()
	sections := docxSections(archive)
	requested := strings.ToLower(strings.TrimSpace(options.Section))
	if requested == "" {
		requested = "body"
	}
	selected := docxSection{}
	for _, section := range sections {
		if strings.EqualFold(section.name, requested) || strings.EqualFold(section.part, requested) {
			selected = section
			break
		}
	}
	if selected.part == "" {
		names := make([]string, 0, len(sections))
		for _, section := range sections {
			names = append(names, section.name)
		}
		return documentReadResult{}, fmt.Errorf("DOCX section %q not found; available sections: %s", options.Section, strings.Join(names, ", "))
	}
	data, err := archive.read(selected.part)
	if err != nil {
		return documentReadResult{}, err
	}
	paragraphs, err := extractWordParagraphs(ctx, data)
	if err != nil {
		return documentReadResult{}, fmt.Errorf("read DOCX section %s: %w", selected.name, err)
	}
	start := options.StartParagraph
	end := options.EndParagraph
	if end == 0 {
		end = start + 199
	}
	if len(paragraphs) == 0 {
		return documentReadResult{
			Format:  string(formatDOCX),
			Content: fmt.Sprintf("[Word section: %s]\n[no extractable text]\n", selected.name),
			Details: map[string]any{"section": selected.name, "paragraph_count": 0},
		}, nil
	}
	if start > len(paragraphs) {
		return documentReadResult{}, fmt.Errorf("start_paragraph %d exceeds section paragraph count %d", start, len(paragraphs))
	}
	if end < start {
		return documentReadResult{}, fmt.Errorf("end_paragraph must be greater than or equal to start_paragraph")
	}
	if end > len(paragraphs) {
		end = len(paragraphs)
	}
	var output strings.Builder
	output.WriteString(fmt.Sprintf("[Word section: %s; paragraphs %d-%d/%d]\n", selected.name, start, end, len(paragraphs)))
	for index := start - 1; index < end; index++ {
		output.WriteString(fmt.Sprintf("P%d: %s\n", index+1, paragraphs[index]))
	}
	return documentReadResult{
		Format:  string(formatDOCX),
		Content: output.String(),
		Details: map[string]any{
			"section":         selected.name,
			"paragraph_count": len(paragraphs),
			"start_paragraph": start,
			"end_paragraph":   end,
		},
	}, nil
}

type docxSection struct {
	name string
	kind string
	part string
}

func docxSections(archive *officeArchive) []docxSection {
	sections := []docxSection{{name: "body", kind: "document", part: "word/document.xml"}}
	groups := []struct {
		prefix string
		kind   string
	}{
		{prefix: "word/header", kind: "header"},
		{prefix: "word/footer", kind: "footer"},
	}
	for _, group := range groups {
		for _, name := range archive.names(group.prefix) {
			if path.Ext(name) != ".xml" {
				continue
			}
			sections = append(sections, docxSection{name: strings.TrimSuffix(path.Base(name), ".xml"), kind: group.kind, part: name})
		}
	}
	for _, item := range []docxSection{
		{name: "footnotes", kind: "notes", part: "word/footnotes.xml"},
		{name: "endnotes", kind: "notes", part: "word/endnotes.xml"},
		{name: "comments", kind: "comments", part: "word/comments.xml"},
	} {
		if archive.has(item.part) {
			sections = append(sections, item)
		}
	}
	sort.SliceStable(sections[1:], func(i, j int) bool { return sections[i+1].name < sections[j+1].name })
	return sections
}

func extractWordParagraphs(ctx context.Context, data []byte) ([]string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	result := []string{}
	var paragraph strings.Builder
	paragraphStyle := ""
	inParagraph := false
	inCell := false
	cellParagraphs := []string{}
	rowCells := []string{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			switch typed.Name.Local {
			case "p":
				inParagraph = true
				paragraph.Reset()
				paragraphStyle = ""
			case "pStyle":
				if inParagraph {
					paragraphStyle = xmlAttribute(typed, "val")
				}
			case "tc":
				inCell = true
				cellParagraphs = nil
			case "tr":
				rowCells = nil
			case "t", "delText", "instrText":
				if !inParagraph {
					continue
				}
				var value string
				if err := decoder.DecodeElement(&value, &typed); err != nil {
					return nil, err
				}
				paragraph.WriteString(value)
			case "tab":
				if inParagraph {
					paragraph.WriteByte('\t')
				}
			case "br", "cr":
				if inParagraph {
					paragraph.WriteByte('\n')
				}
			}
		case xml.EndElement:
			switch typed.Name.Local {
			case "p":
				value := strings.TrimSpace(paragraph.String())
				if value != "" {
					if paragraphStyle != "" {
						value = "[" + paragraphStyle + "] " + value
					}
					if inCell {
						cellParagraphs = append(cellParagraphs, value)
					} else {
						result = append(result, value)
					}
				}
				inParagraph = false
			case "tc":
				rowCells = append(rowCells, strings.Join(cellParagraphs, " / "))
				inCell = false
			case "tr":
				if value := strings.TrimSpace(strings.Join(rowCells, "\t")); value != "" {
					result = append(result, value)
				}
			}
		}
	}
	return result, nil
}

func xmlAttribute(element xml.StartElement, localName string) string {
	for _, attribute := range element.Attr {
		if attribute.Name.Local == localName {
			return attribute.Value
		}
	}
	return ""
}

func xmlRelationshipID(element xml.StartElement) string {
	fallback := ""
	for _, attribute := range element.Attr {
		if attribute.Name.Local != "id" {
			continue
		}
		fallback = attribute.Value
		if attribute.Name.Space != "" {
			return attribute.Value
		}
	}
	return fallback
}
