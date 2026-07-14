package service

import (
	"context"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func ListCalendarProjectSources(ctx context.Context, store storage.Store) (*model.CalendarProjectSourcesResponse, error) {
	return store.Calendar().ListProjectSources(ctx)
}

func SaveCalendarProjectSources(ctx context.Context, store storage.Store, sources []model.CalendarProjectSourceInput) (*model.CalendarProjectSourcesResponse, error) {
	return store.Calendar().SaveProjectSources(ctx, sources)
}
