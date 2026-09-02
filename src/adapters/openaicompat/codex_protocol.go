package openaicompat

import (
	"AgenticService/src/domain"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const codexAuthClaim = "https://api.openai.com/auth"

type codexRequest struct {
	Model             string           `json:"model"`
	Instructions      string           `json:"instructions,omitempty"`
	Input             []codexInputItem `json:"input"`
	Tools             []codexTool      `json:"tools,omitempty"`
	ToolChoice        string           `json:"tool_choice,omitempty"`
	ParallelToolCalls bool             `json:"parallel_tool_calls"`
	Reasoning         *codexReasoning  `json:"reasoning,omitempty"`
	Include           []string         `json:"include,omitempty"`
	PromptCacheKey    string           `json:"prompt_cache_key,omitempty"`
	Stream            bool             `json:"stream"`
	Store             bool             `json:"store"`
}

type codexInputItem struct {
	Type      string             `json:"type"`
	Role      string             `json:"role,omitempty"`
	Content   []codexContentPart `json:"content,omitempty"`
	CallID    string             `json:"call_id,omitempty"`
	Name      string             `json:"name,omitempty"`
	Arguments string             `json:"arguments,omitempty"`
	Output    string             `json:"output,omitempty"`
}

type codexContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type codexTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type codexReasoning struct {
	Effort  string `json:"effort"`
	Summary string `json:"summary"`
}

func buildCodexRequest(request domain.ModelRequest, modelName string) (codexRequest, error) {
	toolChoice := strings.ToLower(strings.TrimSpace(request.ToolChoice))
	if toolChoice != "" && toolChoice != "auto" && toolChoice != "required" && toolChoice != "none" {
		return codexRequest{}, fmt.Errorf("invalid tool choice %q: expected auto, required, or none", request.ToolChoice)
	}
	tools := codexTools(request.Tools)
	if len(tools) == 0 {
		toolChoice = ""
	} else if toolChoice == "" {
		toolChoice = "auto"
	}
	return codexRequest{
		Model:             strings.ToLower(strings.TrimSpace(modelName)),
		Instructions:      codexInstructions(request),
		Input:             codexInput(request),
		Tools:             tools,
		ToolChoice:        toolChoice,
		ParallelToolCalls: true,
		Reasoning:         codexReasoningFor(request.ThinkingMode),
		Include:           []string{"reasoning.encrypted_content"},
		PromptCacheKey:    strings.TrimSpace(request.SessionID),
		Stream:            true,
		Store:             false,
	}, nil
}

func codexInstructions(request domain.ModelRequest) string {
	parts := make([]string, 0, 4)
	for _, value := range []string{request.SystemPrompt, request.HostPrompt, request.ToolPrompt, request.PhasePrompt} {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		return "You are a coding agent. Follow the user's instructions and complete the task."
	}
	return strings.Join(parts, "\n\n")
}

func codexInput(request domain.ModelRequest) []codexInputItem {
	items := make([]codexInputItem, 0, len(request.History)*2+2)
	appendMessage := func(role, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		textType := "input_text"
		if role == "assistant" {
			textType = "output_text"
		}
		items = append(items, codexInputItem{Type: "message", Role: role, Content: []codexContentPart{{Type: textType, Text: text}}})
	}
	appendMessage("user", request.ContextPrompt)
	for _, message := range request.History {
		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "user":
			appendMessage("user", message.Content)
		case "assistant":
			appendMessage("assistant", message.Content)
			for index, call := range message.ToolCalls {
				name := strings.TrimSpace(call.Name)
				if name == "" {
					continue
				}
				callID := strings.TrimSpace(call.ID)
				if callID == "" {
					callID = fmt.Sprintf("call_history_%d", index)
				}
				arguments, _ := json.Marshal(call.Arguments)
				items = append(items, codexInputItem{Type: "function_call", CallID: callID, Name: name, Arguments: string(arguments)})
			}
		case "tool":
			if callID := strings.TrimSpace(message.ToolCallID); callID != "" {
				items = append(items, codexInputItem{Type: "function_call_output", CallID: callID, Output: message.Content})
			} else {
				appendMessage("user", message.Content)
			}
		}
	}
	appendMessage("user", request.UserPrompt)
	if len(items) == 0 {
		items = append(items, codexInputItem{Type: "message", Role: "user", Content: []codexContentPart{{Type: "input_text", Text: ""}}})
	}
	return items
}

func codexTools(definitions []domain.ToolDefinition) []codexTool {
	result := make([]codexTool, 0, len(definitions))
	for _, definition := range definitions {
		name := strings.TrimSpace(definition.Name)
		if name == "" {
			continue
		}
		parameters := definition.InputSchema
		if len(parameters) == 0 {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		result = append(result, codexTool{Type: "function", Name: name, Description: definition.Description, Parameters: parameters})
	}
	return result
}

func codexReasoningFor(value string) *codexReasoning {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none", "minimal", "low", "medium", "high", "xhigh":
		return &codexReasoning{Effort: strings.ToLower(strings.TrimSpace(value)), Summary: "concise"}
	default:
		return nil
	}
}

func codexAccountID(token string) string {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return ""
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(data, &claims); err != nil {
		return ""
	}
	auth, _ := claims[codexAuthClaim].(map[string]any)
	accountID, _ := auth["chatgpt_account_id"].(string)
	return strings.TrimSpace(accountID)
}
