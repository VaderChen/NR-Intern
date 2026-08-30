package domain_test

import (
	"AgenticService/src/domain"
	"errors"
	"testing"
	"time"
)

func TestScheduleRecurrenceNextInterval(t *testing.T) {
	recurrence := domain.ScheduleRecurrence{Frequency: domain.ScheduleFrequencyInterval, IntervalMinutes: 30}
	after := time.Date(2026, time.March, 2, 10, 15, 0, 0, time.Local)
	next, err := recurrence.Next(after)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if got, want := next.Local(), after.Add(30*time.Minute); !got.Equal(want) {
		t.Fatalf("next = %s, want %s", got, want)
	}
}

func TestScheduleRecurrenceNextDailyRollsToTomorrow(t *testing.T) {
	recurrence := domain.ScheduleRecurrence{Frequency: domain.ScheduleFrequencyDaily, TimeOfDay: "09:00"}
	after := time.Date(2026, time.March, 2, 9, 0, 0, 0, time.Local)
	next, err := recurrence.Next(after)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	want := time.Date(2026, time.March, 3, 9, 0, 0, 0, time.Local)
	if got := next.Local(); !got.Equal(want) {
		t.Fatalf("next = %s, want %s", got, want)
	}
}

func TestScheduleRecurrenceNextDailyKeepsSameDay(t *testing.T) {
	recurrence := domain.ScheduleRecurrence{Frequency: domain.ScheduleFrequencyDaily, TimeOfDay: "23:30"}
	after := time.Date(2026, time.March, 2, 8, 0, 0, 0, time.Local)
	next, err := recurrence.Next(after)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	want := time.Date(2026, time.March, 2, 23, 30, 0, 0, time.Local)
	if got := next.Local(); !got.Equal(want) {
		t.Fatalf("next = %s, want %s", got, want)
	}
}

func TestScheduleRecurrenceNextWeeklyPicksNextAllowedWeekday(t *testing.T) {
	// 2026-03-02 是星期一；只允許星期三與星期五。
	recurrence := domain.ScheduleRecurrence{
		Frequency: domain.ScheduleFrequencyWeekly,
		TimeOfDay: "07:45",
		Weekdays:  []int{5, 3},
	}
	after := time.Date(2026, time.March, 2, 12, 0, 0, 0, time.Local)
	next, err := recurrence.Next(after)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	want := time.Date(2026, time.March, 4, 7, 45, 0, 0, time.Local)
	if got := next.Local(); !got.Equal(want) {
		t.Fatalf("next = %s, want %s", got, want)
	}
}

// 錯過的時間點不補跑：從現在往後找下一次，而不是回填過去的每一次。
func TestScheduleRecurrenceNextSkipsMissedOccurrences(t *testing.T) {
	recurrence := domain.ScheduleRecurrence{Frequency: domain.ScheduleFrequencyDaily, TimeOfDay: "09:00"}
	after := time.Date(2026, time.March, 10, 11, 0, 0, 0, time.Local)
	next, err := recurrence.Next(after)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	want := time.Date(2026, time.March, 11, 9, 0, 0, 0, time.Local)
	if got := next.Local(); !got.Equal(want) {
		t.Fatalf("next = %s, want %s", got, want)
	}
}

func TestScheduleRecurrenceNormalizeRejectsInvalidInput(t *testing.T) {
	cases := map[string]domain.ScheduleRecurrence{
		"unknown frequency":  {Frequency: "cron"},
		"interval too small": {Frequency: domain.ScheduleFrequencyInterval, IntervalMinutes: 0},
		"interval too large": {Frequency: domain.ScheduleFrequencyInterval, IntervalMinutes: 10081},
		"daily without time": {Frequency: domain.ScheduleFrequencyDaily},
		"daily bad hour":     {Frequency: domain.ScheduleFrequencyDaily, TimeOfDay: "24:00"},
		"weekly no weekday":  {Frequency: domain.ScheduleFrequencyWeekly, TimeOfDay: "09:00"},
		"weekly bad weekday": {Frequency: domain.ScheduleFrequencyWeekly, TimeOfDay: "09:00", Weekdays: []int{7}},
	}
	for name, recurrence := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := recurrence.Normalize(); !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("expected invalid input, got %v", err)
			}
		})
	}
}

func TestScheduleRecurrenceNormalizeSortsAndDeduplicatesWeekdays(t *testing.T) {
	recurrence, err := domain.ScheduleRecurrence{
		Frequency: domain.ScheduleFrequencyWeekly,
		TimeOfDay: "7:5",
		Weekdays:  []int{5, 1, 5},
	}.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if recurrence.TimeOfDay != "07:05" {
		t.Fatalf("time_of_day = %q, want 07:05", recurrence.TimeOfDay)
	}
	if len(recurrence.Weekdays) != 2 || recurrence.Weekdays[0] != 1 || recurrence.Weekdays[1] != 5 {
		t.Fatalf("weekdays = %v, want [1 5]", recurrence.Weekdays)
	}
}
