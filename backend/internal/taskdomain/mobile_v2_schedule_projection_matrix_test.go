package taskdomain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type mobileV2ScheduleProjectionFixture struct {
	ProjectionCases []mobileV2ProjectionCase `json:"projection_cases"`
	LocalTimeCases  []mobileV2LocalTimeCase  `json:"local_time_cases"`
}

type mobileV2ProjectionCase struct {
	Name                   string                  `json:"name"`
	Generation             string                  `json:"generation"`
	ProjectionTimeZone     string                  `json:"projection_time_zone"`
	Schedule               mobileV2FixtureSchedule `json:"schedule"`
	Window                 mobileV2FixtureWindow   `json:"window"`
	ExpectedOccurrenceKeys []string                `json:"expected_keys"`
}

type mobileV2FixtureSchedule struct {
	RecurrenceType RecurrenceType `json:"recurrence_type"`
	TimingType     TimingType     `json:"timing_type"`
	TimeZone       string         `json:"time_zone"`
	StartsOn       string         `json:"starts_on"`
	EndsOn         string         `json:"ends_on"`
	Interval       int            `json:"interval"`
	Weekdays       []int          `json:"weekdays"`
	MonthDays      []int          `json:"month_days"`
	EffectiveFrom  string         `json:"effective_from"`
	EffectiveTo    string         `json:"effective_to"`
}

type mobileV2FixtureWindow struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type mobileV2LocalTimeCase struct {
	Name                  string `json:"name"`
	Date                  string `json:"date"`
	LocalTime             string `json:"local_time"`
	TimeZone              string `json:"time_zone"`
	DurationMinutes       int    `json:"duration_minutes"`
	SelectedOffsetSeconds *int   `json:"selected_offset_seconds"`
	ExpectedError         string `json:"expected_error"`
	ExpectedOffsets       []int  `json:"expected_offsets"`
	ExpectedStart         string `json:"expected_start"`
	ExpectedEnd           string `json:"expected_end"`
}

func TestMTDV2DomainScheduleProjectionMatchesSharedMobileFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "mobile-v2", "schedule-projection-matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture mobileV2ScheduleProjectionFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}

	for _, test := range fixture.ProjectionCases {
		t.Run("projection/"+test.Name, func(t *testing.T) {
			if test.ProjectionTimeZone != test.Schedule.TimeZone {
				t.Fatalf("projection timezone %q differs from frozen schedule timezone %q", test.ProjectionTimeZone, test.Schedule.TimeZone)
			}
			rule, err := json.Marshal(RecurrenceRule{
				Interval:  test.Schedule.Interval,
				Weekdays:  test.Schedule.Weekdays,
				MonthDays: test.Schedule.MonthDays,
			})
			if err != nil {
				t.Fatal(err)
			}
			schedule, err := NormalizeSchedule(ScheduleInput{
				RecurrenceType: test.Schedule.RecurrenceType,
				TimingType:     test.Schedule.TimingType,
				Timezone:       test.Schedule.TimeZone,
				StartsOn:       test.Schedule.StartsOn,
				EndsOn:         test.Schedule.EndsOn,
				Rule:           rule,
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := CalculateOccurrenceKeys(
				schedule,
				ScheduleEffectiveRange{
					From: test.Schedule.EffectiveFrom,
					To:   test.Schedule.EffectiveTo,
				},
				OccurrenceWindow{From: test.Window.From, To: test.Window.To},
			)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.ExpectedOccurrenceKeys) {
				t.Fatalf("keys=%v, want %v (generation %s)", got, test.ExpectedOccurrenceKeys, test.Generation)
			}
		})
	}

	for _, test := range fixture.LocalTimeCases {
		t.Run("local-time/"+test.Name, func(t *testing.T) {
			got, candidates, err := ResolveTimeBlockUTC(
				test.Date,
				test.LocalTime,
				test.TimeZone,
				test.DurationMinutes,
				test.SelectedOffsetSeconds,
			)
			offsets := make([]int, len(candidates))
			for index, candidate := range candidates {
				offsets[index] = candidate.OffsetSeconds
			}
			if !reflect.DeepEqual(offsets, test.ExpectedOffsets) {
				t.Fatalf("offsets=%v, want %v", offsets, test.ExpectedOffsets)
			}
			if test.ExpectedError != "" {
				if string(ErrorCodeOf(err)) != test.ExpectedError {
					t.Fatalf("error code=%q err=%v, want %q", ErrorCodeOf(err), err, test.ExpectedError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			const millisecondsUTC = "2006-01-02T15:04:05.000Z"
			if got.StartUTC.Format(millisecondsUTC) != test.ExpectedStart ||
				got.EndUTC.Format(millisecondsUTC) != test.ExpectedEnd {
				t.Fatalf(
					"range=(%s,%s), want (%s,%s)",
					got.StartUTC.Format(time.RFC3339Nano),
					got.EndUTC.Format(time.RFC3339Nano),
					test.ExpectedStart,
					test.ExpectedEnd,
				)
			}
		})
	}
}
