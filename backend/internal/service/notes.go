package service

import (
	"errors"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

func GetNotes(folderID, sort string, page, pageSize int) ([]model.Note, int, error) {
	return repository.GetNotes(folderID, sort, page, pageSize)
}

func GetNote(id string) (*model.Note, error) {
	return repository.GetNoteByID(id)
}

func CreateNote(req *model.CreateNoteRequest) (*model.Note, error) {
	if req.Tags == "" {
		req.Tags = "[]"
	}
	note := &model.Note{
		Title:    req.Title,
		Body:     req.Body,
		FolderID: req.FolderID,
		Tags:     req.Tags,
	}
	if err := repository.CreateNote(note); err != nil {
		return nil, err
	}
	return note, nil
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
