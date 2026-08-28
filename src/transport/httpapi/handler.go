package httpapi

import (
	"AgenticService/src/application"
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
	errUnauthorized = errors.New("unauthorized")
	errUnavailable  = errors.New("service unavailable")
)

type Handler struct {
	service                *application.Service
	apiToken               string
	allowedOrigins         []string
	maxBodyBytes           int64
	maxAttachmentBytes     int64
	attachments            ports.AttachmentRepository
	status                 func() domain.ServiceStatus
	toolCatalog            func(context.Context, string) ([]domain.ToolCatalogEntry, error)
	diagnostics            func(context.Context) (any, error)
	serviceSettings        func(context.Context) (domain.ServiceSettings, error)
	updateServiceSettings  func(context.Context, domain.UpdateServiceSettingsInput) (domain.ServiceSettings, error)
	providerSettings       func(context.Context) (domain.ProviderSettings, error)
	updateProviderSettings func(context.Context, domain.UpdateProviderSettingsInput) (domain.ProviderSettings, error)
	providerModels         func(context.Context, string) (domain.ProviderModels, error)
	testProvider           func(context.Context, string) (domain.ProviderTestResult, error)
	mux                    *http.ServeMux
}

func New(service *application.Service, config Config) (*Handler, error) {
	if service == nil {
		return nil, fmt.Errorf("%w: application service is required", domain.ErrInvalidInput)
	}
	maxBodyBytes := config.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = 2 * 1024 * 1024
	}
	maxAttachmentBytes := config.MaxAttachmentBytes
	if maxAttachmentBytes <= 0 {
		maxAttachmentBytes = 8 * 1024 * 1024
	}
	handler := &Handler{
		service:                service,
		apiToken:               strings.TrimSpace(config.APIToken),
		allowedOrigins:         append([]string(nil), config.AllowedOrigins...),
		maxBodyBytes:           maxBodyBytes,
		maxAttachmentBytes:     maxAttachmentBytes,
		attachments:            config.Attachments,
		status:                 config.Status,
		toolCatalog:            config.ToolCatalog,
		diagnostics:            config.Diagnostics,
		serviceSettings:        config.ServiceSettings,
		updateServiceSettings:  config.UpdateServiceSettings,
		providerSettings:       config.ProviderSettings,
		updateProviderSettings: config.UpdateProviderSettings,
		providerModels:         config.ProviderModels,
		testProvider:           config.TestProvider,
		mux:                    http.NewServeMux(),
	}
	handler.routes()
	return handler, nil
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	h.middleware(h.mux).ServeHTTP(writer, request)
}

