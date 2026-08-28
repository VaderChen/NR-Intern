package domain

import (
	"errors"
	"testing"
)

// TestResolveRejectsClientRequestedElevation 覆蓋原本的提權漏洞：
// permission profile 由 API request body 指定，而 elevated 工具的閘門就是查這個值，
// 等於任何持有 token 的呼叫端都能自行宣告 trusted。
func TestResolveRejectsClientRequestedElevation(t *testing.T) {
	policy := PermissionPolicy{DefaultProfile: "default", ElevatedProfiles: []string{"trusted"}}

	if _, err := policy.Resolve("trusted"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestResolveDefaultsWhenUnspecified(t *testing.T) {
	policy := PermissionPolicy{DefaultProfile: "default", ElevatedProfiles: []string{"trusted"}}

	profile, err := policy.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if profile != "default" {
		t.Fatalf("profile = %q, want default", profile)
	}
}

func TestResolveAllowsKnownProfilesWhenClientChoiceEnabled(t *testing.T) {
	policy := PermissionPolicy{DefaultProfile: "default", ElevatedProfiles: []string{"trusted"}, AllowClientChoice: true}

	profile, err := policy.Resolve("TRUSTED")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if profile != "trusted" {
		t.Fatalf("profile = %q, want trusted", profile)
	}
}

func TestResolveRejectsUnknownProfileEvenWithClientChoice(t *testing.T) {
	policy := PermissionPolicy{DefaultProfile: "default", ElevatedProfiles: []string{"trusted"}, AllowClientChoice: true}

	if _, err := policy.Resolve("danger-full-access"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput for an unconfigured profile", err)
	}
}

func TestIsElevatedFailsClosedWithoutConfiguredProfiles(t *testing.T) {
	policy := PermissionPolicy{DefaultProfile: "default"}

	for _, profile := range []string{"trusted", "auto", "bypass", "danger-full-access", "default", ""} {
		if policy.IsElevated(profile) {
			t.Errorf("profile %q must not be elevated when none are configured", profile)
		}
	}
}

// TestBackendMayDeclareTheDefaultProfileElevated 保留「整個後端受信任」的本機部署方式，
// 但這必須是後端設定寫出來的決定，而不是呼叫端宣告的結果。
func TestBackendMayDeclareTheDefaultProfileElevated(t *testing.T) {
	policy := PermissionPolicy{DefaultProfile: "trusted", ElevatedProfiles: []string{"trusted"}}

	profile, err := policy.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !policy.IsElevated(profile) {
		t.Fatal("an explicitly configured elevated default profile must stay elevated")
	}
	if got, want := len(policy.KnownProfiles()), 1; got != want {
		t.Fatalf("known profiles = %d, want %d without duplicates", got, want)
	}
}

func TestNormalizeLowercasesAndDeduplicates(t *testing.T) {
	policy := PermissionPolicy{DefaultProfile: "  Default ", ElevatedProfiles: []string{"Trusted", "trusted", " ", "OPS"}}.Normalize()

	if policy.DefaultProfile != "default" {
		t.Errorf("default = %q, want default", policy.DefaultProfile)
	}
	if got, want := len(policy.ElevatedProfiles), 2; got != want {
		t.Fatalf("elevated profiles = %v, want %d entries", policy.ElevatedProfiles, want)
	}
}
