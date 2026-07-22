package taskdomain

import "time"

// ScheduleEffectiveRange is the immutable ScheduleVersion validity interval.
// From is inclusive, To is exclusive, and an empty To means that the version
// remains effective indefinitely.
type ScheduleEffectiveRange struct {
	From string
	To   string
}

// OccurrenceWindow is the finite, half-open local-date interval for which
// occurrence keys should be calculated.
type OccurrenceWindow struct {
	From string
	To   string
}

// CalculateOccurrenceKeys deterministically calculates the keys expected from
// a normalized schedule in the intersection of:
//
//   - the requested window [window.From, window.To),
//   - the ScheduleVersion interval [effective.From, effective.To), and
//   - the schedule interval [StartsOn, EndsOn], where EndsOn is inclusive.
//
// Recurring keys are local calendar dates (YYYY-MM-DD). Calendar arithmetic is
// deliberately independent of UTC duration, so daylight-saving transitions
// cannot skip or duplicate a local date. The function performs no I/O and does
// not read the clock.
func CalculateOccurrenceKeys(schedule Schedule, effective ScheduleEffectiveRange, window OccurrenceWindow) ([]string, error) {
	if err := validateOccurrenceKeySchedule(schedule); err != nil {
		return nil, err
	}

	windowFrom, err := requiredGeneratorDate(window.From, "window from")
	if err != nil {
		return nil, err
	}
	windowTo, err := requiredGeneratorDate(window.To, "window to")
	if err != nil {
		return nil, err
	}
	if !windowTo.After(windowFrom) {
		return nil, invalidSchedule("occurrence window must be a non-empty half-open range")
	}

	effectiveFrom, err := requiredGeneratorDate(effective.From, "effective from")
	if err != nil {
		return nil, err
	}
	var effectiveTo time.Time
	if effective.To != "" {
		effectiveTo, err = requiredGeneratorDate(effective.To, "effective to")
		if err != nil {
			return nil, err
		}
		if !effectiveTo.After(effectiveFrom) {
			return nil, invalidSchedule("effective range must be a non-empty half-open range")
		}
	}

	from := laterGeneratorDate(windowFrom, effectiveFrom)
	to := windowTo
	if !effectiveTo.IsZero() {
		to = earlierGeneratorDate(to, effectiveTo)
	}

	if schedule.TimingType == TimingUnscheduled {
		return []string{}, nil
	}

	startsOn, err := requiredGeneratorDate(schedule.StartsOn, "starts_on")
	if err != nil {
		return nil, err
	}
	from = laterGeneratorDate(from, startsOn)
	if schedule.EndsOn != "" {
		endsOn, parseErr := requiredGeneratorDate(schedule.EndsOn, "ends_on")
		if parseErr != nil {
			return nil, parseErr
		}
		// The recurrence end date includes the named day; all other ranges in
		// this calculator are half-open.
		to = earlierGeneratorDate(to, endsOn.AddDate(0, 0, 1))
	}
	if !from.Before(to) {
		return []string{}, nil
	}

	if schedule.RecurrenceType == RecurrenceNone {
		if startsOn.Before(from) || !startsOn.Before(to) {
			return []string{}, nil
		}
		return []string{"once"}, nil
	}

	keys := make([]string, 0)
	for date := from; date.Before(to); date = date.AddDate(0, 0, 1) {
		if recurrenceMatchesDate(schedule, startsOn, date) {
			keys = append(keys, formatLocalDate(date))
		}
	}
	return keys, nil
}

func validateOccurrenceKeySchedule(schedule Schedule) error {
	if _, err := loadIANALocation(schedule.Timezone); err != nil {
		return err
	}

	switch schedule.RecurrenceType {
	case RecurrenceNone:
		if schedule.Rule != nil {
			return invalidSchedule("non-recurring schedule must not have a rule")
		}
	case RecurrenceDaily:
		if schedule.Rule == nil || schedule.Rule.Interval <= 0 || len(schedule.Rule.Weekdays) != 0 || len(schedule.Rule.MonthDays) != 0 {
			return invalidSchedule("daily schedule is not normalized")
		}
	case RecurrenceWeekly:
		if schedule.Rule == nil || schedule.Rule.Interval <= 0 || len(schedule.Rule.Weekdays) == 0 || len(schedule.Rule.MonthDays) != 0 {
			return invalidSchedule("weekly schedule is not normalized")
		}
		for _, weekday := range schedule.Rule.Weekdays {
			if weekday < 0 || weekday > 6 {
				return invalidSchedule("weekly schedule contains an invalid weekday")
			}
		}
	case RecurrenceMonthly:
		if schedule.Rule == nil || schedule.Rule.Interval <= 0 || len(schedule.Rule.MonthDays) == 0 || len(schedule.Rule.Weekdays) != 0 {
			return invalidSchedule("monthly schedule is not normalized")
		}
		for _, monthDay := range schedule.Rule.MonthDays {
			if monthDay < 1 || monthDay > 31 {
				return invalidSchedule("monthly schedule contains an invalid month day")
			}
		}
	default:
		return invalidSchedule("unsupported recurrence_type")
	}

	switch schedule.TimingType {
	case TimingUnscheduled:
		if schedule.RecurrenceType != RecurrenceNone || schedule.StartsOn != "" {
			return invalidSchedule("unscheduled schedule is not normalized")
		}
	case TimingDate, TimingTimeBlock:
		if schedule.StartsOn == "" {
			return invalidSchedule("scheduled occurrence requires starts_on")
		}
	default:
		return invalidSchedule("unsupported timing_type")
	}
	return nil
}

func recurrenceMatchesDate(schedule Schedule, startsOn, date time.Time) bool {
	switch schedule.RecurrenceType {
	case RecurrenceDaily:
		return calendarDaysBetween(startsOn, date)%schedule.Rule.Interval == 0
	case RecurrenceWeekly:
		startWeek := isoWeekStart(startsOn)
		dateWeek := isoWeekStart(date)
		if calendarDaysBetween(startWeek, dateWeek)/7%schedule.Rule.Interval != 0 {
			return false
		}
		return containsGeneratorInt(schedule.Rule.Weekdays, int(date.Weekday()))
	case RecurrenceMonthly:
		monthDistance := (date.Year()-startsOn.Year())*12 + int(date.Month()-startsOn.Month())
		return monthDistance%schedule.Rule.Interval == 0 && containsGeneratorInt(schedule.Rule.MonthDays, date.Day())
	default:
		return false
	}
}

func isoWeekStart(date time.Time) time.Time {
	daysSinceMonday := (int(date.Weekday()) + 6) % 7
	return date.AddDate(0, 0, -daysSinceMonday)
}

func calendarDaysBetween(from, to time.Time) int {
	// Inputs are civil dates represented at UTC midnight, so a duration-based
	// difference here is safe and cannot cross a DST offset.
	return int(to.Sub(from) / (24 * time.Hour))
}

func containsGeneratorInt(values []int, candidate int) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func requiredGeneratorDate(value, field string) (time.Time, error) {
	if value == "" {
		return time.Time{}, invalidSchedule(field + " is required")
	}
	date, err := parseLocalDate(value)
	if err != nil {
		return time.Time{}, invalidSchedule("invalid " + field)
	}
	return date, nil
}

func laterGeneratorDate(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}

func earlierGeneratorDate(left, right time.Time) time.Time {
	if right.Before(left) {
		return right
	}
	return left
}
