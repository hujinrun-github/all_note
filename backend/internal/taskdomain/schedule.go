package taskdomain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

const (
	ErrorCodeInvalidSchedule      ErrorCode = "invalid_schedule"
	ErrorCodeInvalidTimezone      ErrorCode = "invalid_timezone"
	ErrorCodeNonexistentLocalTime ErrorCode = "nonexistent_local_time"
	ErrorCodeAmbiguousLocalTime   ErrorCode = "ambiguous_local_time"
)

var (
	ErrInvalidSchedule      = &domainError{code: ErrorCodeInvalidSchedule}
	ErrInvalidTimezone      = &domainError{code: ErrorCodeInvalidTimezone}
	ErrNonexistentLocalTime = &domainError{code: ErrorCodeNonexistentLocalTime}
	ErrAmbiguousLocalTime   = &domainError{code: ErrorCodeAmbiguousLocalTime}
)

type RecurrenceType string

const (
	RecurrenceNone    RecurrenceType = "none"
	RecurrenceDaily   RecurrenceType = "daily"
	RecurrenceWeekly  RecurrenceType = "weekly"
	RecurrenceMonthly RecurrenceType = "monthly"
)

type TimingType string

const (
	TimingUnscheduled TimingType = "unscheduled"
	TimingDate        TimingType = "date"
	TimingTimeBlock   TimingType = "time_block"
)

// ScheduleInput is the user-facing representation accepted by the pure
// schedule normalizer. Dates and local times intentionally contain no implicit
// server timezone.
type ScheduleInput struct {
	RecurrenceType  RecurrenceType
	TimingType      TimingType
	Timezone        string
	StartsOn        string
	EndsOn          string
	Rule            json.RawMessage
	LocalStartTime  string
	DurationMinutes int
}

// RecurrenceRule is the only recurrence JSON shape supported by v2. Fields
// that do not apply to the selected recurrence type are rejected.
type RecurrenceRule struct {
	Interval  int   `json:"interval"`
	Weekdays  []int `json:"weekdays,omitempty"`
	MonthDays []int `json:"month_days,omitempty"`
}

// Schedule is a validated, canonical schedule value. Date strings use
// YYYY-MM-DD and LocalStartTime uses HH:MM:SS.
type Schedule struct {
	RecurrenceType  RecurrenceType
	TimingType      TimingType
	Timezone        string
	StartsOn        string
	EndsOn          string
	Rule            *RecurrenceRule
	LocalStartTime  string
	DurationMinutes int
}

type OffsetCandidate struct {
	OffsetSeconds int
	UTC           time.Time
}

type LocalDateTimeResolution struct {
	UTC        time.Time
	Candidates []OffsetCandidate
}

type UTCInstantRange struct {
	StartUTC time.Time
	EndUTC   time.Time
}

type AllDayUTCInstantRange struct {
	StartDate        string
	ExclusiveEndDate string
	StartUTC         time.Time
	EndUTC           time.Time
}

func NormalizeSchedule(input ScheduleInput) (Schedule, error) {
	zone, err := loadIANALocation(input.Timezone)
	if err != nil {
		return Schedule{}, err
	}
	_ = zone

	normalized := Schedule{
		RecurrenceType:  input.RecurrenceType,
		TimingType:      input.TimingType,
		Timezone:        input.Timezone,
		DurationMinutes: input.DurationMinutes,
	}

	if input.StartsOn != "" {
		date, parseErr := parseLocalDate(input.StartsOn)
		if parseErr != nil {
			return Schedule{}, invalidSchedule("invalid starts_on")
		}
		normalized.StartsOn = formatLocalDate(date)
	}
	if input.EndsOn != "" {
		date, parseErr := parseLocalDate(input.EndsOn)
		if parseErr != nil {
			return Schedule{}, invalidSchedule("invalid ends_on")
		}
		normalized.EndsOn = formatLocalDate(date)
	}

	if err := normalizeTiming(input, &normalized); err != nil {
		return Schedule{}, err
	}
	if err := normalizeRecurrence(input, &normalized); err != nil {
		return Schedule{}, err
	}

	if normalized.EndsOn != "" {
		start, _ := parseLocalDate(normalized.StartsOn)
		end, _ := parseLocalDate(normalized.EndsOn)
		if end.Before(start) {
			return Schedule{}, invalidSchedule("ends_on precedes starts_on")
		}
	}

	return normalized, nil
}

func normalizeTiming(input ScheduleInput, normalized *Schedule) error {
	switch input.TimingType {
	case TimingUnscheduled:
		if input.RecurrenceType != RecurrenceNone || input.StartsOn != "" || input.EndsOn != "" || input.LocalStartTime != "" || input.DurationMinutes != 0 {
			return invalidSchedule("unscheduled fields are inconsistent")
		}
	case TimingDate:
		if input.StartsOn == "" || input.LocalStartTime != "" || input.DurationMinutes != 0 {
			return invalidSchedule("date fields are inconsistent")
		}
	case TimingTimeBlock:
		if input.StartsOn == "" || input.LocalStartTime == "" || input.DurationMinutes <= 0 {
			return invalidSchedule("time_block fields are inconsistent")
		}
		clock, err := parseLocalClock(input.LocalStartTime)
		if err != nil {
			return invalidSchedule("invalid local_start_time")
		}
		normalized.LocalStartTime = clock.Format("15:04:05")
	default:
		return invalidSchedule("unsupported timing_type")
	}
	return nil
}

