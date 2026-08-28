package domain

import (
	"fmt"
	"sort"
	"strings"
)

const DefaultPermissionProfile = "default"

// PermissionPolicy 決定 Session 可以取得哪個 permission profile，以及哪些 profile 屬於 elevated。
//
// 這個型別存在的理由是：elevated 工具的閘門不能由呼叫端自行決定。
// 在 permission profile 可由 API request body 指定時，任何持有 token 的 Client
// 都能直接宣告自己是 trusted，第二道閘門形同不存在。
// 因此預設 AllowClientChoice=false：profile 由後端設定決定，
// request 若仍嘗試指定則明確拒絕，而不是靜默忽略。
type PermissionPolicy struct {
	// DefaultProfile 是未指定時使用的 profile。它可以被列為 elevated：
	// 在只有單一共用 token、沒有呼叫端身分的部署裡，「整個後端都受信任」是誠實的描述，
	// 但這必須是後端設定寫出來的決定，而不是呼叫端自行宣告的結果。
	DefaultProfile string `json:"default_profile"`
	// ElevatedProfiles 是後端承認為 elevated 的 profile 名稱；空集合代表沒有任何 profile 可取得 elevated 工具。
	ElevatedProfiles []string `json:"elevated_profiles,omitempty"`
	// AllowClientChoice 決定 API request 是否可以指定 permission_profile。
	AllowClientChoice bool `json:"allow_client_choice"`
}

func (p PermissionPolicy) Normalize() PermissionPolicy {
	result := PermissionPolicy{
		DefaultProfile:    normalizeProfile(p.DefaultProfile),
		AllowClientChoice: p.AllowClientChoice,
	}
	if result.DefaultProfile == "" {
		result.DefaultProfile = DefaultPermissionProfile
	}
	seen := map[string]struct{}{}
	for _, value := range p.ElevatedProfiles {
		value = normalizeProfile(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result.ElevatedProfiles = append(result.ElevatedProfiles, value)
	}
	sort.Strings(result.ElevatedProfiles)
	return result
}

// Resolve 將呼叫端要求的 profile 轉成實際套用的 profile。
// 未開放呼叫端選擇時，任何明確指定都會被拒絕，讓「要求提權卻沒有生效」不會被靜默吞掉。
func (p PermissionPolicy) Resolve(requested string) (string, error) {
	policy := p.Normalize()
	requested = normalizeProfile(requested)
	if requested == "" {
		return policy.DefaultProfile, nil
	}
	if !policy.AllowClientChoice {
		if requested == policy.DefaultProfile {
			return policy.DefaultProfile, nil
		}
		return "", fmt.Errorf("%w: permission_profile cannot be set by the client; the backend assigns %q", ErrInvalidInput, policy.DefaultProfile)
	}
	if requested == policy.DefaultProfile || containsProfile(policy.ElevatedProfiles, requested) {
		return requested, nil
	}
	return "", fmt.Errorf("%w: unknown permission profile %q", ErrInvalidInput, requested)
}

func (p PermissionPolicy) IsElevated(profile string) bool {
	return containsProfile(p.Normalize().ElevatedProfiles, normalizeProfile(profile))
}

func (p PermissionPolicy) KnownProfiles() []string {
	policy := p.Normalize()
	result := []string{policy.DefaultProfile}
	for _, value := range policy.ElevatedProfiles {
		if value != policy.DefaultProfile {
			result = append(result, value)
		}
	}
	return result
}

func normalizeProfile(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func containsProfile(values []string, wanted string) bool {
	if wanted == "" {
		return false
	}
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
