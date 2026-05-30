package service

import (
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

func Search(q string, page, pageSize int) ([]model.SearchResult, int, error) {
	return repository.Search(q, page, pageSize)
}