func normalizeRecurrence(input ScheduleInput, normalized *Schedule) error {
	switch input.RecurrenceType {
	case RecurrenceNone:
		if !emptyRule(input.Rule) || input.EndsOn != "" {
			return invalidSchedule("non-recurring schedule has recurrence fields")
		}
		return nil
	case RecurrenceDaily, RecurrenceWeekly, RecurrenceMonthly:
		if input.StartsOn == "" {
			return invalidSchedule("recurring schedule requires starts_on")
		}
	default:
		return invalidSchedule("unsupported recurrence_type")
	}

	rule, fields, err := decodeRecurrenceRule(input.Rule)
	if err != nil {
		return err
	}
	if rule.Interval <= 0 {
		return invalidSchedule("interval must be positive")
	}

	switch input.RecurrenceType {
	case RecurrenceDaily:
		if fields["weekdays"] || fields["month_days"] {
			return invalidSchedule("daily rule has unsupported fields")
		}
	case RecurrenceWeekly:
		if !fields["weekdays"] || len(rule.Weekdays) == 0 || fields["month_days"] {
			return invalidSchedule("weekly rule requires only weekdays")
		}
		for _, weekday := range rule.Weekdays {
			if weekday < 0 || weekday > 6 {
				return invalidSchedule("weekday is outside 0..6")
			}
		}
		rule.Weekdays = sortedUnique(rule.Weekdays)
	case RecurrenceMonthly:
		if !fields["month_days"] || len(rule.MonthDays) == 0 || fields["weekdays"] {
			return invalidSchedule("monthly rule requires only month_days")
		}
		for _, monthDay := range rule.MonthDays {
			if monthDay < 1 || monthDay > 31 {
				return invalidSchedule("month_day is outside 1..31")
			}
		}
		rule.MonthDays = sortedUnique(rule.MonthDays)
	}

	normalized.Rule = &rule
	return nil
}

func decodeRecurrenceRule(raw json.RawMessage) (RecurrenceRule, map[string]bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return RecurrenceRule{}, nil, invalidSchedule("recurrence rule is required")
	}

	var document map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &document); err != nil || document == nil {
		return RecurrenceRule{}, nil, invalidSchedule("invalid recurrence rule")
	}
	fields := make(map[string]bool, len(document))
	for field := range document {
		switch field {
		case "interval", "weekdays", "month_days":
			fields[field] = true
		default:
			return RecurrenceRule{}, nil, invalidSchedule("unknown recurrence rule field")
		}
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var rule RecurrenceRule
	if err := decoder.Decode(&rule); err != nil {
		return RecurrenceRule{}, nil, invalidSchedule("invalid recurrence rule")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return RecurrenceRule{}, nil, invalidSchedule("recurrence rule has trailing data")
	}
	return rule, fields, nil
}

func emptyRule(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return true
	}
	var document map[string]json.RawMessage
	return json.Unmarshal(trimmed, &document) == nil && document != nil && len(document) == 0
}

func sortedUnique(values []int) []int {
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Ints(result)
	return result
}

func ResolveLocalDateTime(dateValue, clockValue, timezone string) (LocalDateTimeResolution, error) {
	date, err := parseLocalDate(dateValue)
	if err != nil {
		return LocalDateTimeResolution{}, invalidSchedule("invalid local date")
	}
	clock, err := parseLocalClock(clockValue)
	if err != nil {
		return LocalDateTimeResolution{}, invalidSchedule("invalid local time")
	}
	location, err := loadIANALocation(timezone)
	if err != nil {
		return LocalDateTimeResolution{}, err
	}

	wall := time.Date(date.Year(), date.Month(), date.Day(), clock.Hour(), clock.Minute(), clock.Second(), 0, time.UTC)
	offsets := offsetsNear(wall, location)
	candidates := make([]OffsetCandidate, 0, len(offsets))
	for offset := range offsets {
		candidate := wall.Add(-time.Duration(offset) * time.Second)
		local := candidate.In(location)
		if local.Year() != date.Year() || local.Month() != date.Month() || local.Day() != date.Day() ||
			local.Hour() != clock.Hour() || local.Minute() != clock.Minute() || local.Second() != clock.Second() {
			continue
		}
		candidates = append(candidates, OffsetCandidate{OffsetSeconds: offset, UTC: candidate})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].UTC.Before(candidates[j].UTC) })

	resolution := LocalDateTimeResolution{Candidates: candidates}
	switch len(candidates) {
	case 0:
		return resolution, fmt.Errorf("%w: %s %s in %s", ErrNonexistentLocalTime, dateValue, clockValue, timezone)
	case 1:
		resolution.UTC = candidates[0].UTC
		return resolution, nil
	default:
		return resolution, fmt.Errorf("%w: %s %s in %s", ErrAmbiguousLocalTime, dateValue, clockValue, timezone)
	}
}

