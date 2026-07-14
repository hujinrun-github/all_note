package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

var ErrInvalidEventProject = errors.New("invalid event project")

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
	projectID, err := validateEventProjectID(ctx, store, req.ProjectID)
	if err != nil {
		return nil, err
	}
	event := &model.Event{
		Title:     req.Title,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Location:  req.Location,
		Kind:      req.Kind,
		ProjectID: projectID,
	}
	if err := store.Events().Create(ctx, event); err != nil {
		return nil, err
	}
	return event, nil
}

func UpdateEvent(ctx context.Context, store storage.Store, id string, req *model.UpdateEventRequest) (*model.Event, error) {
	projectID, err := validateEventProjectID(ctx, store, req.ProjectID)
	if err != nil {
		return nil, err
	}
	updateReq := *req
	updateReq.ProjectID = projectID
	return store.Events().Update(ctx, id, &updateReq)
}

func DeleteEvent(ctx context.Context, store storage.Store, id string) error {
	return store.Events().Delete(ctx, id)
}

func validateEventProjectID(ctx context.Context, store storage.Store, projectID *string) (*string, error) {
	if projectID == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*projectID)
	if trimmed == "" {
		return &trimmed, nil
	}
	if _, err := store.Tasks().GetProjectByID(ctx, trimmed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrInvalidEventProject, trimmed)
		}
		return nil, err
	}
	return &trimmed, nil
}
