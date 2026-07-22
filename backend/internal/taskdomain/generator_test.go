package taskdomain

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestCalculateOccurrenceKeysRecurrenceRules(t *testing.T) {
	tests := []struct {
		name      string
		schedule  ScheduleInput
		effective ScheduleEffectiveRange
		window    OccurrenceWindow
		want      []string
	}{
		{
			name: "daily interval is anchored at starts on",
			schedule: ScheduleInput{
				RecurrenceType: RecurrenceDaily,
				TimingType:     TimingDate,
				Timezone:       "Asia/Shanghai",
				StartsOn:       "2026-07-21",
				Rule:           json.RawMessage(`{"interval":2}`),
			},
			effective: ScheduleEffectiveRange{From: "2026-07-21"},
			window:    OccurrenceWindow{From: "2026-07-20", To: "2026-07-29"},
			want:      []string{"2026-07-21", "2026-07-23", "2026-07-25", "2026-07-27"},
		},
		{
			name: "weekly supports multiple weekdays and interval weeks",
			schedule: ScheduleInput{
				RecurrenceType: RecurrenceWeekly,
				TimingType:     TimingDate,
				Timezone:       "UTC",
				StartsOn:       "2026-06-20",
				Rule:           json.RawMessage(`{"interval":2,"weekdays":[5,1,3]}`),
			},
			effective: ScheduleEffectiveRange{From: "2026-06-20"},
			window:    OccurrenceWindow{From: "2026-06-15", To: "2026-07-15"},
			want: []string{
				"2026-06-29", "2026-07-01", "2026-07-03",
				"2026-07-13",
			},
		},
		{
			name: "monthly skips month days that do not exist",
			schedule: ScheduleInput{
				RecurrenceType: RecurrenceMonthly,
				TimingType:     TimingDate,
				Timezone:       "UTC",
				StartsOn:       "2026-01-30",
				Rule:           json.RawMessage(`{"interval":1,"month_days":[31,1,29]}`),
			},
			effective: ScheduleEffectiveRange{From: "2026-01-30"},
			window:    OccurrenceWindow{From: "2026-01-01", To: "2026-04-02"},
			want: []string{
				"2026-01-31",
				"2026-02-01",
				"2026-03-01", "2026-03-29", "2026-03-31",
				"2026-04-01",
			},
		},
		{
			name: "monthly interval is anchored at starts on month",
			schedule: ScheduleInput{
				RecurrenceType: RecurrenceMonthly,
				TimingType:     TimingDate,
				Timezone:       "UTC",
				StartsOn:       "2026-01-01",
				Rule:           json.RawMessage(`{"interval":2,"month_days":[1]}`),
			},
			effective: ScheduleEffectiveRange{From: "2026-01-01"},
			window:    OccurrenceWindow{From: "2026-01-01", To: "2026-06-01"},
			want:      []string{"2026-01-01", "2026-03-01", "2026-05-01"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schedule := mustNormalizeGeneratorSchedule(t, tt.schedule)
			got, err := CalculateOccurrenceKeys(schedule, tt.effective, tt.window)
			if err != nil {
				t.Fatalf("CalculateOccurrenceKeys() unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("CalculateOccurrenceKeys() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCalculateOccurrenceKeysIntersectsAllDateBoundaries(t *testing.T) {
	schedule := mustNormalizeGeneratorSchedule(t, ScheduleInput{
		RecurrenceType: RecurrenceDaily,
		TimingType:     TimingDate,
		Timezone:       "UTC",
		StartsOn:       "2026-07-20",
		EndsOn:         "2026-07-27",
		Rule:           json.RawMessage(`{"interval":1}`),
	})

	got, err := CalculateOccurrenceKeys(
		schedule,
		ScheduleEffectiveRange{From: "2026-07-22", To: "2026-07-27"},
		OccurrenceWindow{From: "2026-07-21", To: "2026-07-29"},
	)
	if err != nil {
		t.Fatalf("CalculateOccurrenceKeys() unexpected error: %v", err)
	}
	want := []string{"2026-07-22", "2026-07-23", "2026-07-24", "2026-07-25", "2026-07-26"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CalculateOccurrenceKeys() = %v, want %v", got, want)
	}

	got, err = CalculateOccurrenceKeys(
		schedule,
		ScheduleEffectiveRange{From: "2026-07-27"},
		OccurrenceWindow{From: "2026-07-27", To: "2026-07-29"},
	)
	if err != nil {
		t.Fatalf("CalculateOccurrenceKeys() ends_on check returned error: %v", err)
	}
	if want := []string{"2026-07-27"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("CalculateOccurrenceKeys() at inclusive ends_on = %v, want %v", got, want)
	}
}

func TestCalculateOccurrenceKeysUsesLocalCalendarAcrossDST(t *testing.T) {
	schedule := mustNormalizeGeneratorSchedule(t, ScheduleInput{
		RecurrenceType:  RecurrenceDaily,
		TimingType:      TimingTimeBlock,
		Timezone:        "America/New_York",
		StartsOn:        "2026-03-06",
		LocalStartTime:  "01:30:00",
		DurationMinutes: 30,
		Rule:            json.RawMessage(`{"interval":1}`),
	})

	got, err := CalculateOccurrenceKeys(
		schedule,
		ScheduleEffectiveRange{From: "2026-03-06"},
		OccurrenceWindow{From: "2026-03-07", To: "2026-03-11"},
	)
	if err != nil {
		t.Fatalf("CalculateOccurrenceKeys() unexpected error: %v", err)
	}
	want := []string{"2026-03-07", "2026-03-08", "2026-03-09", "2026-03-10"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DST calendar keys = %v, want %v", got, want)
	}
}

func TestCalculateOccurrenceKeysIsStableAndOrdered(t *testing.T) {
	schedule := mustNormalizeGeneratorSchedule(t, ScheduleInput{
		RecurrenceType: RecurrenceWeekly,
		TimingType:     TimingDate,
		Timezone:       "Europe/Berlin",
		StartsOn:       "2026-07-01",
		Rule:           json.RawMessage(`{"interval":1,"weekdays":[5,1,3]}`),
	})
	effective := ScheduleEffectiveRange{From: "2026-07-01"}
	window := OccurrenceWindow{From: "2026-07-01", To: "2026-07-20"}

	first, err := CalculateOccurrenceKeys(schedule, effective, window)
	if err != nil {
		t.Fatalf("first CalculateOccurrenceKeys() error: %v", err)
	}
	second, err := CalculateOccurrenceKeys(schedule, effective, window)
	if err != nil {
		t.Fatalf("second CalculateOccurrenceKeys() error: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same input produced different keys: %v and %v", first, second)
	}
	want := []string{"2026-07-01", "2026-07-03", "2026-07-06", "2026-07-08", "2026-07-10", "2026-07-13", "2026-07-15", "2026-07-17"}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("stable ordered keys = %v, want %v", first, want)
	}
}

func TestCalculateOccurrenceKeysScheduleVersionSplitIsLossless(t *testing.T) {
	schedule := mustNormalizeGeneratorSchedule(t, ScheduleInput{
		RecurrenceType: RecurrenceDaily,
		TimingType:     TimingDate,
		Timezone:       "UTC",
		StartsOn:       "2026-07-01",
		Rule:           json.RawMessage(`{"interval":1}`),
	})
	window := OccurrenceWindow{From: "2026-07-01", To: "2026-07-11"}

	unsplit, err := CalculateOccurrenceKeys(schedule, ScheduleEffectiveRange{From: "2026-07-01"}, window)
	if err != nil {
		t.Fatalf("unsplit CalculateOccurrenceKeys() error: %v", err)
	}
	oldVersion, err := CalculateOccurrenceKeys(schedule, ScheduleEffectiveRange{From: "2026-07-01", To: "2026-07-06"}, window)
	if err != nil {
		t.Fatalf("old version CalculateOccurrenceKeys() error: %v", err)
	}
	newVersion, err := CalculateOccurrenceKeys(schedule, ScheduleEffectiveRange{From: "2026-07-06"}, window)
	if err != nil {
		t.Fatalf("new version CalculateOccurrenceKeys() error: %v", err)
	}
	combined := append(append([]string(nil), oldVersion...), newVersion...)
	if !reflect.DeepEqual(combined, unsplit) {
		t.Fatalf("split keys = %v, unsplit keys = %v", combined, unsplit)
	}
}

func TestCalculateOccurrenceKeysNonRecurringScheduleUsesOnceKey(t *testing.T) {
	schedule := mustNormalizeGeneratorSchedule(t, ScheduleInput{
		RecurrenceType: RecurrenceNone,
		TimingType:     TimingDate,
		Timezone:       "UTC",
		StartsOn:       "2026-07-21",
	})

	got, err := CalculateOccurrenceKeys(
		schedule,
		ScheduleEffectiveRange{From: "2026-07-21"},
		OccurrenceWindow{From: "2026-07-21", To: "2026-07-22"},
	)
	if err != nil {
		t.Fatalf("CalculateOccurrenceKeys() unexpected error: %v", err)
	}
	if want := []string{"once"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("non-recurring keys = %v, want %v", got, want)
	}
}

func TestCalculateOccurrenceKeysRejectsInvalidRangesAndSchedule(t *testing.T) {
	daily := mustNormalizeGeneratorSchedule(t, ScheduleInput{
		RecurrenceType: RecurrenceDaily,
		TimingType:     TimingDate,
		Timezone:       "UTC",
		StartsOn:       "2026-07-21",
		Rule:           json.RawMessage(`{"interval":1}`),
	})

	tests := []struct {
		name      string
		schedule  Schedule
		effective ScheduleEffectiveRange
		window    OccurrenceWindow
	}{
		{name: "window from is required", schedule: daily, effective: ScheduleEffectiveRange{From: "2026-07-21"}, window: OccurrenceWindow{To: "2026-08-01"}},
		{name: "window to is required", schedule: daily, effective: ScheduleEffectiveRange{From: "2026-07-21"}, window: OccurrenceWindow{From: "2026-07-21"}},
		{name: "window is half open", schedule: daily, effective: ScheduleEffectiveRange{From: "2026-07-21"}, window: OccurrenceWindow{From: "2026-07-21", To: "2026-07-21"}},
		{name: "effective from is required", schedule: daily, window: OccurrenceWindow{From: "2026-07-21", To: "2026-08-01"}},
		{name: "effective range is half open", schedule: daily, effective: ScheduleEffectiveRange{From: "2026-07-22", To: "2026-07-21"}, window: OccurrenceWindow{From: "2026-07-21", To: "2026-08-01"}},
		{name: "schedule must be normalized", schedule: Schedule{RecurrenceType: RecurrenceDaily, TimingType: TimingDate, Timezone: "UTC", StartsOn: "2026-07-21"}, effective: ScheduleEffectiveRange{From: "2026-07-21"}, window: OccurrenceWindow{From: "2026-07-21", To: "2026-08-01"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CalculateOccurrenceKeys(tt.schedule, tt.effective, tt.window)
			if !errors.Is(err, ErrInvalidSchedule) {
				t.Fatalf("CalculateOccurrenceKeys() error = %v, want %v", err, ErrInvalidSchedule)
			}
		})
	}
}

func mustNormalizeGeneratorSchedule(t *testing.T, input ScheduleInput) Schedule {
	t.Helper()
	schedule, err := NormalizeSchedule(input)
	if err != nil {
		t.Fatalf("NormalizeSchedule() unexpected error: %v", err)
	}
	return schedule
}
