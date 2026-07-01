package service

import (
	"context"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GetFolders(ctx context.Context, store storage.Store) ([]model.Folder, error) {
	return store.Folders().List(ctx)
}
