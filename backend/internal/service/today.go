package service

import (
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

const OverdueWindowDays = 7

type TodayData struct {
	TodayTasks   []model.Task  `json:"todayTasks"`
	OverdueTasks []model.Task  `json:"overdueTasks"`
	Events       []model.Event `json:"events"`
	RecentNotes  []model.Note  `json:"recentNotes"`
}

func GetToday() (*TodayData, error) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	todayEnd := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location()).Unix()
	overdueCutoff := todayStart - int64(OverdueWindowDays*86400)

	todayTasks, overdueTasks, err := repository.GetTodayTasks(todayStart, todayEnd, overdueCutoff)
	if err != nil {
		return nil, err
	}

	events, err := repository.GetTodayEvents(todayStart, todayEnd)
	if err != nil {
		return nil, err
	}

	recentNotes, err := repository.GetRecentNotes(5)
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
