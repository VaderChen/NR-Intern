package mcpclient

import (
	"AgenticService/src/domain"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type toolContinuation struct {
	owner, server, tool, argumentsHash, requestState string
	contractID                                       string
	callID                                           string
	expires                                          time.Time
	used                                             bool
}

func continuationSchema(schema map[string]any) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		properties = map[string]any{}
		schema["properties"] = properties
	}
	properties[mcpContinuationArgument] = map[string]any{"type": "string", "description": "僅在工具要求追加輸入時，帶回 Runtime 提供的續接 ID，並保留原始業務參數。"}
	properties[mcpInputResponsesArgument] = map[string]any{"type": "object", "additionalProperties": true, "description": "依輸入請求 ID 對應的追加輸入回覆。"}
	return schema
}

func continuationArgumentsHash(arguments map[string]any) string {
	values := map[string]any{}
	for key, value := range arguments {
		if key != mcpContinuationArgument && key != mcpInputResponsesArgument && key != mcpRequestStateArgument {
			values[key] = value
		}
	}
	encoded, _ := json.Marshal(values)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (m *Manager) saveContinuation(owner, server string, call domain.ToolCall, state string) (string, error) {
	if len(state) > 64*1024 {
		return "", fmt.Errorf("MCP 續接狀態過大，未自動重送；請確認遠端工作狀態")
	}
	m.continuationMu.Lock()
	defer m.continuationMu.Unlock()
	if m.continuations == nil {
		m.continuations = map[string]*toolContinuation{}
	}
	now := time.Now()
	for id, value := range m.continuations {
		if now.After(value.expires) {
			delete(m.continuations, id)
		}
	}
	if len(m.continuations) >= 512 {
		return "", fmt.Errorf("MCP 續接數量已達上限，未自動重送；請先處理既有追加輸入")
	}
	id := domain.NewID("mcp_continue")
	m.continuations[id] = &toolContinuation{owner: owner, server: server, tool: call.Name, callID: call.ID, argumentsHash: continuationArgumentsHash(call.Arguments), requestState: state, contractID: call.ExpectedContractID, expires: now.Add(24 * time.Hour)}
	return id, nil
}

func (m *Manager) resumeToolCall(owner, server string, call domain.ToolCall, tool resolvedTool) (domain.ToolCall, string, error) {
	raw, supplied := call.Arguments[mcpContinuationArgument]
	if !supplied {
		if _, responses := call.Arguments[mcpInputResponsesArgument]; responses {
			return call, "", fmt.Errorf("MCP 追加輸入必須帶上 Runtime 提供的續接 ID")
		}
		m.continuationMu.Lock()
		defer m.continuationMu.Unlock()
		hash := continuationArgumentsHash(call.Arguments)
		for id, value := range m.continuations {
			if value.owner == owner && value.server == server && value.tool == call.Name && value.argumentsHash == hash && !value.used && time.Now().Before(value.expires) {
				return call, "", fmt.Errorf("MCP 相同操作仍待追加輸入，未重新派送；請使用 %s=%s 續接", mcpContinuationArgument, id)
			}
		}
		return call, "", nil
	}
	id, ok := raw.(string)
	if !ok || id == "" {
		return call, "", fmt.Errorf("MCP 續接 ID 必須是非空字串")
	}
	m.continuationMu.Lock()
	defer m.continuationMu.Unlock()
	value := m.continuations[id]
	if value == nil || value.owner != owner || value.server != server || value.tool != call.Name || time.Now().After(value.expires) || value.used {
		return call, "", fmt.Errorf("MCP 續接已失效、已使用或不屬於此對話；請先查證遠端狀態，不得重開原始操作")
	}
	if continuationArgumentsHash(call.Arguments) != value.argumentsHash {
		return call, "", fmt.Errorf("MCP 續接不可變更原始業務參數")
	}
	if value.contractID != "" && value.contractID != domain.ToolContractID(tool.definition) {
		return call, "", fmt.Errorf("MCP 工具契約已改變，不能沿用舊續接狀態；請先查證遠端工作")
	}
	if _, supplied := call.Arguments[mcpInputResponsesArgument]; !supplied {
		return call, "", fmt.Errorf("MCP 續接缺少 _mcp_input_responses")
	}
	if _, err := decodeMCPInputResponses(call.Arguments[mcpInputResponsesArgument]); err != nil {
		return call, "", err
	}
	// 派送前消耗 ID；失敗不自動釋放，避免未知結果被重送。opaque state 不進 transcript。
	value.used = true
	arguments := make(map[string]any, len(call.Arguments)+1)
	for key, item := range call.Arguments {
		arguments[key] = item
	}
	arguments[mcpRequestStateArgument] = value.requestState
	call.Arguments = arguments
	return call, value.callID, nil
}
