package service

import (
	"errors"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

func GetNotes(folderID, projectID, sort string, unassigned bool, page, pageSize int) ([]model.Note, int, error) {
	return repository.GetNotes(folderID, projectID, sort, unassigned, page, pageSize)
}

func GetNote(id string) (*model.Note, error) {
	return repository.GetNoteByID(id)
}

func CreateNote(req *model.CreateNoteRequest) (*model.Note, error) {
	if req.Tags == "" {
		req.Tags = "[]"
	}
	return repository.CreateNoteWithProjectIDs(req)
}

func UpdateNote(id string, req *model.UpdateNoteRequest) (*model.Note, error) {
	existing, err := repository.GetNoteByID(id)
	if err != nil {
		return nil, errors.New("note not found")
	}
	_ = existing
	return repository.UpdateNote(id, req)
}

func DeleteNote(id string) error {
	return repository.DeleteNote(id)
}
