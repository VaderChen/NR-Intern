package documents

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
)

type officeArchive struct {
	reader  *zip.ReadCloser
	entries map[string]*zip.File
}

func detectDocumentFormat(filePath string) (documentFormat, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open document: %w", err)
	}
	header := make([]byte, 8)
	count, readErr := io.ReadFull(file, header)
	_ = file.Close()
	if readErr != nil && readErr != io.ErrUnexpectedEOF {
		return "", fmt.Errorf("read document header: %w", readErr)
	}
	header = header[:count]
	if bytes.HasPrefix(header, []byte("%PDF-")) {
		return formatPDF, nil
	}
	if !bytes.HasPrefix(header, []byte("PK\x03\x04")) && !bytes.HasPrefix(header, []byte("PK\x05\x06")) {
		return "", fmt.Errorf("unsupported office document; supported formats are PDF, DOCX, XLSX and PPTX (legacy DOC/XLS files are not supported)")
	}
	archive, err := openOfficeArchive(filePath)
	if err != nil {
		return "", err
	}
	defer archive.Close()
	switch {
	case archive.has("word/document.xml"):
		return formatDOCX, nil
	case archive.has("xl/workbook.xml"):
		return formatXLSX, nil
	case archive.has("ppt/presentation.xml"):
		return formatPPTX, nil
	default:
		return "", fmt.Errorf("unsupported ZIP document; expected a DOCX, XLSX or PPTX Open XML package")
	}
}

func openOfficeArchive(filePath string) (*officeArchive, error) {
	reader, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, fmt.Errorf("open Open XML package: %w", err)
	}
	entries := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		name := strings.TrimPrefix(path.Clean(strings.ReplaceAll(file.Name, "\\", "/")), "./")
		entries[name] = file
	}
	return &officeArchive{reader: reader, entries: entries}, nil
}

func (a *officeArchive) Close() error {
	if a == nil || a.reader == nil {
		return nil
	}
	return a.reader.Close()
}

func (a *officeArchive) has(name string) bool {
	_, exists := a.entries[path.Clean(name)]
	return exists
}

func (a *officeArchive) names(prefix string) []string {
	result := []string{}
	for name := range a.entries {
		if strings.HasPrefix(name, prefix) {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

func (a *officeArchive) read(name string) ([]byte, error) {
	file := a.entries[path.Clean(name)]
	if file == nil {
		return nil, fmt.Errorf("Open XML part not found: %s", name)
	}
	if file.UncompressedSize64 > uint64(maxExpandedEntryBytes) {
		return nil, fmt.Errorf("Open XML part %s exceeds the %d byte expanded-size limit", name, maxExpandedEntryBytes)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open Open XML part %s: %w", name, err)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxExpandedEntryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Open XML part %s: %w", name, err)
	}
	if int64(len(data)) > maxExpandedEntryBytes {
		return nil, fmt.Errorf("Open XML part %s exceeds the expanded-size limit", name)
	}
	return data, nil
}

func officeMetadata(archive *officeArchive) map[string]string {
	metadata := map[string]string{}
	for _, name := range []string{"docProps/core.xml", "docProps/app.xml"} {
		data, err := archive.read(name)
		if err != nil {
			continue
		}
		decoder := xml.NewDecoder(bytes.NewReader(data))
		for {
			token, decodeErr := decoder.Token()
			if decodeErr == io.EOF {
				break
			}
			if decodeErr != nil {
				break
			}
			start, ok := token.(xml.StartElement)
			if !ok || !metadataElement(start.Name.Local) {
				continue
			}
			var value string
			if decodeErr := decoder.DecodeElement(&value, &start); decodeErr == nil {
				if value = strings.TrimSpace(value); value != "" {
					metadata[start.Name.Local] = value
				}
			}
		}
	}
	return metadata
}

func metadataElement(name string) bool {
	switch strings.ToLower(name) {
	case "title", "subject", "creator", "keywords", "description", "lastmodifiedby", "created", "modified", "application", "company", "manager", "pages", "words", "characters", "slides":
		return true
	default:
		return false
	}
}

func relationshipTargets(data []byte, baseDirectory string) map[string]string {
	result := map[string]string{}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			return result
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Relationship" {
			continue
		}
		id, target := "", ""
		for _, attribute := range start.Attr {
			switch attribute.Name.Local {
			case "Id":
				id = attribute.Value
			case "Target":
				target = attribute.Value
			}
		}
		if id == "" || target == "" || strings.Contains(target, "://") {
			continue
		}
		if strings.HasPrefix(target, "/") {
			target = strings.TrimPrefix(path.Clean(target), "/")
		} else {
			target = path.Clean(path.Join(baseDirectory, target))
		}
		result[id] = target
	}
}
