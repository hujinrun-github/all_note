package service

import (
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
)

func TestExpandDailyRuleIncludesStartAndEnd(t *testing.T) {
	rule := &model.RecurrenceRule{
		StartDate: "2026-06-21",
		EndDate:   strPtr("2026-06-25"),
		Frequency: "daily",
		Interval:  1,
		Timezone:  "Asia/Shanghai",
		Enabled:   true,
	}
	dates := ExpandRuleOccurrences(rule, "2026-06-20", "2026-06-26")
	if len(dates) != 5 {
		t.Fatalf("expected 5 dates, got %d: %v", len(dates), dates)
	}
	if dates[0] != "2026-06-21" || dates[4] != "2026-06-25" {
		t.Errorf("unexpected range: %v", dates)
	}
}

func TestExpandEveryTwoDaysUsesStartDateAsAnchor(t *testing.T) {
	rule := &model.RecurrenceRule{
		StartDate: "2026-06-21",
		Frequency: "daily",
		Interval:  2,
		Timezone:  "Asia/Shanghai",
		Enabled:   true,
	}
	dates := ExpandRuleOccurrences(rule, "2026-06-20", "2026-06-30")
	expected := []string{"2026-06-21", "2026-06-23", "2026-06-25", "2026-06-27", "2026-06-29"}
	if len(dates) != len(expected) {
		t.Fatalf("expected %d dates, got %d: %v", len(expected), len(dates), dates)
	}
	for i, d := range expected {
		if dates[i] != d {
			t.Errorf("date[%d]: expected %s, got %s", i, d, dates[i])
		}
	}
}

func TestExpandWeeklyRuleUsesISOWeekdays(t *testing.T) {
	rule := &model.RecurrenceRule{
		StartDate: "2026-06-22", // Monday
		Frequency: "weekly",
		Interval:  1,
		Weekdays:  []int{1, 3, 5}, // Mon, Wed, Fri
		Timezone:  "Asia/Shanghai",
		Enabled:   true,
	}
	dates := ExpandRuleOccurrences(rule, "2026-06-22", "2026-06-28")
	expected := []string{"2026-06-22", "2026-06-24", "2026-06-26"}
	if len(dates) != len(expected) {
		t.Fatalf("expected %d dates, got %d: %v", len(expected), len(dates), dates)
	}
	for i, d := range expected {
		if dates[i] != d {
			t.Errorf("date[%d]: expected %s, got %s", i, d, dates[i])
		}
	}
}

func TestExpandWeeklyInterval2AnchorsAtStartDateISOWeek(t *testing.T) {
	// start_date=2026-06-20 (Saturday), weekdays=[1] (Monday), interval=2
	rule := &model.RecurrenceRule{
		StartDate: "2026-06-20",
		Frequency: "weekly",
		Interval:  2,
		Weekdays:  []int{1},
		Timezone:  "Asia/Shanghai",
		Enabled:   true,
	}
	dates := ExpandRuleOccurrences(rule, "2026-06-15", "2026-07-15")
	// 2026-06-20 is in ISO W25 (Mon=06-15). Week 0: Mon 06-15 (before start, skip).
	// Week 2 (W27): Mon 06-29. Week 4 (W29): Mon 07-13.
	if len(dates) < 2 {
		t.Fatalf("expected at least 2 dates, got %d: %v", len(dates), dates)
	}
	if dates[0] != "2026-06-29" {
		t.Errorf("first Mon after start in week 2 should be 2026-06-29, got %s", dates[0])
	}
	if dates[1] != "2026-07-13" {
		t.Errorf("second Mon should be 2026-07-13, got %s", dates[1])
	}
}

func TestExpandMonthlyRuleSkipsMissingMonthDay(t *testing.T) {
	rule := &model.RecurrenceRule{
		StartDate: "2026-01-01",
		Frequency: "monthly",
		Interval:  1,
		MonthDays: []int{31},
		Timezone:  "Asia/Shanghai",
		Enabled:   true,
	}
	dates := ExpandRuleOccurrences(rule, "2026-01-01", "2026-06-30")
	// Jan 31, Mar 31, May 31 — Feb/Apr/Jun have no 31st
	expected := []string{"2026-01-31", "2026-03-31", "2026-05-31"}
	if len(dates) != len(expected) {
		t.Fatalf("expected %d dates, got %d: %v", len(expected), len(dates), dates)
	}
	for i, d := range expected {
		if dates[i] != d {
			t.Errorf("date[%d]: expected %s, got %s", i, d, dates[i])
		}
	}
}

func TestExpandMonthlyLeapYearFeb29(t *testing.T) {
	rule := &model.RecurrenceRule{
		StartDate: "2028-01-01", // 2028 is leap year
		Frequency: "monthly",
		Interval:  1,
		MonthDays: []int{29},
		Timezone:  "Asia/Shanghai",
		Enabled:   true,
	}
	dates := ExpandRuleOccurrences(rule, "2028-02-01", "2028-02-29")
	if len(dates) != 1 || dates[0] != "2028-02-29" {
		t.Errorf("leap year Feb 29 should appear, got %v", dates)
	}
}

func TestExpandRuleRespectsEndDate(t *testing.T) {
	rule := &model.RecurrenceRule{
		StartDate: "2026-06-01",
		EndDate:   strPtr("2026-06-10"),
		Frequency: "daily",
		Interval:  1,
		Timezone:  "Asia/Shanghai",
		Enabled:   true,
	}
	dates := ExpandRuleOccurrences(rule, "2026-06-01", "2026-06-30")
	if len(dates) != 10 {
		t.Fatalf("expected 10 dates, got %d", len(dates))
	}
}