func ResolveTimeBlockUTC(dateValue, clockValue, timezone string, durationMinutes int, selectedOffsetSeconds *int) (UTCInstantRange, []OffsetCandidate, error) {
	if durationMinutes <= 0 {
		return UTCInstantRange{}, nil, invalidSchedule("duration must be positive")
	}
	resolution, err := ResolveLocalDateTime(dateValue, clockValue, timezone)
	if err != nil && !isAmbiguousLocalTime(err) {
		return UTCInstantRange{}, resolution.Candidates, err
	}

	var start time.Time
	if len(resolution.Candidates) == 1 {
		if selectedOffsetSeconds != nil && *selectedOffsetSeconds != resolution.Candidates[0].OffsetSeconds {
			return UTCInstantRange{}, resolution.Candidates, invalidSchedule("selected offset is not valid")
		}
		start = resolution.Candidates[0].UTC
	} else {
		if selectedOffsetSeconds == nil {
			return UTCInstantRange{}, resolution.Candidates, err
		}
		for _, candidate := range resolution.Candidates {
			if candidate.OffsetSeconds == *selectedOffsetSeconds {
				start = candidate.UTC
				break
			}
		}
		if start.IsZero() {
			return UTCInstantRange{}, resolution.Candidates, invalidSchedule("selected offset is not a candidate")
		}
	}

	duration := time.Duration(durationMinutes) * time.Minute
	if duration <= 0 {
		return UTCInstantRange{}, resolution.Candidates, invalidSchedule("duration is too large")
	}
	return UTCInstantRange{StartUTC: start, EndUTC: start.Add(duration)}, resolution.Candidates, nil
}

func ResolveAllDayRangeUTC(startDate, exclusiveEndDate, timezone string) (AllDayUTCInstantRange, error) {
	start, err := parseLocalDate(startDate)
	if err != nil {
		return AllDayUTCInstantRange{}, invalidSchedule("invalid all-day start")
	}
	end := time.Time{}
	if exclusiveEndDate == "" {
		end = start.AddDate(0, 0, 1)
		exclusiveEndDate = formatLocalDate(end)
	} else {
		end, err = parseLocalDate(exclusiveEndDate)
		if err != nil {
			return AllDayUTCInstantRange{}, invalidSchedule("invalid all-day exclusive end")
		}
	}
	if !end.After(start) {
		return AllDayUTCInstantRange{}, invalidSchedule("all-day exclusive end must follow start")
	}

	startResolution, err := ResolveLocalDateTime(formatLocalDate(start), "00:00:00", timezone)
	if err != nil {
		return AllDayUTCInstantRange{}, err
	}
	endResolution, err := ResolveLocalDateTime(formatLocalDate(end), "00:00:00", timezone)
	if err != nil {
		return AllDayUTCInstantRange{}, err
	}
	return AllDayUTCInstantRange{
		StartDate:        formatLocalDate(start),
		ExclusiveEndDate: formatLocalDate(end),
		StartUTC:         startResolution.UTC,
		EndUTC:           endResolution.UTC,
	}, nil
}

func offsetsNear(wall time.Time, location *time.Location) map[int]struct{} {
	offsets := make(map[int]struct{}, 2)
	const window = 48 * time.Hour
	const step = 15 * time.Minute
	for instant := wall.Add(-window); !instant.After(wall.Add(window)); instant = instant.Add(step) {
		_, offset := instant.In(location).Zone()
		offsets[offset] = struct{}{}
	}
	return offsets
}

func loadIANALocation(value string) (*time.Location, error) {
	if value == "" || value == "Local" || strings.TrimSpace(value) != value || (value != "UTC" && !strings.Contains(value, "/")) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidTimezone, value)
	}
	location, err := time.LoadLocation(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrInvalidTimezone, value)
	}
	return location, nil
}

func parseLocalDate(value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value {
		return time.Time{}, fmt.Errorf("invalid date %q", value)
	}
	return parsed, nil
}

func formatLocalDate(value time.Time) string {
	return value.Format("2006-01-02")
}

func parseLocalClock(value string) (time.Time, error) {
	for _, layout := range []string{"15:04:05", "15:04"} {
		parsed, err := time.Parse(layout, value)
		if err == nil && parsed.Format(layout) == value {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid local clock %q", value)
}

func invalidSchedule(detail string) error {
	return fmt.Errorf("%w: %s", ErrInvalidSchedule, detail)
}

func isAmbiguousLocalTime(err error) bool {
	return ErrorCodeOf(err) == ErrorCodeAmbiguousLocalTime
}
