package bootstrap

import (
	"AgenticService/src/adapters/openaicompat"
	"encoding/json"
	"testing"
)

func TestResponsesSettingRoundTrip(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		config := Config{Providers: map[string]ProviderConfig{
			"smoke": {Type: "openai-compatible", OpenAICompatible: &openaicompat.Config{
				BaseURL: "https://example.invalid/custom/responses", Model: "MixedCase", UseResponses: enabled,
			}},
		}}
		encoded, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		var restored Config
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		view := providerSettingsView(restored, nil)
		if len(view.Providers) != 1 || view.Providers[0].OpenAICompatible.UseResponses != enabled {
			t.Fatal("Responses setting lost during persistence or API view")
		}
	}
}
