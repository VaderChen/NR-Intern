package bootstrap

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	updateCheckInterval = 6 * time.Hour
	updateCheckTimeout  = 12 * time.Second
	updateAPIEndpoint   = "https://api.github.com/repos/VaderChen/NR-Intern/releases/latest"
)

var versionNumberPattern = regexp.MustCompile(`[0-9]+`)

type githubLatestRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
}

type latestRelease struct {
	TagName     string
	Name        string
	HTMLURL     string
	PublishedAt *time.Time
}

// startUpdateChecker 在後端啟動後背景檢查一次，之後每六小時檢查一次。
// 網路失敗只記錄在狀態，不影響後端啟動與對話執行。
func (r *Runtime) startUpdateChecker() {
	if r == nil || r.Application == nil || r.updateCheckCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.updateCheckContext = ctx
	r.updateCheckCancel = cancel
	go func() {
		r.checkForUpdates(ctx)
		ticker := time.NewTicker(updateCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.checkForUpdates(ctx)
			}
		}
	}()
}

// UpdateStatus 回傳最近一次更新檢查結果；尚未檢查時只回傳目前版本。
func (r *Runtime) UpdateStatus(ctx context.Context) (domain.UpdateStatus, error) {
	if err := ctx.Err(); err != nil {
		return domain.UpdateStatus{}, err
	}
	r.updateStatusMu.RLock()
	status := r.updateStatus
	r.updateStatusMu.RUnlock()
	if status.CurrentVersion == "" {
		status.CurrentVersion = Version
	}
	return status, nil
}

// CheckForUpdates 強制執行一次公開 Release 檢查。GitHub 網路錯誤會以
// check_error 回傳，保留 200 回應讓前端能顯示目前版本與上次狀態。
func (r *Runtime) CheckForUpdates(ctx context.Context) (domain.UpdateStatus, error) {
	if err := ctx.Err(); err != nil {
		return domain.UpdateStatus{}, err
	}
	return r.checkForUpdates(ctx), nil
}

func (r *Runtime) checkForUpdates(ctx context.Context) domain.UpdateStatus {
	r.updateCheckMu.Lock()
	defer r.updateCheckMu.Unlock()

	status := domain.UpdateStatus{CurrentVersion: Version, CheckedAt: time.Now().UTC()}
	release, err := fetchLatestRelease(ctx)
	if err != nil {
		status.CheckError = "無法取得 GitHub Release"
		r.logger.Debug("update check failed", "error", err)
		r.saveUpdateStatus(status)
		return status
	}
	status.LatestVersion = release.TagName
	status.ReleaseName = release.Name
	status.ReleaseURL = release.HTMLURL
	status.PublishedAt = release.PublishedAt
	status.Available = newerVersion(status.CurrentVersion, status.LatestVersion)
	r.saveUpdateStatus(status)
	if status.Available && r.Application != nil {
		r.Application.NotifyUpdateAvailable(status)
	}
	return status
}

func (r *Runtime) saveUpdateStatus(status domain.UpdateStatus) {
	r.updateStatusMu.Lock()
	r.updateStatus = status
	r.updateStatusMu.Unlock()
}

func fetchLatestRelease(ctx context.Context) (latestRelease, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, updateAPIEndpoint, nil)
	if err != nil {
		return latestRelease{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "NR-Intern-update-check")
	client := &http.Client{Timeout: updateCheckTimeout, CheckRedirect: func(request *http.Request, _ []*http.Request) error {
		if request.URL.Scheme != "https" || request.URL.Host != "api.github.com" {
			return fmt.Errorf("unexpected GitHub redirect")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return latestRelease{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return latestRelease{}, fmt.Errorf("GitHub Release returned HTTP %d", response.StatusCode)
	}
	var release githubLatestRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 256*1024)).Decode(&release); err != nil {
		return latestRelease{}, err
	}
	if release.Draft || release.Prerelease || strings.TrimSpace(release.TagName) == "" {
		return latestRelease{}, fmt.Errorf("GitHub latest Release is invalid")
	}
	if len([]rune(release.TagName)) > 80 || len([]rune(release.Name)) > 200 {
		return latestRelease{}, fmt.Errorf("GitHub Release metadata is too long")
	}
	if !versionNumberPattern.MatchString(release.TagName) {
		return latestRelease{}, fmt.Errorf("GitHub Release tag has no version number")
	}
	parsedURL, err := url.Parse(release.HTMLURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host != "github.com" || !strings.HasPrefix(parsedURL.Path, "/VaderChen/NR-Intern/releases/") {
		return latestRelease{}, fmt.Errorf("GitHub Release URL is invalid")
	}
	var publishedAt *time.Time
	if value := strings.TrimSpace(release.PublishedAt); value != "" {
		parsed, parseErr := time.Parse(time.RFC3339, value)
		if parseErr != nil {
			return latestRelease{}, fmt.Errorf("GitHub Release publish time is invalid")
		}
		parsed = parsed.UTC()
		publishedAt = &parsed
	}
	return latestRelease{TagName: strings.TrimSpace(release.TagName), Name: strings.TrimSpace(release.Name), HTMLURL: parsedURL.String(), PublishedAt: publishedAt}, nil
}

func newerVersion(current, latest string) bool {
	currentParts, latestParts := versionParts(current), versionParts(latest)
	if len(latestParts) == 0 || len(currentParts) == 0 {
		return false
	}
	length := len(currentParts)
	if len(latestParts) > length {
		length = len(latestParts)
	}
	for index := 0; index < length; index++ {
		currentPart, latestPart := "0", "0"
		if index < len(currentParts) {
			currentPart = currentParts[index]
		}
		if index < len(latestParts) {
			latestPart = latestParts[index]
		}
		if len(currentPart) != len(latestPart) {
			return len(latestPart) > len(currentPart)
		}
		if currentPart != latestPart {
			return latestPart > currentPart
		}
	}
	return false
}

func versionParts(value string) []string {
	values := versionNumberPattern.FindAllString(value, -1)
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimLeft(value, "0")
		if trimmed == "" {
			trimmed = "0"
		}
		result = append(result, trimmed)
	}
	return result
}
