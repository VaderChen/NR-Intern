package httpui

import (
	"AgenticService/src/desktop/launcher"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const maxResourceRequestBytes = 64 * 1024

type resourceRequest struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Action string `json:"action"`
}

type resolvedResource struct {
	Kind      string
	Target    string
	Action    string
	Directory bool
}

func (s *Server) openResource(writer http.ResponseWriter, request *http.Request) {
	var input resourceRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxResourceRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "invalid resource request: " + err.Error()})
		return
	}
	resource, err := resolveResource(input)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	switch resource.Kind {
	case "url":
		err = launcher.OpenURL(resource.Target)
	case "path":
		if resource.Action == "reveal" {
			err = launcher.RevealPath(resource.Target, resource.Directory)
		} else {
			err = launcher.OpenPath(resource.Target)
		}
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeData(writer, map[string]any{
		"kind": resource.Kind, "target": resource.Target, "action": resource.Action,
	})
}

func resolveResource(input resourceRequest) (resolvedResource, error) {
	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	target := strings.TrimSpace(input.Target)
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if action == "" {
		action = "open"
	}
	if target == "" {
		return resolvedResource{}, fmt.Errorf("resource target is required")
	}

	switch kind {
	case "url":
		if action != "open" {
			return resolvedResource{}, fmt.Errorf("URL resources only support the open action")
		}
		parsed, err := url.ParseRequestURI(target)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return resolvedResource{}, fmt.Errorf("only absolute HTTP or HTTPS URLs can be opened")
		}
		return resolvedResource{Kind: kind, Target: parsed.String(), Action: action}, nil
	case "path":
		if action != "open" && action != "reveal" {
			return resolvedResource{}, fmt.Errorf("path action must be open or reveal")
		}
		if !filepath.IsAbs(target) {
			return resolvedResource{}, fmt.Errorf("only absolute local paths can be opened")
		}
		cleaned := filepath.Clean(target)
		info, err := os.Stat(cleaned)
		if err != nil {
			if os.IsNotExist(err) {
				return resolvedResource{}, fmt.Errorf("local path does not exist: %s", cleaned)
			}
			return resolvedResource{}, fmt.Errorf("inspect local path: %w", err)
		}
		return resolvedResource{Kind: kind, Target: cleaned, Action: action, Directory: info.IsDir()}, nil
	default:
		return resolvedResource{}, fmt.Errorf("resource kind must be url or path")
	}
}
