package service

import (
	"context"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GetEvents(ctx context.Context, store storage.Store, month string, page, pageSize int) ([]model.Event, int, error) {
	t, err := time.Parse("2006-01", month)
	if err != nil {
		return nil, 0, err
	}
	monthStart := t.Unix()
	monthEnd := t.AddDate(0, 1, 0).Unix()
	return store.Events().List(ctx, monthStart, monthEnd, page, pageSize)
}

func CreateEvent(ctx context.Context, store storage.Store, req *model.CreateEventRequest) (*model.Event, error) {
	event := &model.Event{
		Title:     req.Title,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Location:  req.Location,
		Kind:      req.Kind,
	}
	if err := store.Events().Create(ctx, event); err != nil {
		return nil, err
	}
	return event, nil
}

func UpdateEvent(ctx context.Context, store storage.Store, id string, req *model.UpdateEventRequest) (*model.Event, error) {
	return store.Events().Update(ctx, id, req)
}

func DeleteEvent(ctx context.Context, store storage.Store, id string) error {
	return store.Events().Delete(ctx, id)
}