func (h *Handler) routes() {
	h.mux.HandleFunc("GET /healthz", h.health)
	h.mux.HandleFunc("GET /readyz", h.ready)
	h.mux.HandleFunc("GET /api/v1/openapi.yaml", h.openAPI)
	h.mux.HandleFunc("GET /api/v1/admin/status", h.serviceStatus)
	h.mux.HandleFunc("GET /api/v1/admin/diagnostics", h.serviceDiagnostics)
	h.mux.HandleFunc("GET /api/v1/admin/service-settings", h.getServiceSettings)
	h.mux.HandleFunc("PUT /api/v1/admin/service-settings", h.putServiceSettings)
	h.mux.HandleFunc("GET /api/v1/admin/provider-settings", h.getProviderSettings)
	h.mux.HandleFunc("PUT /api/v1/admin/provider-settings", h.putProviderSettings)
	h.mux.HandleFunc("GET /api/v1/admin/provider-settings/{provider_id}/models", h.getProviderModels)
	h.mux.HandleFunc("POST /api/v1/admin/provider-settings/{provider_id}/test", h.postProviderTest)
	h.mux.HandleFunc("GET /api/v1/tools", h.listTools)
	h.mux.HandleFunc("GET /api/v1/providers", h.listProviders)
	h.mux.HandleFunc("GET /api/v1/sessions/{session_id}/export", h.exportSession)
	h.mux.HandleFunc("GET /api/v1/memories", h.listMemories)
	h.mux.HandleFunc("POST /api/v1/memories", h.createMemory)
	h.mux.HandleFunc("GET /api/v1/memories/{memory_id}", h.getMemory)
	h.mux.HandleFunc("DELETE /api/v1/memories/{memory_id}", h.deleteMemory)
	h.mux.HandleFunc("GET /api/v1/workspaces", h.listWorkspaces)
	h.mux.HandleFunc("POST /api/v1/workspaces", h.createWorkspace)
	h.mux.HandleFunc("GET /api/v1/workspaces/{workspace_id}", h.getWorkspace)
	h.mux.HandleFunc("PATCH /api/v1/workspaces/{workspace_id}", h.updateWorkspace)
	h.mux.HandleFunc("DELETE /api/v1/workspaces/{workspace_id}", h.deleteWorkspace)
	h.mux.HandleFunc("GET /api/v1/projects", h.listProjects)
	h.mux.HandleFunc("POST /api/v1/projects", h.createProject)
	h.mux.HandleFunc("GET /api/v1/projects/{project_id}", h.getProject)
	h.mux.HandleFunc("PATCH /api/v1/projects/{project_id}", h.updateProject)
	h.mux.HandleFunc("DELETE /api/v1/projects/{project_id}", h.deleteProject)
	h.mux.HandleFunc("GET /api/v1/agents", h.listAgents)
	h.mux.HandleFunc("GET /api/v1/agents/{agent_id}", h.getAgent)
	h.mux.HandleFunc("GET /api/v1/agents/{agent_id}/sessions", h.listSessions)
	h.mux.HandleFunc("POST /api/v1/agents/{agent_id}/sessions", h.createSession)
	h.mux.HandleFunc("GET /api/v1/sessions/{session_id}", h.getSession)
	h.mux.HandleFunc("PATCH /api/v1/sessions/{session_id}", h.updateSession)
	h.mux.HandleFunc("DELETE /api/v1/sessions/{session_id}", h.deleteSession)
	h.mux.HandleFunc("GET /api/v1/sessions/{session_id}/plan", h.getPlan)
	h.mux.HandleFunc("PUT /api/v1/sessions/{session_id}/plan", h.putPlan)
	h.mux.HandleFunc("DELETE /api/v1/sessions/{session_id}/plan", h.deletePlan)
	h.mux.HandleFunc("GET /api/v1/sessions/{session_id}/plans", h.listPlans)
	h.mux.HandleFunc("POST /api/v1/sessions/{session_id}/plans", h.createPlan)
	h.mux.HandleFunc("PUT /api/v1/sessions/{session_id}/plans/order", h.reorderPlans)
	h.mux.HandleFunc("PUT /api/v1/sessions/{session_id}/plans/{plan_id}", h.updatePlan)
	h.mux.HandleFunc("DELETE /api/v1/sessions/{session_id}/plans/{plan_id}", h.deletePlanByID)
	h.mux.HandleFunc("GET /api/v1/sessions/{session_id}/messages", h.listMessages)
	h.mux.HandleFunc("POST /api/v1/sessions/{session_id}/attachments", h.uploadSessionAttachments)
	h.mux.HandleFunc("GET /api/v1/sessions/{session_id}/entries", h.listEntries)
	h.mux.HandleFunc("GET /api/v1/sessions/{session_id}/runs", h.listSessionRuns)
	h.mux.HandleFunc("POST /api/v1/sessions/{session_id}/runs", h.executeRun)
	h.mux.HandleFunc("POST /api/v1/sessions/{session_id}/runs:stream", h.streamRun)
	h.mux.HandleFunc("GET /api/v1/runs", h.listRuns)
	h.mux.HandleFunc("GET /api/v1/runs/{run_id}", h.getRun)
	h.mux.HandleFunc("GET /api/v1/runs/{run_id}/events", h.streamExistingRun)
	h.mux.HandleFunc("POST /api/v1/runs/{run_id}/cancel", h.cancelRun)
	h.mux.HandleFunc("POST /api/v1/runs/{run_id}/decision", h.decideRun)
	h.mux.HandleFunc("POST /api/v1/runs/{run_id}/retry", h.retryRun)
}

func (h *Handler) health(writer http.ResponseWriter, _ *http.Request) {
	writeData(writer, http.StatusOK, map[string]any{"status": "ok"})
}

