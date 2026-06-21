package service

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

// RecurrenceStore is the subset of Store needed by RecurrenceService.
type RecurrenceStore interface {
	// Methods defined via storage.Store in production.
}

var weekdayNames = map[int]string{1: "一", 2: "二", 3: "三", 4: "四", 5: "五", 6: "六", 7: "日"}

// ExpandRuleOccurrences returns all occurrence dates in [from, to] for a given rule.
// Uses rule.timezone for date calculations. Returns empty if rule is disabled.
func ExpandRuleOccurrences(rule *model.RecurrenceRule, from, to string) []string {
	if !rule.Enabled {
		return nil
	}
	loc, err := time.LoadLocation(rule.Timezone)
	if err != nil {
		loc = time.Local
	}
	start, _ := time.ParseInLocation("2006-01-02", rule.StartDate, loc)
	fromDate, _ := time.ParseInLocation("2006-01-02", from, loc)
	toDate, _ := time.ParseInLocation("2006-01-02", to, loc)
	var endDate time.Time
	if rule.EndDate != nil {
		endDate, _ = time.ParseInLocation("2006-01-02", *rule.EndDate, loc)
	} else {
		endDate = toDate
	}
	if start.After(endDate) {
		return nil
	}
	if fromDate.Before(start) {
		fromDate = start
	}
	if toDate.After(endDate) {
		toDate = endDate
	}
	if fromDate.After(toDate) {
		return nil
	}

	var dates []string
	switch rule.Frequency {
	case "daily":
		dates = expandDaily(start, fromDate, toDate, rule.Interval)
	case "weekly":
		dates = expandWeekly(start, fromDate, toDate, rule.Interval, rule.Weekdays, loc)
	case "monthly":
		dates = expandMonthly(start, fromDate, toDate, rule.Interval, rule.MonthDays)
	}
	return dates
}

func expandDaily(start, from, to time.Time, interval int) []string {
	// Walk forward from start by interval until >= from
	current := start
	for current.Before(from) {
		current = current.AddDate(0, 0, interval)
	}
	var dates []string
	for !current.After(to) {
		dates = append(dates, current.Format("2006-01-02"))
		current = current.AddDate(0, 0, interval)
	}
	return dates
}

func expandWeekly(start, from, to time.Time, interval int, weekdays []int, loc *time.Location) []string {
	if len(weekdays) == 0 {
		return nil
	}
	// Find the Monday of start's ISO week (week 0)
	startISOYear, startISOWeek := start.ISOWeek()
	week0Monday := isoWeekMonday(startISOYear, startISOWeek, loc)

	weekdaySet := make(map[time.Weekday]bool)
	for _, d := range weekdays {
		weekdaySet[time.Weekday(d%7)] = true // Sunday=0 in Go, ISO Sunday=7→0
	}

	var dates []string
	for w := 0; ; w += interval {
		weekStart := week0Monday.AddDate(0, 0, w*7)
		if weekStart.After(to) {
			break
		}
		for _, d := range weekdays {
			goWd := time.Weekday(d % 7)
			date := weekStart.AddDate(0, 0, int(goWd)-int(time.Monday))
			if date.Before(from) || date.Before(start) || date.After(to) {
				continue
			}
			dates = append(dates, date.Format("2006-01-02"))
		}
	}
	return dates
}

func isoWeekMonday(year, week int, loc *time.Location) time.Time {
	// Jan 4 is always in ISO week 1
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, loc)
	daysToMonday := int(time.Monday - jan4.Weekday())
	if jan4.Weekday() == time.Sunday {
		daysToMonday = -6
	}
	week1Monday := jan4.AddDate(0, 0, daysToMonday)
	return week1Monday.AddDate(0, 0, (week-1)*7)
}

func expandMonthly(start, from, to time.Time, interval int, monthDays []int) []string {
	if len(monthDays) == 0 {
		return nil
	}
	var dates []string
	current := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, start.Location())
	for !current.After(to) {
		for _, day := range monthDays {
			lastDay := daysInMonth(current.Year(), int(current.Month()))
			if day > lastDay {
				continue
			}
			date := time.Date(current.Year(), current.Month(), day, 0, 0, 0, 0, current.Location())
			if date.Before(from) || date.Before(start) || date.After(to) {
				continue
			}
			dates = append(dates, date.Format("2006-01-02"))
		}
		current = current.AddDate(0, interval, 0)
	}
	return dates
}

