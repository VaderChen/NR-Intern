package httpapi

import (
	"AgenticService/src/domain"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// 長期記憶過去只能經由 Agent 工具存取，使用者無法檢視 Agent 記住了什麼、
// 也無法在 Agent 記錯時更正。這組端點把同一份 repository 開放給管理介面。

func (h *Handler) listMemories(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	// scope=all 讓管理介面不必先知道 scope 就能看到 Agent 記了什麼。
	// 回憶空間開啟後記憶落在 project:<id>，那串 ID 使用者猜不到。
	if strings.EqualFold(strings.TrimSpace(query.Get("scope")), "all") {
		limit := 0
		if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed <= 0 {
				writeProblem(writer, request, invalidQuery("limit must be a positive integer"))
				return
			}
			limit = parsed
		}
		values, err := h.service.ListAllMemories(request.Context(), limit)
		if err != nil {
			writeProblem(writer, request, err)
			return
		}
		writeData(writer, http.StatusOK, values)
		return
	}
	memoryQuery := domain.MemoryQuery{
		Scope: query.Get("scope"),
		Text:  strings.TrimSpace(query.Get("q")),
		Tags:  splitQueryList(query.Get("tags")),
	}
	for _, kind := range splitQueryList(query.Get("kinds")) {
		memoryQuery.Kinds = append(memoryQuery.Kinds, domain.MemoryKind(kind))
	}
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeProblem(writer, request, invalidQuery("limit must be a positive integer"))
			return
		}
		memoryQuery.Limit = parsed
	}
	values, err := h.service.SearchMemories(request.Context(), memoryQuery)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, values)
}

func (h *Handler) getMemory(writer http.ResponseWriter, request *http.Request) {
	value, err := h.service.GetMemory(request.Context(), request.URL.Query().Get("scope"), request.PathValue("memory_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) createMemory(writer http.ResponseWriter, request *http.Request) {
	var input domain.RememberMemoryInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.service.RememberMemory(request.Context(), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusCreated, value)
}

// deleteMemory 是軟性遺忘：記憶保留稽核資訊但不再被召回，因此回傳更新後的內容。
func (h *Handler) deleteMemory(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	value, err := h.service.ForgetMemory(request.Context(), query.Get("scope"), request.PathValue("memory_id"), query.Get("reason"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func invalidQuery(message string) error {
	return fmt.Errorf("%w: %s", domain.ErrInvalidInput, message)
}

func splitQueryList(raw string) []string {
	result := []string{}
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
