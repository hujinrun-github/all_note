package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func TestPostgresPublishedCursorFollowsCommitVisibilityNotBigserialAllocation(t *testing.T) {
	schema := fmt.Sprintf("fs_test_mobile_cursor_order_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	opened, err := (Provider{}).Open(ctx, storage.Config{
		Env: "test", Driver: storage.DriverPostgres, URL: createPostgresTestSchema(t, schema),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	workspaceID := "cursor_order_workspace"
	requestContext := seedPostgresMobileHTTPWorkspace(t, opened, workspaceID)
	deviceID := "71717171-7171-4171-8171-717171717171"
	firstClientID := "81818181-8181-4181-8181-818181818181"
	secondClientID := "91919191-9191-4191-8191-919191919191"
	firstReady := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)

	go func() {
		firstDone <- opened.Transact(requestContext, func(tx storage.Store) error {
			repository, err := storage.MobileSyncRepositoryFrom(tx)
			if err != nil {
				return err
			}
			title := "Allocated first, committed second"
			if _, err := repository.ApplyNoteMutation(requestContext, model.MobileNoteMutation{
				MutationID: "a1a1a1a1-a1a1-41a1-81a1-a1a1a1a1a1a1", DeviceClientID: deviceID,
				EntityClientID: firstClientID, Operation: model.MobileOperationNoteCreate,
				RequestSHA256: "allocated-first", Payload: model.MobileNotePayload{Title: &title},
			}); err != nil {
				return err
			}
			close(firstReady)
			select {
			case <-releaseFirst:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()

	select {
	case <-firstReady:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	repository, err := storage.MobileSyncRepositoryFrom(opened)
	if err != nil {
		t.Fatal(err)
	}
	secondTitle := "Allocated second, committed first"
	if _, err := repository.ApplyNoteMutation(requestContext, model.MobileNoteMutation{
		MutationID: "b2b2b2b2-b2b2-42b2-82b2-b2b2b2b2b2b2", DeviceClientID: deviceID,
		EntityClientID: secondClientID, Operation: model.MobileOperationNoteCreate,
		RequestSHA256: "committed-first", Payload: model.MobileNotePayload{Title: &secondTitle},
	}); err != nil {
		t.Fatal(err)
	}
	if count, err := repository.PublishPendingChanges(requestContext, 100, time.Now().UTC().Unix()); err != nil || count != 1 {
		t.Fatalf("first publish count=%d err=%v", count, err)
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if count, err := repository.PublishPendingChanges(requestContext, 100, time.Now().UTC().Unix()); err != nil || count != 1 {
		t.Fatalf("second publish count=%d err=%v", count, err)
	}
	page, err := repository.ReadCommittedChanges(requestContext, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Changes) != 2 || page.Changes[0].Entity.ClientID != secondClientID || page.Changes[1].Entity.ClientID != firstClientID {
		t.Fatalf("published commit order = %+v", page.Changes)
	}
}