func daysInMonth(year, month int) int {
	return time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// GenerateRecurrenceLabel produces Chinese label like "每天", "每周一三五", "每月 1/15 号".
func GenerateRecurrenceLabel(rule *model.RecurrenceRule) string {
	var parts []string
	if rule.Interval > 1 {
		// Special case: weekly interval=2 → "隔周"
		if rule.Frequency == "weekly" && rule.Interval == 2 {
			parts = append(parts, "隔周")
		} else {
			parts = append(parts, "每 "+strconv.Itoa(rule.Interval))
			switch rule.Frequency {
			case "daily":
				parts = append(parts, " 天")
			case "weekly":
				parts = append(parts, " 周")
			case "monthly":
				parts = append(parts, " 个月")
			}
		}
	} else {
		switch rule.Frequency {
		case "daily":
			parts = append(parts, "每天")
		case "weekly":
			parts = append(parts, "每周")
		case "monthly":
			parts = append(parts, "每月")
		}
	}
	switch rule.Frequency {
	case "weekly":
		parts = append(parts, formatWeekdays(rule.Weekdays))
	case "monthly":
		if len(rule.MonthDays) > 0 {
			parts = append(parts, " "+formatMonthDays(rule.MonthDays)+" 号")
		}
	}
	// "（长期）" only for bare-minimal daily labels without EndDate
	if rule.EndDate == nil && rule.Interval == 1 &&
		rule.Frequency == "daily" && len(rule.Weekdays) == 0 && len(rule.MonthDays) == 0 {
		parts = append(parts, "（长期）")
	}
	return strings.Join(parts, "")
}

func formatWeekdays(days []int) string {
	if len(days) == 0 {
		return ""
	}
	if len(days) == 1 {
		return "周" + weekdayNames[days[0]]
	}
	// Check if consecutive
	sort.Ints(days)
	isConsecutive := true
	for i := 1; i < len(days); i++ {
		if days[i]-days[i-1] != 1 {
			isConsecutive = false
			break
		}
	}
	if isConsecutive && len(days) >= 3 {
		return weekdayNames[days[0]] + "至" + weekdayNames[days[len(days)-1]]
	}
	names := make([]string, len(days))
	for i, d := range days {
		names[i] = weekdayNames[d]
	}
	return strings.Join(names, "")
}

func formatMonthDays(days []int) string {
	if len(days) == 0 {
		return ""
	}
	sort.Ints(days)
	parts := make([]string, len(days))
	for i, d := range days {
		parts[i] = strconv.Itoa(d)
	}
	return strings.Join(parts, "/")
}

// ValidateRecurrenceConfig validates a RecurrenceConfig from API input.
func ValidateRecurrenceConfig(rc *model.RecurrenceConfig) error {
	if rc == nil {
		return fmt.Errorf("recurrence config is required")
	}
	if rc.StartDate == "" {
		return fmt.Errorf("请选择开始日期")
	}
	if rc.Frequency != "daily" && rc.Frequency != "weekly" && rc.Frequency != "monthly" {
		return fmt.Errorf("invalid frequency: %s", rc.Frequency)
	}
	if rc.Interval < 1 {
		rc.Interval = 1
	}
	if rc.Frequency == "weekly" && len(rc.Weekdays) == 0 {
		return fmt.Errorf("请选择每周执行的日期")
	}
	if rc.Frequency == "monthly" && len(rc.MonthDays) == 0 {
		return fmt.Errorf("请选择每月执行的日期")
	}
	for _, d := range rc.Weekdays {
		if d < 1 || d > 7 {
			return fmt.Errorf("invalid weekday: %d", d)
		}
	}
	for _, d := range rc.MonthDays {
		if d < 1 || d > 31 {
			return fmt.Errorf("invalid month day: %d", d)
		}
	}
	if rc.EndDate != nil && *rc.EndDate != "" && *rc.EndDate < rc.StartDate {
		return fmt.Errorf("结束日期不能早于开始日期")
	}
	if rc.Timezone == "" {
		rc.Timezone = defaultTimezone()
	}
	return nil
}

func defaultTimezone() string {
	return "Asia/Shanghai"
}

// ExpandWithStatus returns TaskOccurrence objects with status merged from completedMap.
// Dates not in completedMap default to "pending".
func ExpandWithStatus(rule *model.RecurrenceRule, from, to string, completedMap map[string]string) []model.TaskOccurrence {
	dates := ExpandRuleOccurrences(rule, from, to)
	occurrences := make([]model.TaskOccurrence, 0, len(dates))
	for _, d := range dates {
		status := "pending"
		if s, ok := completedMap[d]; ok {
			status = s
		}
		occurrences = append(occurrences, model.TaskOccurrence{
			TaskID:         rule.TaskID,
			OccurrenceDate: d,
			Status:         status,
		})
	}
	return occurrences
}
