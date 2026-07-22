package taskdomain

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeScheduleRecurrenceRules(t *testing.T) {
	tests := []struct {
		name     string
		input    ScheduleInput
		wantRule *RecurrenceRule
		wantErr  error
	}{
		{
			name: "none has no rule",
			input: ScheduleInput{
				RecurrenceType: RecurrenceNone,
				TimingType:     TimingUnscheduled,
				Timezone:       "UTC",
			},
		},
		{
			name: "none canonicalizes an empty json object",
			input: ScheduleInput{
				RecurrenceType: RecurrenceNone,
				TimingType:     TimingUnscheduled,
				Timezone:       "UTC",
				Rule:           json.RawMessage(`{ }`),
			},
		},
		{
			name: "daily requires only a positive interval",
			input: ScheduleInput{
				RecurrenceType: RecurrenceDaily,
				TimingType:     TimingDate,
				Timezone:       "Asia/Shanghai",
				StartsOn:       "2026-07-21",
				Rule:           json.RawMessage(`{"interval":2}`),
			},
			wantRule: &RecurrenceRule{Interval: 2},
		},
		{
			name: "weekly weekdays are sorted and deduplicated",
			input: ScheduleInput{
				RecurrenceType: RecurrenceWeekly,
				TimingType:     TimingDate,
				Timezone:       "Europe/Berlin",
				StartsOn:       "2026-07-21",
				Rule:           json.RawMessage(`{"interval":1,"weekdays":[5,1,5,3]}`),
			},
			wantRule: &RecurrenceRule{Interval: 1, Weekdays: []int{1, 3, 5}},
		},
		{
			name: "monthly days are sorted and deduplicated",
			input: ScheduleInput{
				RecurrenceType:  RecurrenceMonthly,
				TimingType:      TimingTimeBlock,
				Timezone:        "America/New_York",
				StartsOn:        "2026-07-21",
				LocalStartTime:  "09:30:00",
				DurationMinutes: 45,
				Rule:            json.RawMessage(`{"interval":3,"month_days":[31,1,15,1]}`),
			},
			wantRule: &RecurrenceRule{Interval: 3, MonthDays: []int{1, 15, 31}},
		},
		{
			name: "none rejects a rule",
			input: ScheduleInput{
				RecurrenceType: RecurrenceNone,
				TimingType:     TimingUnscheduled,
				Timezone:       "UTC",
				Rule:           json.RawMessage(`{"interval":1}`),
			},
			wantErr: ErrInvalidSchedule,
		},
		{
			name: "daily rejects zero interval",
			input: ScheduleInput{
				RecurrenceType: RecurrenceDaily,
				TimingType:     TimingDate,
				Timezone:       "UTC",
				StartsOn:       "2026-07-21",
				Rule:           json.RawMessage(`{"interval":0}`),
			},
			wantErr: ErrInvalidSchedule,
		},
		{
			name: "weekly requires weekdays",
			input: ScheduleInput{
				RecurrenceType: RecurrenceWeekly,
				TimingType:     TimingDate,
				Timezone:       "UTC",
				StartsOn:       "2026-07-21",
				Rule:           json.RawMessage(`{"interval":1,"weekdays":[]}`),
			},
			wantErr: ErrInvalidSchedule,
		},
		{
			name: "weekly rejects weekday outside zero through six",
			input: ScheduleInput{
				RecurrenceType: RecurrenceWeekly,
				TimingType:     TimingDate,
				Timezone:       "UTC",
				StartsOn:       "2026-07-21",
				Rule:           json.RawMessage(`{"interval":1,"weekdays":[7]}`),
			},
			wantErr: ErrInvalidSchedule,
		},
		{
			name: "monthly requires month days",
			input: ScheduleInput{
				RecurrenceType: RecurrenceMonthly,
				TimingType:     TimingDate,
				Timezone:       "UTC",
				StartsOn:       "2026-07-21",
				Rule:           json.RawMessage(`{"interval":1}`),
			},
			wantErr: ErrInvalidSchedule,
		},
		{
			name: "monthly rejects day outside one through thirty one",
			input: ScheduleInput{
				RecurrenceType: RecurrenceMonthly,
				TimingType:     TimingDate,
				Timezone:       "UTC",
				StartsOn:       "2026-07-21",
				Rule:           json.RawMessage(`{"interval":1,"month_days":[0,32]}`),
			},
			wantErr: ErrInvalidSchedule,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeSchedule(tt.input)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NormalizeSchedule() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeSchedule() unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got.Rule, tt.wantRule) {
				t.Fatalf("NormalizeSchedule() rule = %#v, want %#v", got.Rule, tt.wantRule)
			}
		})
	}
}

