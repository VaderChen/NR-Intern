package domain

import "time"

// UpdateStatus 是公開 Release 更新檢查的脫敏結果。
// 檢查器只讀取固定的官方 GitHub repository，不接受 Client 提供 URL，避免把
// 更新功能變成任意內網請求器。
type UpdateStatus struct {
	CurrentVersion string     `json:"current_version"`
	LatestVersion  string     `json:"latest_version,omitempty"`
	Available      bool       `json:"available"`
	ReleaseName    string     `json:"release_name,omitempty"`
	ReleaseURL     string     `json:"release_url,omitempty"`
	PublishedAt    *time.Time `json:"published_at,omitempty"`
	CheckedAt      time.Time  `json:"checked_at"`
	CheckError     string     `json:"check_error,omitempty"`
}