func TestExpandRuleReturnsEmptyWhenDisabled(t *testing.T) {
	rule := &model.RecurrenceRule{
		StartDate: "2026-06-01",
		Frequency: "daily",
		Interval:  1,
		Timezone:  "Asia/Shanghai",
		Enabled:   false,
	}
	dates := ExpandRuleOccurrences(rule, "2026-06-01", "2026-06-10")
	if len(dates) != 0 {
		t.Errorf("disabled rule should return empty, got %v", dates)
	}
}

func TestGenerateRecurrenceLabel(t *testing.T) {
	tests := []struct {
		rule     model.RecurrenceRule
		expected string
	}{
		{model.RecurrenceRule{Frequency: "daily", Interval: 1, EndDate: strPtr("2026-08-21")}, "每天"},
		{model.RecurrenceRule{Frequency: "daily", Interval: 1}, "每天（长期）"},
		{model.RecurrenceRule{Frequency: "daily", Interval: 2}, "每 2 天"},
		{model.RecurrenceRule{Frequency: "weekly", Interval: 1, Weekdays: []int{1, 3, 5}}, "每周一三五"},
		{model.RecurrenceRule{Frequency: "weekly", Interval: 1, Weekdays: []int{1, 2, 3, 4, 5}}, "每周一至五"},
		{model.RecurrenceRule{Frequency: "weekly", Interval: 2, Weekdays: []int{1}}, "隔周周一"},
		{model.RecurrenceRule{Frequency: "monthly", Interval: 1, MonthDays: []int{1, 15}}, "每月 1/15 号"},
		{model.RecurrenceRule{Frequency: "monthly", Interval: 2, MonthDays: []int{1}}, "每 2 个月 1 号"},
	}
	for _, tc := range tests {
		got := GenerateRecurrenceLabel(&tc.rule)
		if got != tc.expected {
			t.Errorf("GenerateRecurrenceLabel(%+v): expected %q, got %q", tc.rule, tc.expected, got)
		}
	}
}

func TestValidateRecurrenceConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  model.RecurrenceConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil config",
			config:  model.RecurrenceConfig{},
			wantErr: true,
		},
		{
			name: "valid daily",
			config: model.RecurrenceConfig{
				StartDate: "2026-06-21",
				Frequency: "daily",
				Interval:  1,
				Timezone:  "Asia/Shanghai",
			},
			wantErr: false,
		},
		{
			name: "missing start date",
			config: model.RecurrenceConfig{
				Frequency: "daily",
			},
			wantErr: true,
		},
		{
			name: "invalid frequency",
			config: model.RecurrenceConfig{
				StartDate: "2026-06-21",
				Frequency: "yearly",
			},
			wantErr: true,
		},
		{
			name: "weekly without weekdays",
			config: model.RecurrenceConfig{
				StartDate: "2026-06-21",
				Frequency: "weekly",
			},
			wantErr: true,
		},
		{
			name: "monthly without month days",
			config: model.RecurrenceConfig{
				StartDate: "2026-06-21",
				Frequency: "monthly",
			},
			wantErr: true,
		},
		{
			name: "invalid weekday",
			config: model.RecurrenceConfig{
				StartDate: "2026-06-21",
				Frequency: "weekly",
				Weekdays:  []int{1, 8},
			},
			wantErr: true,
		},
		{
			name: "invalid month day",
			config: model.RecurrenceConfig{
				StartDate: "2026-06-21",
				Frequency: "monthly",
				MonthDays: []int{0, 15},
			},
			wantErr: true,
		},
		{
			name: "end date before start date",
			config: model.RecurrenceConfig{
				StartDate: "2026-06-21",
				EndDate:   strPtr("2026-06-20"),
				Frequency: "daily",
			},
			wantErr: true,
		},
		{
			name: "zero interval defaults to 1",
			config: model.RecurrenceConfig{
				StartDate: "2026-06-21",
				Frequency: "daily",
				Interval:  0,
			},
			wantErr: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRecurrenceConfig(&tc.config)
			if tc.wantErr && err == nil {
				t.Errorf("expected error but got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tc.wantErr && tc.errMsg != "" && err != nil {
				// Just check that we got an error; specific message not asserted
			}
		})
	}
}

func TestExpandWithStatus(t *testing.T) {
	rule := &model.RecurrenceRule{
		TaskID:    "task-1",
		StartDate: "2026-06-21",
		EndDate:   strPtr("2026-06-25"),
		Frequency: "daily",
		Interval:  1,
		Timezone:  "Asia/Shanghai",
		Enabled:   true,
	}
	completedMap := map[string]string{
		"2026-06-23": "completed",
		"2026-06-24": "completed",
	}
	occurrences := ExpandWithStatus(rule, "2026-06-20", "2026-06-26", completedMap)
	if len(occurrences) != 5 {
		t.Fatalf("expected 5 occurrences, got %d", len(occurrences))
	}
	for _, occ := range occurrences {
		if occ.TaskID != "task-1" {
			t.Errorf("expected task-1, got %s", occ.TaskID)
		}
		switch occ.OccurrenceDate {
		case "2026-06-23", "2026-06-24":
			if occ.Status != "completed" {
				t.Errorf("%s: expected completed, got %s", occ.OccurrenceDate, occ.Status)
			}
		default:
			if occ.Status != "pending" {
				t.Errorf("%s: expected pending, got %s", occ.OccurrenceDate, occ.Status)
			}
		}
	}
}

func strPtr(s string) *string { return &s }
