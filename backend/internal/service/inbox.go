package service

import (
	"errors"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

func GetInboxItems(kind string, page, pageSize int) ([]model.InboxItem, int, error) {
	return repository.GetInboxItems(kind, page, pageSize)
}

func CreateInboxItem(req *model.CreateInboxRequest) (*model.InboxItem, error) {
	item := &model.InboxItem{
		Kind:  req.Kind,
		Title: req.Title,
		Body:  req.Body,
	}
	if err := repository.CreateInboxItem(item); err != nil {
		return nil, err
	}
	return item, nil
}

func ConvertInboxItem(id string, req *model.ConvertInboxRequest) (interface{}, error) {
	inboxItem, err := repository.GetInboxItemByID(id)
	if err != nil {
		return nil, errors.New("inbox item not found")
	}
	if inboxItem.ConvertedTo != nil {
		return nil, errors.New("already converted")
	}

	now := time.Now()
	tomorrow9am := time.Date(now.Year(), now.Month(), now.Day()+1, 9, 0, 0, 0, now.Location()).Unix()
	tomorrow10am := time.Date(now.Year(), now.Month(), now.Day()+1, 10, 0, 0, 0, now.Location()).Unix()

	var convertedID string

	switch req.Kind {
	case "note":
		body := ""
		if inboxItem.Body != nil {
			body = *inboxItem.Body
		}
		note := &model.Note{
			Title: inboxItem.Title,
			Body:  body,
		}
		if err := repository.CreateNote(note); err != nil {
			return nil, err
		}
		convertedID = note.ID

	case "task":
		task := &model.Task{
			Title: inboxItem.Title,
		}
		if err := repository.CreateTask(task); err != nil {
			return nil, err
		}
		convertedID = task.ID

	case "event":
		event := &model.Event{
			Title:     inboxItem.Title,
			StartTime: tomorrow9am,
			EndTime:   tomorrow10am,
		}
		if err := repository.CreateEvent(event); err != nil {
			return nil, err
		}
		convertedID = event.ID

	default:
		return nil, errors.New("invalid target kind")
	}

	if err := repository.MarkInboxConverted(id, convertedID); err != nil {
		return nil, err
	}

	switch req.Kind {
	case "note":
		return repository.GetNoteByID(convertedID)
	case "task":
		return repository.GetTaskByID(convertedID)
	case "event":
		return repository.GetEventByID(convertedID)
	}
	return nil, errors.New("unexpected kind")
}

func DeleteInboxItem(id string) error {
	return repository.DeleteInboxItem(id)
}

func BatchInbox(req *model.BatchInboxRequest) (int64, error) {
	switch req.Action {
	case "archive":
		return repository.BatchArchiveInbox(req.IDs)
	case "delete":
		return repository.BatchDeleteInbox(req.IDs)
	default:
		return 0, errors.New("invalid action: must be 'archive' or 'delete'")
	}
}
