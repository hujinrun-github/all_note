package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GetInboxItems(ctx context.Context, store storage.Store, kind string, page, pageSize int) ([]model.InboxItem, int, error) {
	return store.Inbox().List(ctx, kind, page, pageSize)
}

func CreateInboxItem(ctx context.Context, store storage.Store, req *model.CreateInboxRequest) (*model.InboxItem, error) {
	item := &model.InboxItem{
		Kind:  req.Kind,
		Title: req.Title,
		Body:  req.Body,
	}
	if err := store.Inbox().Create(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func ConvertInboxItem(ctx context.Context, store storage.Store, id string, req *model.ConvertInboxRequest) (interface{}, error) {
	now := time.Now()
	tomorrow9am := time.Date(now.Year(), now.Month(), now.Day()+1, 9, 0, 0, 0, now.Location()).Unix()
	tomorrow10am := time.Date(now.Year(), now.Month(), now.Day()+1, 10, 0, 0, 0, now.Location()).Unix()

	var convertedID string
	var converted interface{}

	err := store.Transact(ctx, func(tx storage.Store) error {
		inboxItem, err := tx.Inbox().GetByID(ctx, id)
		if err != nil {
			return errors.New("inbox item not found")
		}
		if inboxItem.ConvertedTo != nil {
			return errors.New("already converted")
		}

		switch req.Kind {
		case "note":
			body := ""
			if inboxItem.Body != nil {
				body = *inboxItem.Body
			}
			note, err := tx.Notes().Create(ctx, &model.CreateNoteRequest{
				Title:    inboxItem.Title,
				Body:     body,
				FolderID: "__uncategorized",
				Tags:     "[]",
			})
			if err != nil {
				return err
			}
			convertedID = note.ID
			converted = note

		case "task":
			task := &model.Task{
				Title:         inboxItem.Title,
				Status:        "open",
				ExecutionType: "single",
			}
			if err := tx.Tasks().Create(ctx, task); err != nil {
				return err
			}
			convertedID = task.ID
			got, err := tx.Tasks().GetByID(ctx, task.ID)
			if err != nil {
				return err
			}
			converted = got

		case "event":
			event := &model.Event{
				Title:     inboxItem.Title,
				StartTime: tomorrow9am,
				EndTime:   tomorrow10am,
			}
			if err := tx.Events().Create(ctx, event); err != nil {
				return err
			}
			convertedID = event.ID
			converted = event

		default:
			return errors.New("invalid target kind")
		}

		if err := tx.Inbox().MarkConverted(ctx, id, fmt.Sprintf("%s:%s", req.Kind, convertedID)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return converted, nil
}

func DeleteInboxItem(ctx context.Context, store storage.Store, id string) error {
	return store.Inbox().Delete(ctx, id)
}

func BatchInbox(ctx context.Context, store storage.Store, req *model.BatchInboxRequest) (int64, error) {
	switch req.Action {
	case "archive":
		return store.Inbox().BatchArchive(ctx, req.IDs)
	case "delete":
		return store.Inbox().BatchDelete(ctx, req.IDs)
	default:
		return 0, errors.New("invalid action: must be 'archive' or 'delete'")
	}
}
