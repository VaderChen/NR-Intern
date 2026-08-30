package httpui

import (
	"AgenticService/src/desktop/screencapture"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"time"
)

const maxClipboardImageBytes = 32 << 20

func (s *Server) captureScreen(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := contextWithTimeout(request, 10*time.Minute)
	defer cancel()
	result, err := screencapture.Capture(ctx)
	if errors.Is(err, screencapture.ErrCanceled) {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if errors.Is(err, screencapture.ErrPermissionDenied) {
		writeJSON(writer, http.StatusForbidden, map[string]any{"error": err.Error()})
		return
	}
	if errors.Is(err, screencapture.ErrUnavailable) {
		writeJSON(writer, http.StatusNotImplemented, map[string]any{"error": "目前平台不支援原生畫面擷取"})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	data := map[string]any{"status": result.Status}
	if len(result.PNG) > 0 {
		data["image_base64"] = base64.StdEncoding.EncodeToString(result.PNG)
		data["mime_type"] = "image/png"
	}
	writeData(writer, data)
}

func (s *Server) copyImageToClipboard(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxClipboardImageBytes)
	value, err := io.ReadAll(request.Body)
	if err != nil {
		writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]any{"error": "圖像超過 32 MB 上限"})
		return
	}
	ctx, cancel := contextWithTimeout(request, 30*time.Second)
	defer cancel()
	if err := screencapture.CopyPNGToClipboard(ctx, value); err != nil {
		if errors.Is(err, screencapture.ErrUnavailable) {
			writeJSON(writer, http.StatusNotImplemented, map[string]any{"error": "目前平台不支援更新圖像剪貼簿"})
			return
		}
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeData(writer, map[string]any{"status": screencapture.StatusCopied})
}
