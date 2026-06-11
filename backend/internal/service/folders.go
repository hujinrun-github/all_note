package service

import (
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

func GetFolders() ([]model.Folder, error) {
	return repository.GetFolders()
}
