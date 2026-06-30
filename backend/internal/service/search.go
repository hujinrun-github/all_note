package service

import (
	"context"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func Search(ctx context.Context, store storage.Store, q string, page, pageSize int) ([]model.SearchResult, int, error) {
	return store.Search().Search(ctx, q, page, pageSize)
}
