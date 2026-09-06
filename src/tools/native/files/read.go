package files

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
)

type ReadTool struct {
	MaxOutputBytes int
}

func NewReadTool(maxOutputBytes int) *ReadTool {
	if maxOutputBytes <= 0 {
		maxOutputBytes = 512 * 1024
	}
	return &ReadTool{MaxOutputBytes: maxOutputBytes}
}

func (t *ReadTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:         "file_read",
		Label:        "讀取檔案",
		Version:      "1.0.0",
		Category:     "files",
		Description:  "讀取 Project／Session Sandbox 內的文字檔，可指定起訖行。大型檔案應用 start_line/end_line 分段讀取需要的區域，不應一次讀完整檔案；行為不依賴作業系統 shell。",
		Platforms:    []string{"darwin", "linux", "windows"},
		Capabilities: []string{"read", "line-range", "workspace-sandbox"},
		ReadOnly:     true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string", "description": "Sandbox 內的相對或絕對檔案路徑"},
				"start_line": map[string]any{"type": "integer", "minimum": 1, "default": 1, "description": "起始行號（從 1 起算，包含此行）"},
				"end_line": map[string]any{"type": "integer", "minimum": 1,
					"description": "結束行號，**絕對行號、不是行數**，且包含此行。要讀第 400 行起的 170 行，填 start_line=400、end_line=569。省略表示讀到輸出上限"},
			},
			"required": []string{"path"},
		},
	}
}

func (t *ReadTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	requested := toolutil.String(invocation.Call.Arguments, "path")
	if requested == "" {
		return fileFailure(invocation.Call, "path is required"), nil
	}
	sandboxRoots := invocation.SandboxRoots()
	path, err := toolutil.ResolvePathInRoots(sandboxRoots, requested, true)
	if err != nil {
		return fileFailure(invocation.Call, err.Error()), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fileFailure(invocation.Call, err.Error()), nil
	}
	if !info.Mode().IsRegular() {
		return fileFailure(invocation.Call, "path is not a regular file"), nil
	}
	file, err := os.Open(path)
	if err != nil {
		return fileFailure(invocation.Call, err.Error()), nil
	}
	defer file.Close()

	startLine := toolutil.Int(invocation.Call.Arguments, "start_line", 1, 1, 10_000_000)
	endLine := toolutil.Int(invocation.Call.Arguments, "end_line", 0, 0, 10_000_000)
	if endLine > 0 && endLine < startLine {
		// 訊息要說出收到什麼、正確的意思是什麼、以及這次該怎麼填。
		// 只說「不合法」的話，模型只能猜著重試——實測連續三次都把 end_line
		// 當成「要讀幾行」，每次都要先失敗一輪。
		return fileFailure(invocation.Call, fmt.Sprintf(
			"end_line (%d) must be greater than or equal to start_line (%d). "+
				"end_line 是絕對行號，不是要讀的行數；若想從第 %d 行起讀 %d 行，請用 end_line=%d",
			endLine, startLine, startLine, endLine, startLine+endLine-1)), nil
	}
	var output strings.Builder
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	lineNumber := 0
	lastLine := 0
	truncated := false
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return domain.ToolExecution{}, err
		}
		lineNumber++
		if lineNumber < startLine {
			continue
		}
		if endLine > 0 && lineNumber > endLine {
			break
		}
		line := fmt.Sprintf("%6d | %s\n", lineNumber, scanner.Text())
		if output.Len()+len(line) > t.MaxOutputBytes {
			truncated = true
			break
		}
		output.WriteString(line)
		lastLine = lineNumber
	}
	if err := scanner.Err(); err != nil {
		return fileFailure(invocation.Call, fmt.Sprintf("read file: %v", err)), nil
	}
	if truncated {
		output.WriteString(fmt.Sprintf("[output truncated; continue with start_line=%d or narrow end_line]\n", lastLine+1))
	}
	return domain.ToolExecution{
		ToolCallID: invocation.Call.ID,
		ToolName:   invocation.Call.Name,
		Content:    output.String(),
		Details: map[string]any{
			"path":       toolutil.DisplayPathInRoots(sandboxRoots, path),
			"start_line": startLine,
			"end_line":   lastLine,
			"truncated":  truncated,
		},
	}, nil
}

func fileFailure(call domain.ToolCall, message string) domain.ToolExecution {
	return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: strings.TrimSpace(message), IsError: true}
}
