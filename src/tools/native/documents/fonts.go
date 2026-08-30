package documents

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/image/font/sfnt"
)

const (
	maxFontFileBytes      = int64(64 * 1024 * 1024)
	maxFontCandidates     = 4096
	maxFontCoverageRunes  = 512
	defaultFontMatchLimit = 20
)

type FontTool struct {
	MaxOutputBytes int
}

type fontCandidate struct {
	Path  string
	Scope string
}

type fontCoverage struct {
	Name          string   `json:"name"`
	File          string   `json:"file"`
	Scope         string   `json:"scope"`
	Collection    int      `json:"collection_index,omitempty"`
	Complete      bool     `json:"complete"`
	CoveredRunes  int      `json:"covered_runes"`
	RequiredRunes int      `json:"required_runes"`
	Missing       []string `json:"missing,omitempty"`
	path          string
}

var (
	systemFontsOnce sync.Once
	systemFonts     []fontCandidate
)

func NewFontTool(maxOutputBytes int) *FontTool {
	return &FontTool{MaxOutputBytes: normalizedOutputLimit(maxOutputBytes)}
}

func (t *FontTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:         "document_fonts",
		Label:        "檢查文件字型",
		Version:      "1.0.0",
		Category:     "documents",
		Description:  "探測目前系統可供文件輸出使用的 TrueType 字型，並檢查指定文字的字形覆蓋率。結果不揭露使用者目錄等絕對路徑；document_create 建立 Unicode PDF 時也會使用相同機制自動選擇完整覆蓋的字型。",
		Platforms:    []string{"darwin", "linux", "windows"},
		Capabilities: []string{"font-discovery", "glyph-coverage", "unicode", "pdf", "privacy-safe-output"},
		ReadOnly:     true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":  map[string]any{"type": "string", "maxLength": 4096, "description": "要檢查字形覆蓋率的文字；省略時列出可解析字型"},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": defaultFontMatchLimit},
			},
		},
	}
}

func (t *FontTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	text := toolutil.String(invocation.Call.Arguments, "text")
	if len([]rune(text)) > 4096 {
		return documentFailure(invocation.Call, "text exceeds 4096 characters"), nil
	}
	limit := toolutil.Int(invocation.Call.Arguments, "limit", defaultFontMatchLimit, 1, 100)
	matches, scanned, err := inspectSystemFonts(ctx, text, limit)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	encoded, err := json.MarshalIndent(map[string]any{
		"requested_runes": len(requiredFontRunes(text)),
		"scanned_fonts":   scanned,
		"matches":         matches,
	}, "", "  ")
	if err != nil {
		return documentFailure(invocation.Call, fmt.Sprintf("encode font inspection: %v", err)), nil
	}
	content, truncated := limitUTF8(string(encoded), t.MaxOutputBytes)
	return domain.ToolExecution{
		ToolCallID: invocation.Call.ID,
		ToolName:   invocation.Call.Name,
		Content:    content,
		Details: map[string]any{
			"scanned_fonts": scanned,
			"match_count":   len(matches),
			"truncated":     truncated,
		},
	}, nil
}

func inspectSystemFonts(ctx context.Context, text string, limit int) ([]fontCoverage, int, error) {
	candidates := discoveredSystemFonts()
	required := requiredFontRunes(text)
	matches := make([]fontCoverage, 0, limit)
	partial := make([]fontCoverage, 0, limit)
	scanned := 0
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, scanned, err
		}
		coverages, err := inspectFontCandidate(candidate, required)
		if err != nil {
			continue
		}
		for _, coverage := range coverages {
			scanned++
			if len(required) == 0 || coverage.Complete {
				matches = append(matches, coverage)
				if len(matches) >= limit {
					return matches, scanned, nil
				}
			} else {
				partial = append(partial, coverage)
			}
		}
	}
	if len(matches) == 0 && len(required) > 0 {
		sort.SliceStable(partial, func(i, j int) bool {
			if partial[i].CoveredRunes != partial[j].CoveredRunes {
				return partial[i].CoveredRunes > partial[j].CoveredRunes
			}
			return partial[i].Name < partial[j].Name
		})
		if len(partial) > limit {
			partial = partial[:limit]
		}
		matches = partial
	}
	return matches, scanned, nil
}

func findSystemFontForText(ctx context.Context, text string) (fontCoverage, bool) {
	required := requiredFontRunes(text)
	if len(required) == 0 {
		return fontCoverage{}, false
	}
	for _, candidate := range discoveredSystemFonts() {
		if ctx.Err() != nil {
			return fontCoverage{}, false
		}
		coverages, err := inspectFontCandidate(candidate, required)
		if err != nil {
			continue
		}
		for _, coverage := range coverages {
			if coverage.Complete && coverage.Collection == 0 && strings.EqualFold(filepath.Ext(coverage.path), ".ttf") {
				return coverage, true
			}
		}
	}
	return fontCoverage{}, false
}