func TestNormalizeScheduleRejectsUnsupportedOrUnknownRuleFields(t *testing.T) {
	tests := []struct {
		name       string
		recurrence RecurrenceType
		rule       string
	}{
		{name: "unknown recurrence", recurrence: RecurrenceType("custom"), rule: `{"interval":1}`},
		{name: "unknown json field", recurrence: RecurrenceDaily, rule: `{"interval":1,"future_option":true}`},
		{name: "skip holidays", recurrence: RecurrenceDaily, rule: `{"interval":1,"skip_holidays":true}`},
		{name: "daily rejects weekdays", recurrence: RecurrenceDaily, rule: `{"interval":1,"weekdays":[1]}`},
		{name: "daily rejects explicitly empty weekdays", recurrence: RecurrenceDaily, rule: `{"interval":1,"weekdays":[]}`},
		{name: "weekly rejects month days", recurrence: RecurrenceWeekly, rule: `{"interval":1,"weekdays":[1],"month_days":[1]}`},
		{name: "weekly rejects explicitly null month days", recurrence: RecurrenceWeekly, rule: `{"interval":1,"weekdays":[1],"month_days":null}`},
		{name: "monthly rejects weekdays", recurrence: RecurrenceMonthly, rule: `{"interval":1,"weekdays":[1],"month_days":[1]}`},
		{name: "field names are case sensitive", recurrence: RecurrenceDaily, rule: `{"Interval":1}`},
		{name: "trailing json", recurrence: RecurrenceDaily, rule: `{"interval":1} {}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizeSchedule(ScheduleInput{
				RecurrenceType: tt.recurrence,
				TimingType:     TimingDate,
				Timezone:       "UTC",
				StartsOn:       "2026-07-21",
				Rule:           json.RawMessage(tt.rule),
			})
			if !errors.Is(err, ErrInvalidSchedule) {
				t.Fatalf("NormalizeSchedule() error = %v, want %v", err, ErrInvalidSchedule)
			}
		})
	}
}

func TestNormalizeScheduleTimingCombinations(t *testing.T) {
	valid := []ScheduleInput{
		{
			RecurrenceType: RecurrenceNone,
			TimingType:     TimingUnscheduled,
			Timezone:       "UTC",
		},
		{
			RecurrenceType: RecurrenceNone,
			TimingType:     TimingDate,
			Timezone:       "UTC",
			StartsOn:       "2026-07-21",
		},
		{
			RecurrenceType:  RecurrenceNone,
			TimingType:      TimingTimeBlock,
			Timezone:        "UTC",
			StartsOn:        "2026-07-21",
			LocalStartTime:  "14:05",
			DurationMinutes: 30,
		},
		{
			RecurrenceType: RecurrenceDaily,
			TimingType:     TimingDate,
			Timezone:       "UTC",
			StartsOn:       "2026-07-21",
			EndsOn:         "2026-07-31",
			Rule:           json.RawMessage(`{"interval":1}`),
		},
	}
	for index, input := range valid {
		if _, err := NormalizeSchedule(input); err != nil {
			t.Fatalf("valid case %d rejected: %v", index, err)
		}
	}

	invalid := []ScheduleInput{
		{RecurrenceType: RecurrenceDaily, TimingType: TimingUnscheduled, Timezone: "UTC", StartsOn: "2026-07-21", Rule: json.RawMessage(`{"interval":1}`)},
		{RecurrenceType: RecurrenceNone, TimingType: TimingUnscheduled, Timezone: "UTC", StartsOn: "2026-07-21"},
		{RecurrenceType: RecurrenceNone, TimingType: TimingDate, Timezone: "UTC"},
		{RecurrenceType: RecurrenceNone, TimingType: TimingDate, Timezone: "UTC", StartsOn: "2026-07-21", LocalStartTime: "00:00:00", DurationMinutes: 1439},
		{RecurrenceType: RecurrenceNone, TimingType: TimingTimeBlock, Timezone: "UTC", StartsOn: "2026-07-21", DurationMinutes: 30},
		{RecurrenceType: RecurrenceNone, TimingType: TimingTimeBlock, Timezone: "UTC", StartsOn: "2026-07-21", LocalStartTime: "14:00:00", DurationMinutes: 0},
		{RecurrenceType: RecurrenceNone, TimingType: TimingTimeBlock, Timezone: "UTC", StartsOn: "2026-07-21", LocalStartTime: "25:00:00", DurationMinutes: 30},
		{RecurrenceType: RecurrenceNone, TimingType: TimingDate, Timezone: "UTC", StartsOn: "2026-02-30"},
		{RecurrenceType: RecurrenceDaily, TimingType: TimingDate, Timezone: "UTC", StartsOn: "2026-07-21", EndsOn: "2026-07-20", Rule: json.RawMessage(`{"interval":1}`)},
		{RecurrenceType: RecurrenceNone, TimingType: TimingDate, Timezone: "UTC", StartsOn: "2026-07-21", EndsOn: "2026-07-22"},
		{RecurrenceType: RecurrenceType("yearly"), TimingType: TimingDate, Timezone: "UTC", StartsOn: "2026-07-21"},
		{RecurrenceType: RecurrenceNone, TimingType: TimingType("floating"), Timezone: "UTC"},
	}
	for index, input := range invalid {
		if _, err := NormalizeSchedule(input); !errors.Is(err, ErrInvalidSchedule) {
			t.Fatalf("invalid case %d error = %v, want %v", index, err, ErrInvalidSchedule)
		}
	}
}

func TestNormalizeScheduleCanonicalizesDateAndLocalTime(t *testing.T) {
	got, err := NormalizeSchedule(ScheduleInput{
		RecurrenceType:  RecurrenceNone,
		TimingType:      TimingTimeBlock,
		Timezone:        "Asia/Shanghai",
		StartsOn:        "2026-07-21",
		LocalStartTime:  "09:05",
		DurationMinutes: 45,
	})
	if err != nil {
		t.Fatalf("NormalizeSchedule() unexpected error: %v", err)
	}
	if got.StartsOn != "2026-07-21" || got.LocalStartTime != "09:05:00" {
		t.Fatalf("NormalizeSchedule() canonical values = (%q, %q)", got.StartsOn, got.LocalStartTime)
	}
}
