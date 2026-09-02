package harness

import (
	"strings"
	"testing"
)

// 收斂階段的提示必須要求答案帶上依據。只回一句結論（例如「目前共有 264 筆製令。」）
// 數字就算是對的，使用者也無從判斷查了哪個服務、用什麼條件、資料是什麼時候的。
func TestFinalizationPromptRequiresEvidenceInTheAnswer(t *testing.T) {
	cases := map[string]string{
		"normal":     finalizationPhasePrompt(2, 0, false, "", ""),
		"forced":     finalizationPhasePrompt(8, 8, true, "", ""),
		"loop guard": finalizationPhasePrompt(3, 0, true, "重複寫入同一檔案。", ""),
	}
	for name, prompt := range cases {
		t.Run(name, func(t *testing.T) {
			for _, expected := range []string{"實際查了什麼", "查詢範圍與過濾條件", "長度與問題複雜度相稱"} {
				if !strings.Contains(prompt, expected) {
					t.Fatalf("finalization prompt is missing %q: %s", expected, prompt)
				}
			}
		})
	}
}

// 這段規則只能出現在工具結果已進入 history 的收斂階段；純聊天的回答不該被要求
// 交代工具依據。
func TestEvidenceRulesAreScopedToFinalization(t *testing.T) {
	for name, prompt := range map[string]string{
		"tool selection": toolSelectionPhasePrompt(false, nil),
		"exploration":    explorationPhasePrompt(false, nil),
		"progress":       progressPresentationPrompt(""),
	} {
		if strings.Contains(prompt, "實際查了什麼") {
			t.Fatalf("%s prompt must not carry the answer evidence rules", name)
		}
	}
}
