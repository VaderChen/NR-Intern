package domain

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Schedule 是 Workspace 層級的獨立排程實體，不隸屬於任何 Project 或 Session。
//
// 到點時由排程執行器建立一個全新的 Session，再送出 Prompt 開始 Run；每次執行
// 都是乾淨的上下文，不會把長期累積的對話拖進定期任務。沙箱由排程自己決定：
// SandboxRoots 為空時只使用該次 Session 的私有工作目錄。
type Schedule struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id"`
	Name        string `json:"name"`
	// Prompt 是到點後送進新 Session 的使用者訊息，等同於使用者親自交辦的任務。
	Prompt     string             `json:"prompt"`
	Enabled    bool               `json:"enabled"`
	Recurrence ScheduleRecurrence `json:"recurrence"`
	// SandboxRoots 與 Project 一樣是後端驗證後才寫入的保留欄位，呼叫端送進來的
	// 路徑必須通過絕對路徑、實際存在與非檔案系統根目錄的檢查。
	SandboxRoots      []string   `json:"sandbox_roots,omitempty"`
	ProviderID        string     `json:"provider_id,omitempty"`
	Model             string     `json:"model,omitempty"`
	PermissionProfile string     `json:"permission_profile,omitempty"`
	Position          int        `json:"position"`
	NextRunAt         *time.Time `json:"next_run_at,omitempty"`
	LastRunAt         *time.Time `json:"last_run_at,omitempty"`
	LastRunID         string     `json:"last_run_id,omitempty"`
	LastSessionID     string     `json:"last_session_id,omitempty"`
	LastStatus        string     `json:"last_status,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type ScheduleFrequency string

const (
	ScheduleFrequencyInterval ScheduleFrequency = "interval"
	ScheduleFrequencyDaily    ScheduleFrequency = "daily"
	ScheduleFrequencyWeekly   ScheduleFrequency = "weekly"
)

const (
	scheduleMinIntervalMinutes = 1
	scheduleMaxIntervalMinutes = 7 * 24 * 60
)

// ScheduleRecurrence 只提供固定間隔、每日與每週三種週期，不使用 cron 字串。
//
// 桌面使用者要的是「每天 09:00 幫我巡一次」，而不是一段容易寫錯又難以在 UI 上
// 回顯的表達式；結構化欄位也讓下一次執行時間可以直接驗證。
type ScheduleRecurrence struct {
	Frequency ScheduleFrequency `json:"frequency"`
	// IntervalMinutes 只在 frequency=interval 時使用。
	IntervalMinutes int `json:"interval_minutes,omitempty"`
	// TimeOfDay 是 24 小時制的 "HH:MM"，採後端所在時區的牆上時間。
	TimeOfDay string `json:"time_of_day,omitempty"`
	// Weekdays 只在 frequency=weekly 時使用，0 是星期日。
	Weekdays []int `json:"weekdays,omitempty"`
}

// Normalize 會回傳整理後的週期設定；欄位不合法時回報 ErrInvalidInput。
func (r ScheduleRecurrence) Normalize() (ScheduleRecurrence, error) {
	result := ScheduleRecurrence{Frequency: ScheduleFrequency(strings.TrimSpace(string(r.Frequency)))}
	switch result.Frequency {
	case ScheduleFrequencyInterval:
		if r.IntervalMinutes < scheduleMinIntervalMinutes || r.IntervalMinutes > scheduleMaxIntervalMinutes {
			return ScheduleRecurrence{}, fmt.Errorf("%w: interval_minutes must be between %d and %d", ErrInvalidInput, scheduleMinIntervalMinutes, scheduleMaxIntervalMinutes)
		}
		result.IntervalMinutes = r.IntervalMinutes
	case ScheduleFrequencyDaily:
		timeOfDay, err := normalizeScheduleTimeOfDay(r.TimeOfDay)
		if err != nil {
			return ScheduleRecurrence{}, err
		}
		result.TimeOfDay = timeOfDay
	case ScheduleFrequencyWeekly:
		timeOfDay, err := normalizeScheduleTimeOfDay(r.TimeOfDay)
		if err != nil {
			return ScheduleRecurrence{}, err
		}
		weekdays, err := normalizeScheduleWeekdays(r.Weekdays)
		if err != nil {
			return ScheduleRecurrence{}, err
		}
		result.TimeOfDay = timeOfDay
		result.Weekdays = weekdays
	default:
		return ScheduleRecurrence{}, fmt.Errorf("%w: schedule frequency must be interval, daily or weekly", ErrInvalidInput)
	}
	return result, nil
}

// Next 回傳嚴格晚於 after 的下一次執行時間（UTC）。
//
// 每日與每週以牆上時間計算，跨日採日期加法而非固定加 24 小時，夏令時間切換時
// 仍會停在使用者設定的時刻。錯過的時間點不補跑：後端關掉再打開時，Next 會直接
// 從現在往後找，避免一次湧出整批補償 Run。
func (r ScheduleRecurrence) Next(after time.Time) (time.Time, error) {
	recurrence, err := r.Normalize()
	if err != nil {
		return time.Time{}, err
	}
	local := after.Local()
	switch recurrence.Frequency {
	case ScheduleFrequencyInterval:
		return local.Add(time.Duration(recurrence.IntervalMinutes) * time.Minute).UTC(), nil
	case ScheduleFrequencyDaily:
		hour, minute := splitScheduleTimeOfDay(recurrence.TimeOfDay)
		for offset := 0; offset <= 1; offset++ {
			candidate := scheduleWallClock(local, offset, hour, minute)
			if candidate.After(local) {
				return candidate.UTC(), nil
			}
		}
		return time.Time{}, fmt.Errorf("%w: cannot resolve next daily occurrence", ErrInvalidInput)
	case ScheduleFrequencyWeekly:
		hour, minute := splitScheduleTimeOfDay(recurrence.TimeOfDay)
		allowed := map[int]struct{}{}
		for _, weekday := range recurrence.Weekdays {
			allowed[weekday] = struct{}{}
		}
		for offset := 0; offset <= 7; offset++ {
			candidate := scheduleWallClock(local, offset, hour, minute)
			if _, ok := allowed[int(candidate.Weekday())]; !ok {
				continue
			}
			if candidate.After(local) {
				return candidate.UTC(), nil
			}
		}
		return time.Time{}, fmt.Errorf("%w: cannot resolve next weekly occurrence", ErrInvalidInput)
	}
	return time.Time{}, fmt.Errorf("%w: schedule frequency must be interval, daily or weekly", ErrInvalidInput)
}

type CreateScheduleInput struct {
	WorkspaceID       string             `json:"workspace_id"`
	AgentID           string             `json:"agent_id,omitempty"`
	Name              string             `json:"name"`
	Prompt            string             `json:"prompt"`
	Enabled           *bool              `json:"enabled,omitempty"`
	Recurrence        ScheduleRecurrence `json:"recurrence"`
	SandboxRoots      []string           `json:"sandbox_roots,omitempty"`
	ProviderID        string             `json:"provider_id,omitempty"`
	Model             string             `json:"model,omitempty"`
	PermissionProfile string             `json:"permission_profile,omitempty"`
}

type UpdateScheduleInput struct {
	Name              *string             `json:"name,omitempty"`
	Prompt            *string             `json:"prompt,omitempty"`
	Enabled           *bool               `json:"enabled,omitempty"`
	Recurrence        *ScheduleRecurrence `json:"recurrence,omitempty"`
	SandboxRoots      *[]string           `json:"sandbox_roots,omitempty"`
	ProviderID        *string             `json:"provider_id,omitempty"`
	Model             *string             `json:"model,omitempty"`
	PermissionProfile *string             `json:"permission_profile,omitempty"`
	Position          *int                `json:"position,omitempty"`
}

// ScheduleRunState 是排程執行器在觸發後寫回的結果快照。
// 執行器只更新這幾個欄位，不會覆寫使用者正在編輯的設定。
type ScheduleRunState struct {
	LastRunAt     time.Time
	NextRunAt     *time.Time
	LastRunID     string
	LastSessionID string
	LastStatus    string
	LastError     string
}

const (
	ScheduleStatusTriggered = "triggered"
	ScheduleStatusFailed    = "failed"
)

func normalizeScheduleTimeOfDay(value string) (string, error) {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return "", fmt.Errorf("%w: time_of_day must use the HH:MM format", ErrInvalidInput)
	}
	hour, hourErr := strconv.Atoi(strings.TrimSpace(parts[0]))
	minute, minuteErr := strconv.Atoi(strings.TrimSpace(parts[1]))
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return "", fmt.Errorf("%w: time_of_day must be between 00:00 and 23:59", ErrInvalidInput)
	}
	return fmt.Sprintf("%02d:%02d", hour, minute), nil
}

func normalizeScheduleWeekdays(values []int) ([]int, error) {
	seen := map[int]struct{}{}
	result := make([]int, 0, len(values))
	for _, value := range values {
		if value < 0 || value > 6 {
			return nil, fmt.Errorf("%w: weekdays must be between 0 (Sunday) and 6", ErrInvalidInput)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%w: weekly schedules require at least one weekday", ErrInvalidInput)
	}
	sort.Ints(result)
	return result, nil
}

func splitScheduleTimeOfDay(value string) (int, int) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, 0
	}
	hour, _ := strconv.Atoi(parts[0])
	minute, _ := strconv.Atoi(parts[1])
	return hour, minute
}

func scheduleWallClock(reference time.Time, dayOffset, hour, minute int) time.Time {
	day := reference.AddDate(0, 0, dayOffset)
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, reference.Location())
}
