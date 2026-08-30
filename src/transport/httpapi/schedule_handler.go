package httpapi

import (
	"AgenticService/src/domain"
	"net/http"
	"strings"
)

func (h *Handler) listSchedules(writer http.ResponseWriter, request *http.Request) {
	values, err := h.service.ListSchedules(request.Context())
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	workspaceID := strings.TrimSpace(request.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		writeData(writer, http.StatusOK, values)
		return
	}
	filtered := make([]domain.Schedule, 0, len(values))
	for _, value := range values {
		if value.WorkspaceID == workspaceID {
			filtered = append(filtered, value)
		}
	}
	writeData(writer, http.StatusOK, filtered)
}

func (h *Handler) createSchedule(writer http.ResponseWriter, request *http.Request) {
	var input domain.CreateScheduleInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.service.CreateSchedule(request.Context(), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusCreated, value)
}

func (h *Handler) getSchedule(writer http.ResponseWriter, request *http.Request) {
	value, err := h.service.GetSchedule(request.Context(), request.PathValue("schedule_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) updateSchedule(writer http.ResponseWriter, request *http.Request) {
	var input domain.UpdateScheduleInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.service.UpdateSchedule(request.Context(), request.PathValue("schedule_id"), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) deleteSchedule(writer http.ResponseWriter, request *http.Request) {
	if err := h.service.DeleteSchedule(request.Context(), request.PathValue("schedule_id")); err != nil {
		writeProblem(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

// postScheduleRun 立刻執行一次排程，回傳建立出來的 Run；
// 排程原本的下一次執行時間不受影響。
func (h *Handler) postScheduleRun(writer http.ResponseWriter, request *http.Request) {
	value, err := h.service.RunSchedule(request.Context(), request.PathValue("schedule_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusAccepted, value)
}
