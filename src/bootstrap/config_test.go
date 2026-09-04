package bootstrap

import (
	"AgenticService/src/domain"
	"log/slog"
	"testing"
)

func TestDefaultConfigAllowsSandboxedWriteTools(t *testing.T) {
	config := DefaultConfig()
	if config.ServiceName != "永不休息的實習生" {
		t.Fatalf("default service name = %q", config.ServiceName)
	}
	if !config.AllowElevatedTools {
		t.Fatal("default config must expose sandboxed write and shell tools")
	}
	policy := config.Permissions.Normalize()
	if !policy.IsElevated(policy.DefaultProfile) {
		t.Fatalf("default profile %q must be elevated when local write tools are enabled", policy.DefaultProfile)
	}
	if config.MaxTokens != 0 || config.MaxToolCalls != 0 {
		t.Fatalf("long-task count limits must default to unlimited, got tokens=%d tool_calls=%d", config.MaxTokens, config.MaxToolCalls)
	}
	if !config.HTTPFetch.AllowPrivateNetworks {
		t.Fatal("http_fetch must allow localhost and private networks by default")
	}
	if !config.RAMDisk.Enabled || config.RAMDisk.SizeMB != DefaultRAMDiskSizeMB {
		t.Fatalf("RAM disk defaults = %+v", config.RAMDisk)
	}
}

func TestValidateConfigRejectsInvalidRAMDiskSize(t *testing.T) {
	config := DefaultConfig()
	config.RAMDisk.SizeMB = minRAMDiskSizeMB - 1
	if err := validateConfig(&config); err == nil {
		t.Fatal("RAM disk size below the safe minimum was accepted")
	}
	config.RAMDisk.SizeMB = maxRAMDiskSizeMB + 1
	if err := validateConfig(&config); err == nil {
		t.Fatal("RAM disk size above the safe maximum was accepted")
	}
}

func TestValidateAdjustableRunLimitsAllowsUnlimitedCounts(t *testing.T) {
	if err := validateAdjustableRunLimits(2*60*60, 0, 0); err != nil {
		t.Fatalf("unlimited count settings were rejected: %v", err)
	}
	for _, test := range []struct {
		name      string
		wallClock int
		tokens    int
		toolCalls int
	}{
		{name: "zero wall clock", wallClock: 0},
		{name: "negative tokens", wallClock: 1, tokens: -1},
		{name: "negative tool calls", wallClock: 1, toolCalls: -1},
		{name: "tool calls too large", wallClock: 1, toolCalls: 10_001},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateAdjustableRunLimits(test.wallClock, test.tokens, test.toolCalls); err == nil {
				t.Fatal("invalid run limits were accepted")
			}
		})
	}
}

func TestProviderEnabledDefaultsToTrue(t *testing.T) {
	if !(ProviderConfig{}).IsEnabled() {
		t.Fatal("provider without enabled field must remain enabled for backward compatibility")
	}
	disabled := false
	if (ProviderConfig{Enabled: &disabled}).IsEnabled() {
		t.Fatal("explicitly disabled provider was treated as enabled")
	}
}

func TestValidateConfigRejectsDisabledDefaultProvider(t *testing.T) {
	config := DefaultConfig()
	disabled := false
	provider := config.Providers[config.DefaultProviderID]
	provider.Enabled = &disabled
	config.Providers[config.DefaultProviderID] = provider
	if err := validateConfig(&config); err == nil {
		t.Fatal("disabled default provider was accepted")
	}
}

func TestValidateConfigNormalizesModelPrices(t *testing.T) {
	config := DefaultConfig()
	config.ModelPrices = map[string]map[string]domain.ModelPrice{
		"openai-compatible": {
			" gpt-4o-mini ": {InputPerMillion: 0.15, OutputPerMillion: 0.6},
		},
	}
	if err := validateConfig(&config); err != nil {
		t.Fatalf("validateConfig: %v", err)
	}
	price, ok := config.ModelPrices["openai-compatible"]["gpt-4o-mini"]
	if !ok || price.Currency != "USD" {
		t.Fatalf("normalized model price = %+v, want USD entry", config.ModelPrices)
	}
}

func TestValidateConfigRejectsInvalidModelPrice(t *testing.T) {
	config := DefaultConfig()
	config.ModelPrices = map[string]map[string]domain.ModelPrice{
		"openai-compatible": {"gpt-4o-mini": {InputPerMillion: -1}},
	}
	if err := validateConfig(&config); err == nil {
		t.Fatal("negative model price was accepted")
	}
}

func TestBuildProviderValuesExcludesDisabledProviders(t *testing.T) {
	config := DefaultConfig()
	disabled := config.Providers[config.DefaultProviderID]
	disabled.Enabled = boolPointer(false)
	config.Providers["disabled-provider"] = disabled
	values, err := buildProviderValues(config, slog.Default(), nil)
	if err != nil {
		t.Fatalf("buildProviderValues: %v", err)
	}
	if _, exists := values["disabled-provider"]; exists {
		t.Fatal("disabled provider was registered in the runtime router")
	}
	if _, exists := values[config.DefaultProviderID]; !exists {
		t.Fatal("enabled default provider is missing from the runtime router")
	}
}

func TestPlanningToolsAreAvailableForDefaultAndLegacyAllowlists(t *testing.T) {
	for _, values := range [][]string{DefaultConfig().AllowedTools, ensurePlanningTools([]string{"shell_exec"})} {
		for _, name := range []string{"plan_get", "plan_create", "plan_step_update"} {
			if !containsString(values, name) {
				t.Fatalf("planning tool %q is missing from %v", name, values)
			}
		}
	}
	if values := ensurePlanningTools(nil); values != nil {
		t.Fatalf("empty allowlist must remain unrestricted, got %v", values)
	}
}
