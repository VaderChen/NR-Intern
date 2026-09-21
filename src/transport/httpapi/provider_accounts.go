package httpapi

import (
	"AgenticService/src/domain"
	"fmt"
	"io"
	"mime"
	"net/http"
)

// 不使用一般 JSON 解碼錯誤，避免憑證內容被拼進 Problem；不暫存上傳檔案。
func readProviderAccountUpload(writer http.ResponseWriter, request *http.Request) ([]byte, error) {
	writer.Header().Set("Cache-Control", "no-store")
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return nil, fmt.Errorf("%w: 帳號檔必須以 application/json 上傳", domain.ErrInvalidInput)
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 8*1024*1024)
	data, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: 帳號檔讀取失敗或超過 8 MiB 上限", domain.ErrInvalidInput)
	}
	return data, nil
}

func (h *Handler) postProviderOAuthFileAccounts(writer http.ResponseWriter, request *http.Request) {
	if h.providerOAuthFileAccounts == nil {
		writeProblem(writer, request, errUnavailable)
		return
	}
	data, err := readProviderAccountUpload(writer, request)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.providerOAuthFileAccounts(request.Context(), request.PathValue("provider_id"), data)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) postImportProviderOAuthFileAccount(writer http.ResponseWriter, request *http.Request) {
	if h.importProviderOAuthFileAccount == nil {
		writeProblem(writer, request, errUnavailable)
		return
	}
	data, err := readProviderAccountUpload(writer, request)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.importProviderOAuthFileAccount(request.Context(), request.PathValue("provider_id"), data, request.PathValue("selection_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}
