package files

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type SearchTool struct {
	MaxFileBytes int64
	MaxFiles     int
}

type SearchMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Preview string `json:"preview,omitempty"`
}

func NewSearchTool(maxFileBytes int64, maxFiles int) *SearchTool {
	if maxFileBytes <= 0 {
		maxFileBytes = 2 * 1024 * 1024
	}
	if maxFiles <= 0 {
		maxFiles = 10_000
	}
	return &SearchTool{MaxFileBytes: maxFileBytes, MaxFiles: maxFiles}
}

func (t *SearchTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:         "file_search",
		Label:        "搜尋檔案",
		Version:      "1.0.0",
		Category:     "files",
		Description:  "在 Project／Session Sandbox 內依文字、正規表示式或檔名搜尋。大型目錄應先用 filename 或具體文字查詢、include/exclude_dirs 與 max_results 縮小候選集；不呼叫 grep、find 或其他 OS 指令。",
		Platforms:    []string{"darwin", "linux", "windows"},
		Capabilities: []string{"content-search", "regex", "filename-search", "workspace-sandbox"},
		ReadOnly:     true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":          map[string]any{"type": "string"},
				"path":           map[string]any{"type": "string", "default": "."},
				"mode":           map[string]any{"type": "string", "enum": []string{"text", "regex", "filename"}, "default": "text"},
				"include":        map[string]any{"type": "string", "description": "可選的 filepath glob，例如 *.go"},
				"case_sensitive": map[string]any{"type": "boolean", "default": false},
				"max_results":    map[string]any{"type": "integer", "minimum": 1, "maximum": 500, "default": 50},
				"exclude_dirs":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"required": []string{"query"},
		},
	}
}

func (t *SearchTool) Execute(ctx context.Context, invocation tools.Invocation, sink ports.ToolUpdateSink) (domain.ToolExecution, error) {
	arguments := invocation.Call.Arguments
	sandboxRoots := invocation.SandboxRoots()
	query := toolutil.String(arguments, "query")
	if query == "" {
		return fileFailure(invocation.Call, "query is required"), nil
	}
	root, err := toolutil.ResolvePathInRoots(sandboxRoots, toolutil.String(arguments, "path"), true)
	if err != nil {
		return fileFailure(invocation.Call, err.Error()), nil
	}
	mode := strings.ToLower(toolutil.String(arguments, "mode"))
	if mode == "" {
		mode = "text"
	}
	caseSensitive := toolutil.Bool(arguments, "case_sensitive", false)
	include := toolutil.String(arguments, "include")
	maxResults := toolutil.Int(arguments, "max_results", 50, 1, 500)
	excluded := excludedDirectorySet(toolutil.StringSlice(arguments, "exclude_dirs"))
	matcher, err := buildMatcher(query, mode, caseSensitive)
	if err != nil {
		return fileFailure(invocation.Call, err.Error()), nil
	}

	matches := []SearchMatch{}
	filesScanned := 0
	filesSkipped := 0
	stopped := false
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			filesSkipped++
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root {
				if _, skip := excluded[strings.ToLower(entry.Name())]; skip {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || filesScanned >= t.MaxFiles {
			filesSkipped++
			return nil
		}
		relative := toolutil.DisplayPathInRoots(sandboxRoots, path)
		if !included(relative, entry.Name(), include) {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() || info.Size() > t.MaxFileBytes {
			filesSkipped++
			return nil
		}
		filesScanned++
		if filesScanned%250 == 0 && sink != nil {
			_ = sink(domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Details: map[string]any{"files_scanned": filesScanned, "matches": len(matches)}})
		}
		if mode == "filename" {
			if column := matcher(entry.Name()); column >= 0 {
				matches = append(matches, SearchMatch{Path: relative, Column: column + 1, Preview: entry.Name()})
			}
		} else {
			fileMatches, searchErr := searchFile(ctx, path, relative, matcher, maxResults-len(matches))
			if searchErr != nil {
				filesSkipped++
				return nil
			}
			matches = append(matches, fileMatches...)
		}
		if len(matches) >= maxResults {
			stopped = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return domain.ToolExecution{}, err
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Path == matches[j].Path {
			return matches[i].Line < matches[j].Line
		}
		return matches[i].Path < matches[j].Path
	})
	data, _ := json.MarshalIndent(matches, "", "  ")
	return domain.ToolExecution{
		ToolCallID: invocation.Call.ID,
		ToolName:   invocation.Call.Name,
		Content:    string(data),
		Details: map[string]any{
			"matches":       matches,
			"match_count":   len(matches),
			"files_scanned": filesScanned,
			"files_skipped": filesSkipped,
			"truncated":     stopped,
		},
	}, nil
}

func buildMatcher(query, mode string, caseSensitive bool) (func(string) int, error) {
	switch mode {
	case "text", "filename":
		needle := query
		if !caseSensitive {
			needle = strings.ToLower(needle)
		}
		return func(value string) int {
			if !caseSensitive {
				value = strings.ToLower(value)
			}
			return strings.Index(value, needle)
		}, nil
	case "regex":
		pattern := query
		if !caseSensitive {
			pattern = "(?i)" + pattern
		}
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("compile regular expression: %w", err)
		}
		return func(value string) int {
			location := compiled.FindStringIndex(value)
			if location == nil {
				return -1
			}
			return location[0]
		}, nil
	default:
		return nil, fmt.Errorf("unsupported search mode %q", mode)
	}
}

func searchFile(ctx context.Context, path, displayPath string, matcher func(string) int, remaining int) ([]SearchMatch, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	matches := []SearchMatch{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lineNumber++
		line := scanner.Text()
		if strings.IndexByte(line, 0) >= 0 {
			return nil, nil
		}
		if column := matcher(line); column >= 0 {
			matches = append(matches, SearchMatch{Path: displayPath, Line: lineNumber, Column: column + 1, Preview: strings.TrimSpace(line)})
			if len(matches) >= remaining {
				break
			}
		}
	}
	return matches, scanner.Err()
}

func included(relative, name, pattern string) bool {
	if pattern == "" {
		return true
	}
	if matched, _ := filepath.Match(pattern, name); matched {
		return true
	}
	matched, _ := filepath.Match(filepath.FromSlash(pattern), filepath.FromSlash(relative))
	return matched
}

func excludedDirectorySet(extra []string) map[string]struct{} {
	result := map[string]struct{}{".git": {}, ".hg": {}, ".svn": {}, "node_modules": {}}
	for _, value := range extra {
		result[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	return result
}
