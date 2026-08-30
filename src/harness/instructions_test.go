package harness

import (
	"strings"
	"testing"
)

func TestInstructionsPromptOrdersScopes(t *testing.T) {
	prompt := instructionsPrompt(map[string]any{"instructions": []any{
		map[string]any{"scope": "workspace", "name": "產品開發", "text": "回覆一律使用繁體中文。"},
		map[string]any{"scope": "project", "name": "NR-Intern", "text": "修改後必須跑 go test。"},
	}})

	workspaceIndex := strings.Index(prompt, "Workspace「產品開發」")
	projectIndex := strings.Index(prompt, "Project「NR-Intern」")
	if workspaceIndex < 0 || projectIndex < 0 {
		t.Fatalf("prompt is missing a scope heading: %s", prompt)
	}
	if workspaceIndex > projectIndex {
		t.Fatalf("workspace instructions must come before project instructions: %s", prompt)
	}
	for _, wanted := range []string{"## 職務說明", "回覆一律使用繁體中文。", "修改後必須跑 go test。", "以本輪要求為準"} {
		if !strings.Contains(prompt, wanted) {
			t.Fatalf("prompt is missing %q: %s", wanted, prompt)
		}
	}
}

func TestInstructionsPromptIsEmptyWithoutUsableEntries(t *testing.T) {
	cases := map[string]map[string]any{
		"no metadata":   {},
		"wrong type":    {"instructions": "文字"},
		"empty list":    {"instructions": []any{}},
		"blank text":    {"instructions": []any{map[string]any{"scope": "project", "text": "   "}}},
		"wrong element": {"instructions": []any{"文字"}},
	}
	for name, metadata := range cases {
		t.Run(name, func(t *testing.T) {
			if prompt := instructionsPrompt(metadata); prompt != "" {
				t.Fatalf("expected an empty prompt, got: %s", prompt)
			}
		})
	}
}
