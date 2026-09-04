package documents

import (
	"context"
	"fmt"
	"os"
)

type documentFormat string

const (
	formatPDF  documentFormat = "pdf"
	formatDOCX documentFormat = "docx"
	formatXLSX documentFormat = "xlsx"
	formatPPTX documentFormat = "pptx"
	// 純文字類：與 Office 文件共用 document_create，輸入結構也沿用同一組。
	formatMarkdown documentFormat = "md"
	formatText     documentFormat = "txt"
	formatCSV      documentFormat = "csv"
	formatHTML     documentFormat = "html"
)

// textDocumentFormat 判斷是否為純文字輸出。
//
// 這些格式不走 Office 的封裝流程，也不需要字型探索：內容就是最終位元組。
func textDocumentFormat(format documentFormat) bool {
	switch format {
	case formatMarkdown, formatText, formatCSV, formatHTML:
		return true
	default:
		return false
	}
}

func inspectDocument(ctx context.Context, path string, info os.FileInfo) (documentInspection, error) {
	format, err := detectDocumentFormat(path)
	if err != nil {
		return documentInspection{}, err
	}
	base := documentInspection{Format: string(format), SizeBytes: info.Size()}
	switch format {
	case formatPDF:
		base.MediaType = "application/pdf"
		return inspectPDF(ctx, path, base)
	case formatDOCX:
		base.MediaType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		return inspectDOCX(ctx, path, base)
	case formatXLSX:
		base.MediaType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		return inspectXLSX(ctx, path, base)
	case formatPPTX:
		base.MediaType = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
		return inspectPPTX(ctx, path, base)
	default:
		return documentInspection{}, fmt.Errorf("unsupported document format")
	}
}

func readDocument(ctx context.Context, path string, _ os.FileInfo, options readOptions) (documentReadResult, error) {
	format, err := detectDocumentFormat(path)
	if err != nil {
		return documentReadResult{}, err
	}
	switch format {
	case formatPDF:
		return readPDF(ctx, path, options)
	case formatDOCX:
		return readDOCX(ctx, path, options)
	case formatXLSX:
		return readXLSX(ctx, path, options)
	case formatPPTX:
		return readPPTX(ctx, path, options)
	default:
		return documentReadResult{}, fmt.Errorf("unsupported document format")
	}
}
