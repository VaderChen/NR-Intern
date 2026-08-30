package bootstrap

import (
	"AgenticService/src/domain"
	"context"
	"testing"
)

func TestUpdateServiceSettingsPersistsName(t *testing.T) {
	dataDir := t.TempDir()
	runtime := &Runtime{Config: Config{
		DataDir:             dataDir,
		ServiceName:         DefaultServiceName,
		UILanguage:          DefaultUILanguage,
		MaxWallClockSeconds: 7200,
		MaxTokens:           10_000_000,
		MaxToolCalls:        512,
	}}
	wallClock, tokens, toolCalls, uiLanguage := 10_800, 0, 0, "ja"

	updated, err := runtime.UpdateServiceSettings(context.Background(), domain.UpdateServiceSettingsInput{
		ServiceName:         "我的助理",
		UILanguage:          &uiLanguage,
		MaxWallClockSeconds: &wallClock,
		MaxTokens:           &tokens,
		MaxToolCalls:        &toolCalls,
	})
	if err != nil {
		t.Fatalf("UpdateServiceSettings: %v", err)
	}
	if updated.ServiceName != "我的助理" || updated.UILanguage != "ja" || updated.MaxWallClockSeconds != wallClock || updated.MaxTokens != 0 || updated.MaxToolCalls != 0 || runtime.Status().Name != "我的助理" {
		t.Fatalf("updated settings = %+v, status = %+v", updated, runtime.Status())
	}

	loaded := Config{DataDir: dataDir, ServiceName: DefaultServiceName, UILanguage: DefaultUILanguage, MaxWallClockSeconds: 7200, MaxTokens: 1, MaxToolCalls: 1}
	if err := loadPersistedServiceSettings(&loaded); err != nil {
		t.Fatalf("loadPersistedServiceSettings: %v", err)
	}
	if loaded.ServiceName != "我的助理" || loaded.UILanguage != "ja" || loaded.MaxWallClockSeconds != wallClock || loaded.MaxTokens != 0 || loaded.MaxToolCalls != 0 {
		t.Fatalf("persisted service settings = %+v", loaded)
	}
}

func TestUpdateServiceSettingsRejectsEmptyName(t *testing.T) {
	runtime := &Runtime{Config: Config{DataDir: t.TempDir(), ServiceName: DefaultServiceName, MaxWallClockSeconds: 7200}}
	if _, err := runtime.UpdateServiceSettings(context.Background(), domain.UpdateServiceSettingsInput{}); err == nil {
		t.Fatal("empty service name was accepted")
	}
}

func TestUpdateServiceSettingsPreservesOmittedRunLimits(t *testing.T) {
	runtime := &Runtime{Config: Config{
		DataDir:             t.TempDir(),
		ServiceName:         DefaultServiceName,
		UILanguage:          "ko",
		MaxWallClockSeconds: 7200,
		MaxTokens:           1234,
		MaxToolCalls:        56,
	}}
	updated, err := runtime.UpdateServiceSettings(context.Background(), domain.UpdateServiceSettingsInput{ServiceName: "新名稱"})
	if err != nil {
		t.Fatalf("UpdateServiceSettings: %v", err)
	}
	if updated.UILanguage != "ko" || updated.MaxWallClockSeconds != 7200 || updated.MaxTokens != 1234 || updated.MaxToolCalls != 56 {
		t.Fatalf("omitted run limits changed: %+v", updated)
	}
}

func TestPersistedPrivateNetworkSettingOverridesDefault(t *testing.T) {
	dataDir := t.TempDir()
	if err := persistServiceSettings(dataDir, domain.ServiceSettings{
		ServiceName: DefaultServiceName,
		HTTPFetch: domain.HTTPFetchSettings{
			AllowPrivateNetworks: false,
		},
	}); err != nil {
		t.Fatalf("persistServiceSettings: %v", err)
	}

	loaded := Config{
		DataDir: dataDir,
		HTTPFetch: HTTPFetchConfig{
			AllowPrivateNetworks: true,
		},
	}
	if err := loadPersistedServiceSettings(&loaded); err != nil {
		t.Fatalf("loadPersistedServiceSettings: %v", err)
	}
	if loaded.HTTPFetch.AllowPrivateNetworks {
		t.Fatal("explicitly disabled private-network access was replaced by the default")
	}
}

func TestUpdateServiceSettingsRejectsUnsupportedUILanguage(t *testing.T) {
	runtime := &Runtime{Config: Config{
		DataDir:             t.TempDir(),
		ServiceName:         DefaultServiceName,
		UILanguage:          DefaultUILanguage,
		MaxWallClockSeconds: 7200,
	}}
	uiLanguage := "fr"
	if _, err := runtime.UpdateServiceSettings(context.Background(), domain.UpdateServiceSettingsInput{
		ServiceName: DefaultServiceName,
		UILanguage:  &uiLanguage,
	}); err == nil {
		t.Fatal("unsupported UI language was accepted")
	}
}
