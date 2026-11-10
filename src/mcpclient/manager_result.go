package mcpclient

import (
	"AgenticService/src/domain"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (m *Manager) executionFromResult(call domain.ToolCall, config ServerConfig, result *mcp.CallToolResult, owners ...string) domain.ToolExecution {
	execution := domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Details: map[string]any{
		"mcp_server_id": config.ID, "mcp_transport": config.Transport,
	}}
	if result == nil {
		execution.Content = "MCP Server 未回傳結果"
		execution.IsError = true
		return execution
	}
	execution.IsError = result.IsError
	if result.IsError {
		execution.Details["execution_state"] = domain.ToolFailed
	}
	if result.StructuredContent != nil {
		execution.Details["structured_content"] = result.StructuredContent
	}
	if result.NeedsInput() {
		requests, marshalErr := json.Marshal(result.InputRequests)
		if marshalErr == nil {
			execution.Details["mcp_input_requests"] = result.InputRequests
		}
		ownerID := ""
		if len(owners) > 0 {
			ownerID = owners[0]
		}
		continuationID, err := m.saveContinuation(ownerID, config.ID, call, result.RequestState)
		if err != nil {
			execution.IsError = true
			execution.Content = err.Error()
			execution.Details["execution_state"] = domain.ToolOutcomeUnknown
			return execution
		}
		execution.Details["mcp_continuation_id"] = continuationID
		execution.Details["execution_state"] = "awaiting_input"
		execution.Content = "MCP 工具需要額外輸入。沿用相同工具及原始業務參數，將回覆放入 " + mcpInputResponsesArgument + "，並帶上 " + mcpContinuationArgument + "=" + continuationID + "。續接狀態由 Runtime 自動帶回；不可重開原始操作。"
		if len(requests) > 0 {
			execution.Content += "\nMCP 輸入請求：" + string(requests)
		}
		execution.IsError = true
		execution.Details["mcp_input_required"] = true
		return execution
	}
	parts := make([]string, 0, len(result.Content))
	contentTypes := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		switch value := content.(type) {
		case *mcp.TextContent:
			parts = append(parts, value.Text)
			contentTypes = append(contentTypes, "text")
		case *mcp.ResourceLink:
			parts = append(parts, fmt.Sprintf("MCP Resource：%s (%s)", value.Name, value.URI))
			contentTypes = append(contentTypes, "resource_link")
		case *mcp.EmbeddedResource:
			if value.Resource != nil && value.Resource.Text != "" {
				parts = append(parts, value.Resource.Text)
			} else if value.Resource != nil {
				parts = append(parts, fmt.Sprintf("MCP Embedded Resource：%s", value.Resource.URI))
			}
			contentTypes = append(contentTypes, "embedded_resource")
		case *mcp.ImageContent:
			parts = append(parts, fmt.Sprintf("[MCP 圖片：%s，%d bytes]", value.MIMEType, len(value.Data)))
			contentTypes = append(contentTypes, "image")
		case *mcp.AudioContent:
			parts = append(parts, fmt.Sprintf("[MCP 音訊：%s，%d bytes]", value.MIMEType, len(value.Data)))
			contentTypes = append(contentTypes, "audio")
		default:
			encoded, _ := json.Marshal(content)
			parts = append(parts, string(encoded))
			contentTypes = append(contentTypes, "unknown")
		}
	}
	if len(contentTypes) > 0 {
		execution.Details["mcp_content_types"] = contentTypes
	}
	execution.Content = strings.TrimSpace(strings.Join(parts, "\n\n"))
	if result.StructuredContent != nil {
		encoded, _ := json.Marshal(result.StructuredContent)
		// 宣告 output schema 的 MCP Server 會同時回傳 structuredContent 與一段內容
		// 相同的 text block（規格為了相容舊 Client 的要求）。兩份都塞進工具結果，
		// 等於這筆資料在 transcript 裡存兩次、之後每一輪都重讀兩次；只有在文字區塊
		// 沒有涵蓋同一份資料時才附加。
		if len(encoded) > 0 && string(encoded) != "null" && !jsonAlreadyPresent(parts, encoded) {
			structured := "MCP 結構化結果：" + string(encoded)
			if execution.Content == "" {
				execution.Content = structured
			} else {
				execution.Content += "\n\n" + structured
			}
		}
	}
	if execution.Content == "" {
		execution.Content = map[bool]string{true: "MCP 工具執行失敗，但未提供錯誤內容。", false: "MCP 工具已完成。"}[execution.IsError]
	}
	execution.Content = truncateUTF8WithNote(execution.Content, m.maxOutputBytes,
		"\n…（這次的 MCP 輸出過長已截斷。這是 Harness 的內部狀態，不要轉述給使用者，"+
			"也不要請使用者自己去查記錄；需要完整資料時縮小查詢範圍或加上篩選條件重新呼叫工具。）")
	return execution
}

// jsonAlreadyPresent 判斷某個文字區塊是否就是同一份 JSON 資料。
// 比對解碼後的值而不是字串，因為欄位順序與空白都可能不同。
func jsonAlreadyPresent(parts []string, encoded []byte) bool {
	var structured any
	if err := json.Unmarshal(encoded, &structured); err != nil {
		return false
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(part), &value); err != nil {
			continue
		}
		if reflect.DeepEqual(value, structured) {
			return true
		}
	}
	return false
}

func failed(call domain.ToolCall, message string) domain.ToolExecution {
	return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: message, IsError: true}
}

func truncateUTF8(value string, limit int) string {
	return truncateUTF8WithNote(value, limit, "\n…（MCP 輸出已截斷）")
}

// truncateUTF8WithNote 讓工具結果帶上「下一步該做什麼」，Server instructions
// 則維持單純的截斷說明——對 instructions 講「重新查詢」是沒有意義的。
func truncateUTF8WithNote(value string, limit int, note string) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	data := []byte(value[:limit])
	for len(data) > 0 && !utf8.Valid(data) {
		data = data[:len(data)-1]
	}
	return string(data) + note
}
