package taskdomain

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestNormalizeScheduleTimezoneValidation(t *testing.T) {
	for _, zone := range []string{"", "Local", "Mars/Olympus"} {
		t.Run(zone, func(t *testing.T) {
			_, err := NormalizeSchedule(ScheduleInput{
				RecurrenceType: RecurrenceNone,
				TimingType:     TimingUnscheduled,
				Timezone:       zone,
			})
			if !errors.Is(err, ErrInvalidTimezone) {
				t.Fatalf("NormalizeSchedule() error = %v, want %v", err, ErrInvalidTimezone)
			}
		})
	}

	for _, zone := range []string{"UTC", "Asia/Shanghai", "America/New_York", "Etc/GMT+5"} {
		t.Run(zone, func(t *testing.T) {
			if _, err := NormalizeSchedule(ScheduleInput{
				RecurrenceType: RecurrenceNone,
				TimingType:     TimingUnscheduled,
				Timezone:       zone,
			}); err != nil {
				t.Fatalf("valid timezone %q rejected: %v", zone, err)
			}
		})
	}
}

func TestTimezoneResolutionRejectsNonexistentLocalTime(t *testing.T) {
	resolution, err := ResolveLocalDateTime("2026-03-08", "02:30:00", "America/New_York")
	if !errors.Is(err, ErrNonexistentLocalTime) {
		t.Fatalf("ResolveLocalDateTime() error = %v, want %v", err, ErrNonexistentLocalTime)
	}
	if len(resolution.Candidates) != 0 {
		t.Fatalf("nonexistent time candidates = %#v, want none", resolution.Candidates)
	}
}

func TestTimezoneResolutionReturnsAmbiguousOffsetCandidates(t *testing.T) {
	resolution, err := ResolveLocalDateTime("2026-11-01", "01:30:00", "America/New_York")
	if !errors.Is(err, ErrAmbiguousLocalTime) {
		t.Fatalf("ResolveLocalDateTime() error = %v, want %v", err, ErrAmbiguousLocalTime)
	}

	want := []OffsetCandidate{
		{OffsetSeconds: -4 * 60 * 60, UTC: time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)},
		{OffsetSeconds: -5 * 60 * 60, UTC: time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)},
	}
	if !reflect.DeepEqual(resolution.Candidates, want) {
		t.Fatalf("ambiguous candidates = %#v, want %#v", resolution.Candidates, want)
	}
	if !resolution.UTC.IsZero() {
		t.Fatalf("ambiguous resolution silently selected %s", resolution.UTC)
	}
}

func TestTimezoneResolutionReturnsUnambiguousInstant(t *testing.T) {
	resolution, err := ResolveLocalDateTime("2026-07-21", "21:00:00", "Asia/Shanghai")
	if err != nil {
		t.Fatalf("ResolveLocalDateTime() unexpected error: %v", err)
	}
	want := time.Date(2026, 7, 21, 13, 0, 0, 0, time.UTC)
	if !resolution.UTC.Equal(want) {
		t.Fatalf("ResolveLocalDateTime() UTC = %s, want %s", resolution.UTC, want)
	}
	if len(resolution.Candidates) != 1 || resolution.Candidates[0].OffsetSeconds != 8*60*60 {
		t.Fatalf("ResolveLocalDateTime() candidates = %#v", resolution.Candidates)
	}
}

func TestTimezoneTimeBlockRequiresOffsetChoiceWhenAmbiguous(t *testing.T) {
	_, candidates, err := ResolveTimeBlockUTC(
		"2026-11-01",
		"01:30:00",
		"America/New_York",
		45,
		nil,
	)
	if !errors.Is(err, ErrAmbiguousLocalTime) || len(candidates) != 2 {
		t.Fatalf("ResolveTimeBlockUTC() error/candidates = %v/%#v", err, candidates)
	}

	offset := -5 * 60 * 60
	got, candidates, err := ResolveTimeBlockUTC(
		"2026-11-01",
		"01:30:00",
		"America/New_York",
		45,
		&offset,
	)
	if err != nil {
		t.Fatalf("ResolveTimeBlockUTC() unexpected error: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("ResolveTimeBlockUTC() candidates = %#v, want both choices", candidates)
	}
	wantStart := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)
	if !got.StartUTC.Equal(wantStart) || !got.EndUTC.Equal(wantStart.Add(45*time.Minute)) {
		t.Fatalf("ResolveTimeBlockUTC() range = [%s, %s)", got.StartUTC, got.EndUTC)
	}
}

func TestTimezoneTimeBlockConvertsToUTCInstants(t *testing.T) {
	got, _, err := ResolveTimeBlockUTC(
		"2026-07-21",
		"21:00:00",
		"Asia/Shanghai",
		30,
		nil,
	)
	if err != nil {
		t.Fatalf("ResolveTimeBlockUTC() unexpected error: %v", err)
	}
	wantStart := time.Date(2026, 7, 21, 13, 0, 0, 0, time.UTC)
	if !got.StartUTC.Equal(wantStart) || !got.EndUTC.Equal(wantStart.Add(30*time.Minute)) {
		t.Fatalf("ResolveTimeBlockUTC() range = [%s, %s)", got.StartUTC, got.EndUTC)
	}
}

func TestTimezoneAllDayRangeUsesExclusiveEnd(t *testing.T) {
	got, err := ResolveAllDayRangeUTC("2026-07-01", "2026-07-04", "Asia/Shanghai")
	if err != nil {
		t.Fatalf("ResolveAllDayRangeUTC() unexpected error: %v", err)
	}
	if got.StartDate != "2026-07-01" || got.ExclusiveEndDate != "2026-07-04" {
		t.Fatalf("ResolveAllDayRangeUTC() dates = [%s, %s)", got.StartDate, got.ExclusiveEndDate)
	}
	if got.EndUTC.Sub(got.StartUTC) != 72*time.Hour {
		t.Fatalf("ResolveAllDayRangeUTC() duration = %s, want 72h", got.EndUTC.Sub(got.StartUTC))
	}

	single, err := ResolveAllDayRangeUTC("2026-07-01", "", "Asia/Shanghai")
	if err != nil {
		t.Fatalf("ResolveAllDayRangeUTC() single day unexpected error: %v", err)
	}
	if single.ExclusiveEndDate != "2026-07-02" {
		t.Fatalf("single day exclusive end = %s, want 2026-07-02", single.ExclusiveEndDate)
	}
}

func TestTimezoneAllDayRangeFollowsDSTBoundaries(t *testing.T) {
	got, err := ResolveAllDayRangeUTC("2026-03-08", "2026-03-09", "America/New_York")
	if err != nil {
		t.Fatalf("ResolveAllDayRangeUTC() unexpected error: %v", err)
	}
	if got.EndUTC.Sub(got.StartUTC) != 23*time.Hour {
		t.Fatalf("DST all-day duration = %s, want 23h", got.EndUTC.Sub(got.StartUTC))
	}
}

func TestTimezoneAllDayRangeRejectsNonExclusiveOrInvalidEnd(t *testing.T) {
	for _, end := range []string{"2026-07-01", "2026-06-30", "not-a-date"} {
		if _, err := ResolveAllDayRangeUTC("2026-07-01", end, "UTC"); !errors.Is(err, ErrInvalidSchedule) {
			t.Fatalf("ResolveAllDayRangeUTC(end=%q) error = %v, want %v", end, err, ErrInvalidSchedule)
		}
	}
}