func discoveredSystemFonts() []fontCandidate {
	systemFontsOnce.Do(func() {
		seen := map[string]bool{}
		roots := fontSearchRoots()
		for _, root := range roots {
			if len(systemFonts) >= maxFontCandidates {
				break
			}
			root = strings.TrimSpace(root)
			if root == "" {
				continue
			}
			_ = filepath.WalkDir(root, func(candidatePath string, entry os.DirEntry, err error) error {
				if err != nil || len(systemFonts) >= maxFontCandidates {
					return nil
				}
				if entry.IsDir() {
					return nil
				}
				extension := strings.ToLower(filepath.Ext(entry.Name()))
				if extension != ".ttf" && extension != ".otf" && extension != ".ttc" && extension != ".otc" {
					return nil
				}
				cleaned := filepath.Clean(candidatePath)
				if seen[cleaned] {
					return nil
				}
				info, statErr := entry.Info()
				if statErr != nil || info.Size() <= 0 || info.Size() > maxFontFileBytes {
					return nil
				}
				seen[cleaned] = true
				systemFonts = append(systemFonts, fontCandidate{Path: cleaned, Scope: fontScope(cleaned)})
				return nil
			})
		}
		sort.SliceStable(systemFonts, func(i, j int) bool {
			left, right := fontPriority(systemFonts[i].Path), fontPriority(systemFonts[j].Path)
			if left != right {
				return left < right
			}
			return strings.ToLower(systemFonts[i].Path) < strings.ToLower(systemFonts[j].Path)
		})
	})
	return systemFonts
}

func fontSearchRoots() []string {
	home, _ := os.UserHomeDir()
	executable, _ := os.Executable()
	resources := filepath.Join(filepath.Dir(executable), "resources", "fonts")
	macResources := filepath.Join(filepath.Dir(executable), "..", "Resources", "fonts")
	switch runtime.GOOS {
	case "darwin":
		return []string{resources, macResources, filepath.Join(home, "Library", "Fonts"), "/Library/Fonts", "/System/Library/Fonts"}
	case "windows":
		return []string{resources, filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Windows", "Fonts"), filepath.Join(os.Getenv("WINDIR"), "Fonts")}
	default:
		return []string{resources, filepath.Join(home, ".local", "share", "fonts"), filepath.Join(home, ".fonts"), "/usr/local/share/fonts", "/usr/share/fonts"}
	}
}

func fontScope(fontPath string) string {
	home, _ := os.UserHomeDir()
	if home != "" {
		if relative, err := filepath.Rel(home, fontPath); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "user"
		}
	}
	executable, _ := os.Executable()
	if executable != "" {
		if relative, err := filepath.Rel(filepath.Dir(executable), fontPath); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "bundled"
		}
	}
	return "system"
}

func fontPriority(fontPath string) int {
	name := strings.ToLower(filepath.Base(fontPath))
	for index, keyword := range []string{"notosanstc", "notosanscjk", "sourcehansans", "microsoftjhenghei", "arial unicode", "dejavusans", "arial"} {
		if strings.Contains(name, keyword) {
			return index
		}
	}
	return 100
}

func inspectFontCandidate(candidate fontCandidate, required []rune) ([]fontCoverage, error) {
	data, err := os.ReadFile(candidate.Path)
	if err != nil || int64(len(data)) > maxFontFileBytes {
		return nil, fmt.Errorf("read font: %w", err)
	}
	collection, err := sfnt.ParseCollection(data)
	if err != nil {
		return nil, err
	}
	result := make([]fontCoverage, 0, collection.NumFonts())
	for index := 0; index < collection.NumFonts(); index++ {
		font, fontErr := collection.Font(index)
		if fontErr != nil {
			continue
		}
		var buffer sfnt.Buffer
		name, _ := font.Name(&buffer, sfnt.NameIDFull)
		if strings.TrimSpace(name) == "" {
			name, _ = font.Name(&buffer, sfnt.NameIDFamily)
		}
		if strings.TrimSpace(name) == "" {
			name = strings.TrimSuffix(filepath.Base(candidate.Path), filepath.Ext(candidate.Path))
		}
		missing := make([]string, 0)
		covered := 0
		for _, character := range required {
			glyph, glyphErr := font.GlyphIndex(&buffer, character)
			if glyphErr != nil || glyph == 0 {
				if len(missing) < 16 {
					missing = append(missing, string(character))
				}
				continue
			}
			covered++
		}
		result = append(result, fontCoverage{
			Name: name, File: filepath.Base(candidate.Path), Scope: candidate.Scope, Collection: index,
			Complete: covered == len(required), CoveredRunes: covered, RequiredRunes: len(required), Missing: missing, path: candidate.Path,
		})
	}
	return result, nil
}

func requiredFontRunes(text string) []rune {
	seen := map[rune]bool{}
	result := make([]rune, 0)
	for _, character := range text {
		if unicode.IsSpace(character) || unicode.IsControl(character) || seen[character] {
			continue
		}
		seen[character] = true
		result = append(result, character)
		if len(result) == maxFontCoverageRunes {
			break
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func pdfRequestText(request documentCreateRequest) string {
	var text strings.Builder
	text.WriteString(request.Title)
	for _, block := range request.Blocks {
		text.WriteRune('\n')
		text.WriteString(block.Text)
		for _, row := range block.Rows {
			for _, cell := range row {
				text.WriteRune('\n')
				text.WriteString(cell)
			}
		}
	}
	return text.String()
}
