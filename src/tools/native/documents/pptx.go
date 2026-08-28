package documents

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type presentationSlide struct {
	Part  string
	Index int
}

func inspectPPTX(ctx context.Context, filePath string, base documentInspection) (documentInspection, error) {
	archive, err := openOfficeArchive(filePath)
	if err != nil {
		return documentInspection{}, err
	}
	defer archive.Close()
	slides, err := presentationSlides(archive)
	if err != nil {
		return documentInspection{}, err
	}
	base.Metadata = officeMetadata(archive)
	base.SlideCount = len(slides)
	base.PageCount = len(slides)
	base.Sections = make([]documentSection, 0, len(slides))
	for _, slide := range slides {
		if err := ctx.Err(); err != nil {
			return documentInspection{}, err
		}
		base.Sections = append(base.Sections, documentSection{
			Name: fmt.Sprintf("slide%d", slide.Index), Kind: "slide", Index: slide.Index,
		})
	}
	return base, nil
}

func readPPTX(ctx context.Context, filePath string, options readOptions) (documentReadResult, error) {
	archive, err := openOfficeArchive(filePath)
	if err != nil {
		return documentReadResult{}, err
	}
	defer archive.Close()
	slides, err := presentationSlides(archive)
	if err != nil {
		return documentReadResult{}, err
	}
	if len(slides) == 0 {
		return documentReadResult{}, fmt.Errorf("PPTX does not contain any slides")
	}
	startPage := options.StartPage
	if requested := strings.TrimSpace(options.Section); requested != "" {
		requested = strings.TrimPrefix(strings.ToLower(requested), "slide")
		if value, parseErr := strconv.Atoi(requested); parseErr == nil {
			startPage = value
		}
	}
	endPage := options.EndPage
	if endPage == 0 {
		endPage = startPage
	}
	if startPage < 1 || startPage > len(slides) {
		return documentReadResult{}, fmt.Errorf("start_page %d exceeds PPTX slide count %d", startPage, len(slides))
	}
	if endPage < startPage {
		return documentReadResult{}, fmt.Errorf("end_page must be greater than or equal to start_page")
	}
	if endPage > len(slides) {
		endPage = len(slides)
	}
	var output strings.Builder
	for index := startPage - 1; index < endPage; index++ {
		if err := ctx.Err(); err != nil {
			return documentReadResult{}, err
		}
		slide := slides[index]
		data, err := archive.read(slide.Part)
		if err != nil {
			return documentReadResult{}, err
		}
		paragraphs, err := extractPresentationParagraphs(ctx, data)
		if err != nil {
			return documentReadResult{}, fmt.Errorf("read PPTX slide %d: %w", slide.Index, err)
		}
		output.WriteString(fmt.Sprintf("[Slide %d/%d]\n", slide.Index, len(slides)))
		if len(paragraphs) == 0 {
			output.WriteString("[no extractable text]\n")
		} else {
			for _, paragraph := range paragraphs {
				output.WriteString(paragraph)
				output.WriteByte('\n')
			}
		}
		if index+1 < endPage {
			output.WriteByte('\n')
		}
	}
	return documentReadResult{
		Format:  string(formatPPTX),
		Content: output.String(),
		Details: map[string]any{
			"slide_count": len(slides),
			"start_page":  startPage,
			"end_page":    endPage,
		},
	}, nil
}

func presentationSlides(archive *officeArchive) ([]presentationSlide, error) {
	presentation, err := archive.read("ppt/presentation.xml")
	if err != nil {
		return nil, err
	}
	relationships, err := archive.read("ppt/_rels/presentation.xml.rels")
	if err != nil {
		return nil, err
	}
	targets := relationshipTargets(relationships, "ppt")
	decoder := xml.NewDecoder(bytes.NewReader(presentation))
	result := []presentationSlide{}
	for {
		token, decodeErr := decoder.Token()
		if decodeErr == io.EOF {
			break
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("parse PPTX presentation: %w", decodeErr)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "sldId" {
			continue
		}
		part := targets[xmlRelationshipID(start)]
		if part == "" || !archive.has(part) {
			continue
		}
		result = append(result, presentationSlide{Part: part, Index: len(result) + 1})
	}
	return result, nil
}

func extractPresentationParagraphs(ctx context.Context, data []byte) ([]string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	result := []string{}
	var paragraph strings.Builder
	inParagraph := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			return result, nil
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
			case "t":
				if !inParagraph {
					continue
				}
				var value string
				if err := decoder.DecodeElement(&value, &typed); err != nil {
					return nil, err
				}
				paragraph.WriteString(value)
			case "br":
				if inParagraph {
					paragraph.WriteByte('\n')
				}
			}
		case xml.EndElement:
			if typed.Name.Local == "p" {
				if value := strings.TrimSpace(paragraph.String()); value != "" {
					result = append(result, value)
				}
				inParagraph = false
			}
		}
	}
}
