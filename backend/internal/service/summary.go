package service

import (
	"sort"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

func GetSummary(params model.SummaryParams) (*model.SummaryData, error) {
	tasks, total, err := repository.GetCompletedTasksByRange(params.From, params.To, params.Page, params.PageSize)
	if err != nil {
		return nil, err
	}

	projectIDs := make(map[string]bool)
	for _, t := range tasks {
		if t.Project != nil {
			projectIDs[t.Project.ID] = true
		}
	}
	ids := make([]string, 0, len(projectIDs))
	for id := range projectIDs {
		ids = append(ids, id)
	}
	noteMap, err := repository.GetNotesByProjectIDs(ids)
	if err != nil {
		noteMap = map[string][]model.NoteRef{}
	}

	// Attach notes and group by date
	for i := range tasks {
		if tasks[i].Project != nil {
			tasks[i].LinkedNotes = noteMap[tasks[i].Project.ID]
		}
	}

	groups := groupByDate(tasks)
	activeDays, projectCount, err := repository.GetSummaryStats(params.From, params.To)
	if err != nil {
		activeDays, projectCount = 0, 0
	}

	return model.NewSummaryData(groups, activeDays, projectCount, total), nil
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
