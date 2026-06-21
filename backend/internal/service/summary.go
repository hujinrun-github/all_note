package service

import (
	"context"
	"sort"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GetSummary(ctx context.Context, store storage.Store, params model.SummaryParams) (*model.SummaryData, error) {
	const maxItems = 10000

	// 1. Single tasks
	singleTasks, singleTotal, err := store.Tasks().GetCompletedTasksByRange(ctx, params.From, params.To, 1, maxItems)
	if err != nil {
		return nil, err
	}

	// 2. Recurring occurrences
	recurringSummaries, err := store.Recurrence().GetCompletedOccurrencesByRange(ctx, params.From, params.To)
	if err != nil {
		recurringSummaries = nil // graceful degradation
	}

	// 3. Merge and sort by CompletedAt descending
	all := append(singleTasks, recurringSummaries...)
	truncated := false
	if len(all) > maxItems {
		sortByCompletedAtDesc(all)
		all = all[:maxItems]
		truncated = true
	} else {
		sortByCompletedAtDesc(all)
	}

	// 4. Paginate in memory
	total := singleTotal + len(recurringSummaries)
	start := (params.Page - 1) * params.PageSize
	if start > len(all) {
		all = nil
	} else {
		end := start + params.PageSize
		if end > len(all) {
			end = len(all)
		}
		all = all[start:end]
	}

	// 5. Attach notes
	projectIDs := make(map[string]bool)
	for _, t := range all {
		if t.Project != nil {
			projectIDs[t.Project.ID] = true
		}
	}
	ids := make([]string, 0, len(projectIDs))
	for id := range projectIDs {
		ids = append(ids, id)
	}
	noteMap, err := store.Notes().GetNotesByProjectIDs(ctx, ids)
	if err != nil {
		noteMap = map[string][]model.NoteRef{}
	}
	for i := range all {
		if all[i].Project != nil {
			all[i].LinkedNotes = noteMap[all[i].Project.ID]
		}
	}

	// 6. Group by date
	groups := groupByDate(all)
	if groups == nil {
		groups = []model.DateGroup{}
	}

	// 7. Get active_days + project_count
	activeDays, projectCount, err := store.Tasks().GetSummaryStats(ctx, params.From, params.To)
	if err != nil {
		activeDays, projectCount = 0, 0
	}

	result := model.NewSummaryData(groups, activeDays, projectCount, total)
	_ = truncated
	return result, nil
}

func sortByCompletedAtDesc(tasks []model.TaskSummary) {
	sort.Slice(tasks, func(i, j int) bool {
		a, b := tasks[i].CompletedAt, tasks[j].CompletedAt
		if a == nil && b == nil {
			return false
		}
		if a == nil {
			return false
		}
		if b == nil {
			return true
		}
		return *a > *b
	})
}

func groupByDate(tasks []model.TaskSummary) []model.DateGroup {
	groups := make(map[string][]model.TaskSummary)
	var dates []string
	for _, t := range tasks {
		var date string
		if t.CompletedAt != nil {
			date = time.Unix(*t.CompletedAt, 0).Format("2006-01-02")
		}
		if date == "" {
			date = "未知日期"
		}
		if _, ok := groups[date]; !ok {
			dates = append(dates, date)
		}
		groups[date] = append(groups[date], t)
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i] > dates[j] })
	var result []model.DateGroup
	for _, d := range dates {
		result = append(result, model.DateGroup{Date: d, Tasks: groups[d], Count: len(groups[d])})
	}
	return result
}
