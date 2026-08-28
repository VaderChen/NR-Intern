package bootstrap

import (
	"AgenticService/src/domain"
	"context"
	"testing"
)

func TestUpdateServiceSettingsPersistsName(t *testing.T) {
	dataDir := t.TempDir()
	runtime := &Runtime{Config: Config{DataDir: dataDir, ServiceName: DefaultServiceName}}

	updated, err := runtime.UpdateServiceSettings(context.Background(), domain.UpdateServiceSettingsInput{ServiceName: "我的助理"})
	if err != nil {
		t.Fatalf("UpdateServiceSettings: %v", err)
	}
	if updated.ServiceName != "我的助理" || runtime.Status().Name != "我的助理" {
		t.Fatalf("updated settings = %+v, status = %+v", updated, runtime.Status())
	}

	loaded := Config{DataDir: dataDir, ServiceName: DefaultServiceName}
	if err := loadPersistedServiceSettings(&loaded); err != nil {
		t.Fatalf("loadPersistedServiceSettings: %v", err)
	}
	if loaded.ServiceName != "我的助理" {
		t.Fatalf("persisted service name = %q", loaded.ServiceName)
	}
}

func TestUpdateServiceSettingsRejectsEmptyName(t *testing.T) {
	runtime := &Runtime{Config: Config{DataDir: t.TempDir(), ServiceName: DefaultServiceName}}
	if _, err := runtime.UpdateServiceSettings(context.Background(), domain.UpdateServiceSettingsInput{}); err == nil {
		t.Fatal("empty service name was accepted")
	}
}
