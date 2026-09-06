package bootstrap

import (
	"AgenticService/src/adapters/openaicompat"
	"AgenticService/src/harness"
	"testing"
)

// Codex 一定走雲端、視窗以 20 萬 token 起跳，沿用全域的 60,000
// 會在約 25% 使用率就被字元閘門壓縮，而介面上看不出原因。
func TestCodexDefaultsRaiseHistoryCharacterLimit(t *testing.T) {
	settings := openaicompat.Config{}
	applyOpenAICodexResponsesDefaults(&settings)
	if settings.MaxHistoryCharacters != codexMaxHistoryCharacters {
		t.Fatalf("Codex 應使用自己的預設 %d，得到 %d", codexMaxHistoryCharacters, settings.MaxHistoryCharacters)
	}
	if codexMaxHistoryCharacters <= harness.DefaultMaxHistoryCharacters {
		t.Fatalf("Codex 的預設要比全域寬鬆，否則這個設定沒有意義：%d vs %d",
			codexMaxHistoryCharacters, harness.DefaultMaxHistoryCharacters)
	}
}

// 使用者明確設定的值不能被預設值蓋掉——包含刻意調低到全域值的情況。
func TestCodexDefaultsKeepExplicitHistoryCharacterLimit(t *testing.T) {
	settings := openaicompat.Config{MaxHistoryCharacters: 30_000}
	applyOpenAICodexResponsesDefaults(&settings)
	if settings.MaxHistoryCharacters != 30_000 {
		t.Fatalf("明確設定的值應保留，得到 %d", settings.MaxHistoryCharacters)
	}
}

// 一般 OpenAI 相容 Provider 可能是本機模型，必須維持沿用全域的行為。
func TestOpenAICompatibleDefaultsLeaveHistoryCharacterLimitAlone(t *testing.T) {
	settings := openaicompat.Config{}
	applyOpenAICompatibleDefaults(&settings)
	if settings.MaxHistoryCharacters != 0 {
		t.Fatalf("相容 Provider 不該自帶字元上限（0 代表沿用全域），得到 %d", settings.MaxHistoryCharacters)
	}
}
