package documents

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"
)

const maxExpandedPackageBytes = int64(256 * 1024 * 1024)

func buildOpenXMLPackage(ctx context.Context, entries map[string][]byte) ([]byte, error) {
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			_ = archive.Close()
			return nil, err
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o640)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			_ = archive.Close()
			return nil, fmt.Errorf("create Open XML part %s: %w", name, err)
		}
		if _, err := writer.Write(entries[name]); err != nil {
			_ = archive.Close()
			return nil, fmt.Errorf("write Open XML part %s: %w", name, err)
		}
	}
	if err := archive.Close(); err != nil {
		return nil, fmt.Errorf("close Open XML package: %w", err)
	}
	return output.Bytes(), nil
}

func rewriteOpenXMLPackage(ctx context.Context, sourcePath string, updates map[string][]byte) ([]byte, error) {
	reader, err := zip.OpenReader(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("open Open XML package: %w", err)
	}
	defer reader.Close()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	written := map[string]struct{}{}
	expandedBytes := int64(0)
	for _, source := range reader.File {
		if err := ctx.Err(); err != nil {
			_ = archive.Close()
			return nil, err
		}
		name := strings.TrimPrefix(strings.ReplaceAll(source.Name, "\\", "/"), "./")
		data, replace := updates[name]
		if !replace {
			if source.UncompressedSize64 > uint64(maxExpandedEntryBytes) {
				_ = archive.Close()
				return nil, fmt.Errorf("Open XML part %s exceeds the expanded-size limit", name)
			}
			input, openErr := source.Open()
			if openErr != nil {
				_ = archive.Close()
				return nil, fmt.Errorf("open Open XML part %s: %w", name, openErr)
			}
			data, err = io.ReadAll(io.LimitReader(input, maxExpandedEntryBytes+1))
			_ = input.Close()
			if err != nil {
				_ = archive.Close()
				return nil, fmt.Errorf("read Open XML part %s: %w", name, err)
			}
			if int64(len(data)) > maxExpandedEntryBytes {
				_ = archive.Close()
				return nil, fmt.Errorf("Open XML part %s exceeds the expanded-size limit", name)
			}
		}
		expandedBytes += int64(len(data))
		if expandedBytes > maxExpandedPackageBytes {
			_ = archive.Close()
			return nil, fmt.Errorf("Open XML package exceeds the %d byte expanded-size limit", maxExpandedPackageBytes)
		}
		header := source.FileHeader
		header.Name = name
		header.Flags &^= 0x1
		writer, createErr := archive.CreateHeader(&header)
		if createErr != nil {
			_ = archive.Close()
			return nil, fmt.Errorf("create Open XML part %s: %w", name, createErr)
		}
		if _, writeErr := writer.Write(data); writeErr != nil {
			_ = archive.Close()
			return nil, fmt.Errorf("write Open XML part %s: %w", name, writeErr)
		}
		written[name] = struct{}{}
	}
	added := make([]string, 0)
	for name := range updates {
		if _, exists := written[name]; !exists {
			added = append(added, name)
		}
	}
	sort.Strings(added)
	for _, name := range added {
		expandedBytes += int64(len(updates[name]))
		if expandedBytes > maxExpandedPackageBytes {
			_ = archive.Close()
			return nil, fmt.Errorf("Open XML package exceeds the %d byte expanded-size limit", maxExpandedPackageBytes)
		}
		writer, createErr := archive.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if createErr != nil {
			_ = archive.Close()
			return nil, fmt.Errorf("create Open XML part %s: %w", name, createErr)
		}
		if _, writeErr := writer.Write(updates[name]); writeErr != nil {
			_ = archive.Close()
			return nil, fmt.Errorf("write Open XML part %s: %w", name, writeErr)
		}
	}
	if err := archive.Close(); err != nil {
		return nil, fmt.Errorf("close Open XML package: %w", err)
	}
	return output.Bytes(), nil
}

func readRegularFile(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("path is not a regular file")
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("file exceeds the %d byte safety limit", maxBytes)
	}
	return os.ReadFile(path)
}

func xmlText(value string) string {
	value = validXMLText(value)
	var output bytes.Buffer
	_ = xml.EscapeText(&output, []byte(value))
	return output.String()
}

func xmlAttributeText(value string) string {
	return xmlText(value)
}

func validXMLText(value string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\t', '\n', '\r':
			return r
		}
		if r >= 0x20 && r <= unicode.MaxRune && r != 0xFFFE && r != 0xFFFF {
			return r
		}
		return -1
	}, value)
}

func officeCoreProperties(title, subject, author string) []byte {
	now := time.Now().UTC().Format(time.RFC3339)
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<dc:title>` + xmlText(title) + `</dc:title><dc:subject>` + xmlText(subject) + `</dc:subject><dc:creator>` + xmlText(author) + `</dc:creator>` +
		`<cp:lastModifiedBy>` + xmlText(author) + `</cp:lastModifiedBy><dcterms:created xsi:type="dcterms:W3CDTF">` + now + `</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">` + now + `</dcterms:modified></cp:coreProperties>`)
}

func rootRelationships(officeTarget string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="` + officeTarget + `"/>` +
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
		`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>` +
		`</Relationships>`)
}
