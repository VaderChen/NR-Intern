package httpapi

import (
	"AgenticService/src/domain"
	"net/http"
	"strings"
)

func (h *Handler) listPlans(writer http.ResponseWriter, request *http.Request) {
	values, err := h.service.ListPlans(request.Context(), request.PathValue("session_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, values)
}

func (h *Handler) createPlan(writer http.ResponseWriter, request *http.Request) {
	var input domain.CreatePlanInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.service.CreatePlan(request.Context(), request.PathValue("session_id"), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusCreated, value)
}

func (h *Handler) updatePlan(writer http.ResponseWriter, request *http.Request) {
	var input domain.CreatePlanInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.service.UpdatePlan(request.Context(), request.PathValue("session_id"), request.PathValue("plan_id"), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) deletePlanByID(writer http.ResponseWriter, request *http.Request) {
	if err := h.service.DeletePlanByID(request.Context(), request.PathValue("session_id"), request.PathValue("plan_id")); err != nil {
		writeProblem(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (h *Handler) reorderPlans(writer http.ResponseWriter, request *http.Request) {
	var input domain.ReorderPlansInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, request, err)
		return
	}
	values, err := h.service.ReorderPlans(request.Context(), request.PathValue("session_id"), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, values)
}

func (h *Handler) getPlan(writer http.ResponseWriter, request *http.Request) {
	value, err := h.service.GetPlan(request.Context(), request.PathValue("session_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) putPlan(writer http.ResponseWriter, request *http.Request) {
	var input domain.CreatePlanInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.service.PutPlan(request.Context(), request.PathValue("session_id"), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) deletePlan(writer http.ResponseWriter, request *http.Request) {
	if err := h.service.DeletePlan(request.Context(), request.PathValue("session_id")); err != nil {
		writeProblem(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

// 多輪執行的四個入口。啟動與續跑都由使用者發起——Agent 只能中斷，
// 讓它能自行開始無人看管地花錢，與 Approval 機制的整個前提衝突。
func (h *Handler) startPlanLoop(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		MaxRounds int `json:"max_rounds"`
	}
	if request.ContentLength != 0 {
		if err := h.decodeJSON(writer, request, &input); err != nil {
			writeProblem(writer, request, err)
			return
		}
	}
	value, err := h.service.StartPlanLoop(request.Context(),
		request.PathValue("session_id"), request.PathValue("plan_id"), input.MaxRounds)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) resumePlanLoop(writer http.ResponseWriter, request *http.Request) {
	value, err := h.service.ResumePlanLoop(request.Context(),
		request.PathValue("session_id"), request.PathValue("plan_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

// pausePlanLoop 是使用者版的中斷：立刻停下這一輪，但留下檢查點可以續跑。
func (h *Handler) pausePlanLoop(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Reason string `json:"reason"`
	}
	if request.ContentLength != 0 {
		if err := h.decodeJSON(writer, request, &input); err != nil {
			writeProblem(writer, request, err)
			return
		}
	}
	if strings.TrimSpace(input.Reason) == "" {
		input.Reason = "使用者暫停"
	}
	value, err := h.service.InterruptPlanLoop(request.Context(),
		request.PathValue("session_id"), request.PathValue("plan_id"),
		domain.PlanLoopStopUser, input.Reason, "")
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

// stopPlanLoop 是終結，不留續跑餘地。
func (h *Handler) stopPlanLoop(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Reason string `json:"reason"`
	}
	if request.ContentLength != 0 {
		if err := h.decodeJSON(writer, request, &input); err != nil {
			writeProblem(writer, request, err)
			return
		}
	}
	value, err := h.service.StopPlanLoop(request.Context(),
		request.PathValue("session_id"), request.PathValue("plan_id"), input.Reason)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}
