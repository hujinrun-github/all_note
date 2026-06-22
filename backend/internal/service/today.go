package service

import (
	"context"
	"sort"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

const OverdueWindowDays = 7

type TodayData struct {
	TodayTasks   []model.Task  `json:"todayTasks"`
	OverdueTasks []model.Task  `json:"overdueTasks"`
	Events       []model.Event `json:"events"`
	RecentNotes  []model.Note  `json:"recentNotes"`
}

func GetToday(ctx context.Context, store storage.Store, recurrenceSvc *RecurrenceService) (*TodayData, error) {
	now := time.Now()
	todayStr := now.Format("2006-01-02")
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	todayEnd := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location()).Unix()
	overdueCutoff := todayStart - int64(OverdueWindowDays*86400)

	// 1. Get single tasks (filtered to execution_type=single)
	todayTasks, overdueTasks, err := store.Tasks().Today(ctx, todayStart, todayEnd, overdueCutoff)
	if err != nil {
		return nil, err
	}

	// 2. Get recurring occurrences for today
	activeRules, err := store.Recurrence().ListActiveRules(ctx, todayStr, todayStr)
	if err != nil {
		return nil, err
	}
	if len(activeRules) > 0 {
		existingOccurrences, _ := store.Recurrence().ListOccurrences(ctx, todayStr, todayStr)
		occMap := make(map[string]*model.TaskOccurrence)
		for i := range existingOccurrences {
			occMap[existingOccurrences[i].TaskID] = &existingOccurrences[i]
		}
		for _, rule := range activeRules {
			dates := ExpandRuleOccurrences(&rule, todayStr, todayStr)
			if len(dates) == 0 {
				continue
			}
			task, err := store.Tasks().GetByID(ctx, rule.TaskID)
			if err != nil {
				continue
			}
			for _, date := range dates {
				task.ExecutionType = "recurring"
				task.OccurrenceDate = &date
				status := "open"
				if occ, ok := occMap[rule.TaskID]; ok && occ.OccurrenceDate == date {
					status = occ.Status
				}
				task.OccurrenceStatus = &status
				label := GenerateRecurrenceLabel(&rule)
				task.RecurrenceLabel = &label
				task.PlannedDate = nil // template has no planned_date
				todayTasks = append(todayTasks, *task)
			}
		}
	}

	// 3. Sort merged list: sort_order ASC, created_at DESC
	sort.SliceStable(todayTasks, func(i, j int) bool {
		if todayTasks[i].SortOrder != todayTasks[j].SortOrder {
			return todayTasks[i].SortOrder < todayTasks[j].SortOrder
		}
		return todayTasks[i].CreatedAt > todayTasks[j].CreatedAt
	})

	// 4. Events and notes
	events, err := store.Events().Today(ctx, todayStart, todayEnd)
	if err != nil {
		return nil, err
	}
	recentNotes, err := store.Notes().Recent(ctx, 5)
	if err != nil {
		return nil, err
	}

	return &TodayData{
		TodayTasks:   todayTasks,
		OverdueTasks: overdueTasks,
		Events:       events,
		RecentNotes:  recentNotes,
	}, nil
}