func (h *Handler) ready(writer http.ResponseWriter, request *http.Request) {
	status := h.currentStatus()
	if !status.Ready {
		writeProblem(writer, request, fmt.Errorf("%w: service is not ready", errUnavailable))
		return
	}
	writeData(writer, http.StatusOK, map[string]any{"status": "ready"})
}

func (h *Handler) serviceStatus(writer http.ResponseWriter, _ *http.Request) {
	writeData(writer, http.StatusOK, h.currentStatus())
}

func (h *Handler) serviceDiagnostics(writer http.ResponseWriter, request *http.Request) {
	if h.diagnostics == nil {
		writeProblem(writer, request, fmt.Errorf("%w: diagnostics provider is unavailable", errUnavailable))
		return
	}
	value, err := h.diagnostics(request.Context())
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) getServiceSettings(writer http.ResponseWriter, request *http.Request) {
	if h.serviceSettings == nil {
		writeProblem(writer, request, fmt.Errorf("%w: service settings are unavailable", errUnavailable))
		return
	}
	value, err := h.serviceSettings(request.Context())
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) putServiceSettings(writer http.ResponseWriter, request *http.Request) {
	if h.updateServiceSettings == nil {
		writeProblem(writer, request, fmt.Errorf("%w: service settings are unavailable", errUnavailable))
		return
	}
	var input domain.UpdateServiceSettingsInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.updateServiceSettings(request.Context(), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) getProviderSettings(writer http.ResponseWriter, request *http.Request) {
	if h.providerSettings == nil {
		writeProblem(writer, request, fmt.Errorf("%w: provider settings are unavailable", errUnavailable))
		return
	}
	value, err := h.providerSettings(request.Context())
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) putProviderSettings(writer http.ResponseWriter, request *http.Request) {
	if h.updateProviderSettings == nil {
		writeProblem(writer, request, fmt.Errorf("%w: provider settings are unavailable", errUnavailable))
		return
	}
	var input domain.UpdateProviderSettingsInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.updateProviderSettings(request.Context(), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) getProviderModels(writer http.ResponseWriter, request *http.Request) {
	if h.providerModels == nil {
		writeProblem(writer, request, fmt.Errorf("%w: provider model catalog is unavailable", errUnavailable))
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	value, err := h.providerModels(ctx, request.PathValue("provider_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) postProviderTest(writer http.ResponseWriter, request *http.Request) {
	if h.testProvider == nil {
		writeProblem(writer, request, fmt.Errorf("%w: provider test is unavailable", errUnavailable))
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 90*time.Second)
	defer cancel()
	value, err := h.testProvider(ctx, request.PathValue("provider_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) listTools(writer http.ResponseWriter, request *http.Request) {
	if h.toolCatalog == nil {
		writeProblem(writer, request, fmt.Errorf("%w: tool catalog is unavailable", errUnavailable))
		return
	}
	value, err := h.toolCatalog(request.Context(), request.URL.Query().Get("session_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) currentStatus() domain.ServiceStatus {
	if h.status == nil {
		return domain.ServiceStatus{Name: "ai-agent", Ready: true}
	}
	return h.status()
}

func (h *Handler) listProjects(writer http.ResponseWriter, request *http.Request) {
	values, err := h.service.ListProjects(request.Context())
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	workspaceID := strings.TrimSpace(request.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		writeData(writer, http.StatusOK, values)
		return
	}
	filtered := make([]domain.Project, 0, len(values))
	for _, value := range values {
		if value.WorkspaceID == workspaceID {
			filtered = append(filtered, value)
		}
	}
	writeData(writer, http.StatusOK, filtered)
}

func (h *Handler) listProviders(writer http.ResponseWriter, _ *http.Request) {
	writeData(writer, http.StatusOK, h.service.ListProviders())
}

func (h *Handler) listWorkspaces(writer http.ResponseWriter, request *http.Request) {
	values, err := h.service.ListWorkspaces(request.Context())
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, values)
}

func (h *Handler) createWorkspace(writer http.ResponseWriter, request *http.Request) {
	var input domain.CreateWorkspaceInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.service.CreateWorkspace(request.Context(), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusCreated, value)
}

func (h *Handler) getWorkspace(writer http.ResponseWriter, request *http.Request) {
	value, err := h.service.GetWorkspace(request.Context(), request.PathValue("workspace_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) updateWorkspace(writer http.ResponseWriter, request *http.Request) {
	var input domain.UpdateWorkspaceInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.service.UpdateWorkspace(request.Context(), request.PathValue("workspace_id"), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) deleteWorkspace(writer http.ResponseWriter, request *http.Request) {
	if err := h.service.DeleteWorkspace(request.Context(), request.PathValue("workspace_id")); err != nil {
		writeProblem(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (h *Handler) createProject(writer http.ResponseWriter, request *http.Request) {
	var input domain.CreateProjectInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.service.CreateProject(request.Context(), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusCreated, value)
}

func (h *Handler) getProject(writer http.ResponseWriter, request *http.Request) {
	value, err := h.service.GetProject(request.Context(), request.PathValue("project_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) updateProject(writer http.ResponseWriter, request *http.Request) {
	var input domain.UpdateProjectInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.service.UpdateProject(request.Context(), request.PathValue("project_id"), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) deleteProject(writer http.ResponseWriter, request *http.Request) {
	if err := h.service.DeleteProject(request.Context(), request.PathValue("project_id")); err != nil {
		writeProblem(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listAgents(writer http.ResponseWriter, _ *http.Request) {
	writeData(writer, http.StatusOK, h.service.ListAgents())
}

func (h *Handler) getAgent(writer http.ResponseWriter, request *http.Request) {
	value, err := h.service.GetAgent(request.PathValue("agent_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) listSessions(writer http.ResponseWriter, request *http.Request) {
	value, err := h.service.ListSessions(request.Context(), request.PathValue("agent_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	workspaceID := strings.TrimSpace(request.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		writeData(writer, http.StatusOK, value)
		return
	}
	filtered := make([]domain.Session, 0, len(value))
	for _, session := range value {
		if session.WorkspaceID == workspaceID {
			filtered = append(filtered, session)
		}
	}
	writeData(writer, http.StatusOK, filtered)
}

func (h *Handler) createSession(writer http.ResponseWriter, request *http.Request) {
	var input domain.CreateSessionInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.service.CreateSession(request.Context(), request.PathValue("agent_id"), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusCreated, value)
}

func (h *Handler) getSession(writer http.ResponseWriter, request *http.Request) {
	value, err := h.service.GetSession(request.Context(), request.PathValue("session_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) updateSession(writer http.ResponseWriter, request *http.Request) {
	var input domain.UpdateSessionInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.service.UpdateSession(request.Context(), request.PathValue("session_id"), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) deleteSession(writer http.ResponseWriter, request *http.Request) {
	if err := h.service.DeleteSession(request.Context(), request.PathValue("session_id")); err != nil {
		writeProblem(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listMessages(writer http.ResponseWriter, request *http.Request) {
	value, err := h.service.ListMessages(request.Context(), request.PathValue("session_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) uploadSessionAttachments(writer http.ResponseWriter, request *http.Request) {
	if h.attachments == nil {
		writeProblem(writer, request, fmt.Errorf("%w: attachment storage is unavailable", errUnavailable))
		return
	}
	sessionID := request.PathValue("session_id")
	if _, err := h.service.GetSession(request.Context(), sessionID); err != nil {
		writeProblem(writer, request, err)
		return
	}
	// 最多 16 個附件；MaxBytesReader 的額外 1 MiB 保留 multipart headers 與 boundary。
	request.Body = http.MaxBytesReader(writer, request.Body, h.maxAttachmentBytes*16+1024*1024)
	reader, err := request.MultipartReader()
	if err != nil {
		writeProblem(writer, request, fmt.Errorf("%w: multipart form-data is required: %v", domain.ErrInvalidInput, err))
		return
	}
	values := make([]domain.Attachment, 0, 4)
	cleanup := func() {
		for _, value := range values {
			_ = h.attachments.Delete(context.WithoutCancel(request.Context()), sessionID, value.ID)
		}
	}
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			cleanup()
			writeProblem(writer, request, fmt.Errorf("%w: read multipart attachment: %v", domain.ErrInvalidInput, nextErr))
			return
		}
		filename := strings.TrimSpace(part.FileName())
		if (part.FormName() != "files" && part.FormName() != "file") || filename == "" {
			_ = part.Close()
			continue
		}
		if len(values) >= 16 {
			_ = part.Close()
			cleanup()
			writeProblem(writer, request, fmt.Errorf("%w: no more than 16 attachments are allowed", domain.ErrInvalidInput))
			return
		}
		value, saveErr := h.attachments.Save(request.Context(), sessionID, filename, part.Header.Get("Content-Type"), part, h.maxAttachmentBytes)
		_ = part.Close()
		if saveErr != nil {
			cleanup()
			writeProblem(writer, request, saveErr)
			return
		}
		values = append(values, value)
	}
	if len(values) == 0 {
		writeProblem(writer, request, fmt.Errorf("%w: at least one file is required", domain.ErrInvalidInput))
		return
	}
	writeData(writer, http.StatusCreated, values)
}

func (h *Handler) listEntries(writer http.ResponseWriter, request *http.Request) {
	after, err := queryInt64(request, "after_sequence", 0, 0)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	limit, err := queryInt64(request, "limit", 200, 1)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	if limit > 1000 {
		writeProblem(writer, request, fmt.Errorf("%w: limit cannot exceed 1000", domain.ErrInvalidInput))
		return
	}
	values, err := h.service.ListEntries(request.Context(), request.PathValue("session_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	filtered := make([]domain.SessionEntry, 0, min(int(limit), len(values)))
	hasMore := false
	for _, value := range values {
		if value.Sequence <= after {
			continue
		}
		if len(filtered) >= int(limit) {
			hasMore = true
			break
		}
		filtered = append(filtered, value)
	}
	next := after
	if len(filtered) > 0 {
		next = filtered[len(filtered)-1].Sequence
	}
	writeData(writer, http.StatusOK, map[string]any{"items": filtered, "next_after_sequence": next, "has_more": hasMore})
}

func (h *Handler) listSessionRuns(writer http.ResponseWriter, request *http.Request) {
	if _, err := h.service.GetSession(request.Context(), request.PathValue("session_id")); err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.service.ListRuns(request.Context(), request.PathValue("session_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) listRuns(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	statuses := []domain.RunStatus{}
	for _, raw := range splitQueryList(query.Get("status")) {
		status := domain.RunStatus(raw)
		if !knownRunStatus(status) {
			writeProblem(writer, request, invalidQuery("unknown run status "+raw))
			return
		}
		statuses = append(statuses, status)
	}
	value, err := h.service.ListRunsByStatus(request.Context(), query.Get("session_id"), statuses)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func knownRunStatus(status domain.RunStatus) bool {
	switch status {
	case domain.RunStatusQueued, domain.RunStatusRunning, domain.RunStatusWaitingApproval,
		domain.RunStatusCompleted, domain.RunStatusFailed, domain.RunStatusCanceled:
		return true
	default:
		return false
	}
}

// exportSession 提供整個 Session 的可攜快照。format=markdown 產生人類可讀的逐字稿，
// 預設 json 帶完整結構；include_entries=true 另外附上稽核用的完整 transcript。
func (h *Handler) exportSession(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	includeEntries := strings.EqualFold(strings.TrimSpace(query.Get("include_entries")), "true")
	export, err := h.service.ExportSession(request.Context(), request.PathValue("session_id"), includeEntries)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	switch strings.ToLower(strings.TrimSpace(query.Get("format"))) {
	case "", "json":
		writeData(writer, http.StatusOK, export)
	case "markdown":
		writer.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(export.Markdown()))
	default:
		writeProblem(writer, request, invalidQuery("format must be json or markdown"))
	}
}

func (h *Handler) getRun(writer http.ResponseWriter, request *http.Request) {
	value, err := h.service.GetRun(request.Context(), request.PathValue("run_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusOK, value)
}

func (h *Handler) cancelRun(writer http.ResponseWriter, request *http.Request) {
	value, err := h.service.CancelRun(request.Context(), request.PathValue("run_id"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusAccepted, value)
}

func (h *Handler) decideRun(writer http.ResponseWriter, request *http.Request) {
	var input domain.ToolApprovalDecisionInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.service.DecideRun(request.Context(), request.PathValue("run_id"), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writeData(writer, http.StatusAccepted, value)
}

func (h *Handler) retryRun(writer http.ResponseWriter, request *http.Request) {
	value, err := h.service.RetryRun(request.Context(), request.PathValue("run_id"), request.Header.Get("Idempotency-Key"))
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/api/v1/runs/"+value.ID)
	writeData(writer, http.StatusAccepted, value)
}

func (h *Handler) executeRun(writer http.ResponseWriter, request *http.Request) {
	input, err := h.decodeRunInput(writer, request)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.service.StartRun(request.Context(), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/api/v1/runs/"+value.ID)
	writeData(writer, http.StatusAccepted, value)
}

func (h *Handler) streamRun(writer http.ResponseWriter, request *http.Request) {
	input, err := h.decodeRunInput(writer, request)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	afterSequence, err := parseAfterSequence(request)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	value, err := h.service.StartRun(request.Context(), input)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	writer.Header().Set("X-Run-ID", value.ID)
	writer.Header().Set("Location", "/api/v1/runs/"+value.ID)
	h.writeRunEventStream(writer, request, value.ID, afterSequence)
}

func (h *Handler) streamExistingRun(writer http.ResponseWriter, request *http.Request) {
	runID := request.PathValue("run_id")
	if _, err := h.service.GetRun(request.Context(), runID); err != nil {
		writeProblem(writer, request, err)
		return
	}
	afterSequence, err := parseAfterSequence(request)
	if err != nil {
		writeProblem(writer, request, err)
		return
	}
	h.writeRunEventStream(writer, request, runID, afterSequence)
}

func (h *Handler) writeRunEventStream(writer http.ResponseWriter, request *http.Request, runID string, afterSequence int64) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeProblem(writer, request, fmt.Errorf("streaming is not supported by this server"))
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-cache, no-store, no-transform")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		updates, unsubscribe, err := h.service.SubscribeRunEvents(request.Context(), runID)
		if err != nil {
			return
		}
		events, err := h.service.ListRunEvents(request.Context(), runID, afterSequence)
		if err != nil {
			unsubscribe()
			return
		}
		for _, event := range events {
			if err := writeSSEEvent(writer, event); err != nil {
				unsubscribe()
				return
			}
			afterSequence = event.Sequence
			flusher.Flush()
		}
		run, err := h.service.GetRun(request.Context(), runID)
		if err != nil {
			unsubscribe()
			return
		}
		if isTerminalRun(run.Status) {
			unsubscribe()
			return
		}
		select {
		case <-request.Context().Done():
			unsubscribe()
			return
		case <-updates:
			unsubscribe()
		case <-heartbeat.C:
			unsubscribe()
			if _, err := fmt.Fprint(writer, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSEEvent(writer io.Writer, event domain.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, data)
	return err
}

func parseAfterSequence(request *http.Request) (int64, error) {
	value := strings.TrimSpace(request.URL.Query().Get("after_sequence"))
	if header := strings.TrimSpace(request.Header.Get("Last-Event-ID")); header != "" {
		value = header
	}
	if value == "" {
		return 0, nil
	}
	sequence, err := strconv.ParseInt(value, 10, 64)
	if err != nil || sequence < 0 {
		return 0, fmt.Errorf("%w: Last-Event-ID and after_sequence must be a non-negative event sequence", domain.ErrInvalidInput)
	}
	return sequence, nil
}

func isTerminalRun(status domain.RunStatus) bool {
	return status == domain.RunStatusCompleted || status == domain.RunStatusFailed || status == domain.RunStatusCanceled
}

func (h *Handler) decodeRunInput(writer http.ResponseWriter, request *http.Request) (domain.RunInput, error) {
	var input domain.RunInput
	if err := h.decodeJSON(writer, request, &input); err != nil {
		return domain.RunInput{}, err
	}
	input.SessionID = request.PathValue("session_id")
	input.IdempotencyKey = request.Header.Get("Idempotency-Key")
	return input, nil
}

func (h *Handler) decodeJSON(writer http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, h.maxBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: invalid JSON body: %v", domain.ErrInvalidInput, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: request body must contain one JSON value", domain.ErrInvalidInput)
	}
	return nil
}

func queryInt64(request *http.Request, name string, fallback, minimum int64) (int64, error) {
	value := strings.TrimSpace(request.URL.Query().Get(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < minimum {
		return 0, fmt.Errorf("%w: %s must be an integer greater than or equal to %d", domain.ErrInvalidInput, name, minimum)
	}
	return parsed, nil
}
