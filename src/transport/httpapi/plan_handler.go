package httpapi

import (
	"AgenticService/src/domain"
	"net/http"
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
