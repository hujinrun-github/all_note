package contracttest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/mobilesync"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func RunMobileSyncNoteSuite(t *testing.T, factory StoreFactory) {
	t.Helper()

	t.Run("MSYNC-STORE-001_CreateReplayAndRejectChangedPayload", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		title := "Offline note"
		body := "Created on a synthetic phone"
		create := model.MobileNoteMutation{
			MutationID:     "11111111-1111-4111-8111-111111111111",
			DeviceClientID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			EntityClientID: "22222222-2222-4222-8222-222222222222",
			Operation:      model.MobileOperationNoteCreate,
			RequestSHA256:  "request-a",
			Payload:        model.MobileNotePayload{Title: &title, Body: &body},
		}
		first, err := repository.ApplyNoteMutation(ctx, create)
		if err != nil {
			t.Fatalf("apply create: %v", err)
		}
		if first.Status != model.MobileMutationApplied || first.Entity == nil || first.Entity.Revision != 1 || first.Entity.ClientID != create.EntityClientID {
			t.Fatalf("unexpected create result: %+v", first)
		}
		replayed, err := repository.ApplyNoteMutation(ctx, create)
		if err != nil {
			t.Fatalf("replay create: %v", err)
		}
		if !reflect.DeepEqual(replayed, first) {
			t.Fatalf("replay result = %+v, want %+v", replayed, first)
		}
		changed := create
		changed.RequestSHA256 = "request-b"
		if _, err := repository.ApplyNoteMutation(ctx, changed); !errors.Is(err, storage.ErrMutationIDReused) {
			t.Fatalf("changed replay error = %v, want ErrMutationIDReused", err)
		}
		changes, err := repository.ListPendingChanges(ctx)
		if err != nil {
			t.Fatalf("list changes: %v", err)
		}
		if len(changes) != 1 || changes[0].MutationID != create.MutationID {
			t.Fatalf("changes = %+v, want one create", changes)
		}
	})

	t.Run("MSYNC-STORE-002_BusinessReceiptAndOutboxRollbackTogether", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		title := "Rolled back note"
		mutation := model.MobileNoteMutation{
			MutationID:     "33333333-3333-4333-8333-333333333333",
			DeviceClientID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			EntityClientID: "44444444-4444-4444-8444-444444444444",
			Operation:      model.MobileOperationNoteCreate,
			RequestSHA256:  "rollback-request",
			Payload:        model.MobileNotePayload{Title: &title},
		}
		sentinel := errors.New("force rollback")
		err := store.Transact(ctx, func(tx storage.Store) error {
			txRepository, err := storage.MobileSyncRepositoryFrom(tx)
			if err != nil {
				return err
			}
			if _, err := txRepository.ApplyNoteMutation(ctx, mutation); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("transaction error = %v, want sentinel", err)
		}
		if _, err := repository.GetNoteByClientID(ctx, mutation.EntityClientID); !errors.Is(err, storage.ErrMobileEntityNotFound) {
			t.Fatalf("get rolled-back note error = %v, want ErrMobileEntityNotFound", err)
		}
		changes, err := repository.ListPendingChanges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) != 0 {
			t.Fatalf("rollback left changes: %+v", changes)
		}
		if _, err := repository.ApplyNoteMutation(ctx, mutation); err != nil {
			t.Fatalf("receipt survived rollback and blocked retry: %v", err)
		}
	})

	t.Run("MSYNC-STORE-003_RevisionConflictAndTombstone", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		title := "Revision one"
		create := model.MobileNoteMutation{
			MutationID:     "55555555-5555-4555-8555-555555555555",
			DeviceClientID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			EntityClientID: "66666666-6666-4666-8666-666666666666",
			Operation:      model.MobileOperationNoteCreate,
			RequestSHA256:  "create-request",
			Payload:        model.MobileNotePayload{Title: &title},
		}
		if _, err := repository.ApplyNoteMutation(ctx, create); err != nil {
			t.Fatal(err)
		}
		baseOne := int64(1)
		updatedTitle := "Revision two"
		update := model.MobileNoteMutation{
			MutationID:     "77777777-7777-4777-8777-777777777777",
			DeviceClientID: create.DeviceClientID,
			EntityClientID: create.EntityClientID,
			Operation:      model.MobileOperationNoteUpdate,
			BaseRevision:   &baseOne,
			RequestSHA256:  "update-request",
			Payload:        model.MobileNotePayload{Title: &updatedTitle},
		}
		updated, err := repository.ApplyNoteMutation(ctx, update)
		if err != nil || updated.Entity == nil || updated.Entity.Revision != 2 {
			t.Fatalf("update result=%+v err=%v", updated, err)
		}
		stale := update
		stale.MutationID = "88888888-8888-4888-8888-888888888888"
		stale.RequestSHA256 = "stale-request"
		if _, err := repository.ApplyNoteMutation(ctx, stale); !errors.Is(err, storage.ErrRevisionConflict) {
			t.Fatalf("stale update error = %v, want ErrRevisionConflict", err)
		}
		baseTwo := int64(2)
		deleted, err := repository.ApplyNoteMutation(ctx, model.MobileNoteMutation{
			MutationID:     "99999999-9999-4999-8999-999999999999",
			DeviceClientID: create.DeviceClientID,
			EntityClientID: create.EntityClientID,
			Operation:      model.MobileOperationNoteDelete,
			BaseRevision:   &baseTwo,
			RequestSHA256:  "delete-request",
		})
		if err != nil || deleted.Entity == nil || deleted.Entity.Revision != 3 || deleted.Entity.DeletedAt == nil {
			t.Fatalf("delete result=%+v err=%v", deleted, err)
		}
		loaded, err := repository.GetNoteByClientID(ctx, create.EntityClientID)
		if err != nil || loaded.DeletedAt == nil || loaded.Revision != 3 {
			t.Fatalf("tombstone=%+v err=%v", loaded, err)
		}
		if _, err := store.Notes().GetByID(ctx, loaded.ID); err == nil {
			t.Fatal("tombstoned note remained visible through legacy note reads")
		}
		searchResults, searchTotal, err := store.Search().Search(ctx, updatedTitle, 1, 20)
		if err != nil {
			t.Fatalf("search tombstoned note: %v", err)
		}
		if searchTotal != 0 || len(searchResults) != 0 {
			t.Fatalf("tombstoned note remained searchable: total=%d results=%+v", searchTotal, searchResults)
		}
		changes, err := repository.ListPendingChanges(ctx)
		if err != nil || len(changes) != 3 {
			t.Fatalf("changes=%+v err=%v", changes, err)
		}
	})

	t.Run("MSYNC-STORE-004_ConcurrentReplayPersistsOneResult", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		title := "Concurrent offline note"
		mutation := model.MobileNoteMutation{
			MutationID:     "77777777-7777-4777-8777-777777777777",
			DeviceClientID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			EntityClientID: "88888888-8888-4888-8888-888888888888",
			Operation:      model.MobileOperationNoteCreate,
			RequestSHA256:  "concurrent-request",
			Payload:        model.MobileNotePayload{Title: &title},
		}

		type outcome struct {
			result *model.MobileMutationResult
			err    error
		}
		start := make(chan struct{})
		outcomes := make(chan outcome, 2)
		var ready sync.WaitGroup
		ready.Add(2)
		for range 2 {
			go func() {
				ready.Done()
				<-start
				result, err := repository.ApplyNoteMutation(ctx, mutation)
				outcomes <- outcome{result: result, err: err}
			}()
		}
		ready.Wait()
		close(start)
		first := <-outcomes
		second := <-outcomes
		for index, got := range []outcome{first, second} {
			if got.err != nil {
				t.Fatalf("concurrent apply %d: %v", index+1, got.err)
			}
		}
		if !reflect.DeepEqual(first.result, second.result) {
			t.Fatalf("concurrent results differ: first=%+v second=%+v", first.result, second.result)
		}
		changes, err := repository.ListPendingChanges(ctx)
		if err != nil {
			t.Fatalf("list concurrent changes: %v", err)
		}
		if len(changes) != 1 || changes[0].MutationID != mutation.MutationID {
			t.Fatalf("changes = %+v, want exactly one concurrent create", changes)
		}
	})

	t.Run("MSYNC-ADAPTER-001_LegacyCRUDWritesRevisionTombstoneAndOutbox", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		created, err := store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title: "Legacy create", Body: "Legacy body", FolderID: "__uncategorized", Tags: "[]",
		})
		if err != nil {
			t.Fatalf("legacy create: %v", err)
		}
		changes, err := repository.ListPendingChanges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) != 1 || changes[0].Operation != "note.server_created" || changes[0].Entity.Revision != 1 || changes[0].Entity.ClientID == "" {
			t.Fatalf("legacy create changes = %+v", changes)
		}
		clientID := changes[0].Entity.ClientID
		updatedTitle := "Legacy update"
		if _, err := store.Notes().Update(ctx, created.ID, &model.UpdateNoteRequest{Title: &updatedTitle}); err != nil {
			t.Fatalf("legacy update: %v", err)
		}
		changes, err = repository.ListPendingChanges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) != 2 || changes[1].Operation != "note.server_updated" || changes[1].Entity.Revision != 2 || changes[1].Entity.ClientID != clientID {
			t.Fatalf("legacy update changes = %+v", changes)
		}
		if err := store.Notes().Delete(ctx, created.ID); err != nil {
			t.Fatalf("legacy delete: %v", err)
		}
		changes, err = repository.ListPendingChanges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) != 3 || changes[2].Operation != "note.server_deleted" || changes[2].Entity.Revision != 3 || changes[2].Entity.DeletedAt == nil {
			t.Fatalf("legacy delete changes = %+v", changes)
		}
		if _, err := store.Notes().GetByID(ctx, created.ID); err == nil {
			t.Fatal("legacy delete remained visible")
		}
		tombstone, err := repository.GetNoteByClientID(ctx, clientID)
		if err != nil || tombstone.DeletedAt == nil || tombstone.Revision != 3 {
			t.Fatalf("legacy tombstone = %+v err=%v", tombstone, err)
		}
	})

	t.Run("MSYNC-READ-001_CommittedCursorPagesWithoutDuplicatesAndExpiresExplicitly", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		for index, clientID := range []string{
			"10101010-1010-4010-8010-101010101010",
			"20202020-2020-4020-8020-202020202020",
		} {
			title := "Cursor note " + string(rune('A'+index))
			if _, err := repository.ApplyNoteMutation(ctx, model.MobileNoteMutation{
				MutationID: uuid.NewString(), DeviceClientID: "30303030-3030-4030-8030-303030303030",
				EntityClientID: clientID, Operation: model.MobileOperationNoteCreate,
				RequestSHA256: "cursor-create-" + clientID, Payload: model.MobileNotePayload{Title: &title},
			}); err != nil {
				t.Fatal(err)
			}
		}
		published, err := repository.PublishPendingChanges(ctx, 100, time.Now().UTC().Unix())
		if err != nil || published != 2 {
			t.Fatalf("publish changes count=%d err=%v", published, err)
		}
		first, err := repository.ReadCommittedChanges(ctx, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(first.Changes) != 1 || !first.HasMore || first.NextPosition <= 0 {
			t.Fatalf("first change page = %+v", first)
		}
		second, err := repository.ReadCommittedChanges(ctx, first.NextPosition, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(second.Changes) != 1 || second.HasMore || second.NextPosition <= first.NextPosition {
			t.Fatalf("second change page = %+v", second)
		}
		if first.Changes[0].Entity.ClientID == second.Changes[0].Entity.ClientID {
			t.Fatalf("duplicate cursor pages: first=%+v second=%+v", first, second)
		}
		if err := repository.PruneCommittedChanges(ctx, first.NextPosition); err != nil {
			t.Fatal(err)
		}
		if _, err := repository.ReadCommittedChanges(ctx, 0, 10); !errors.Is(err, storage.ErrMobileCursorExpired) {
			t.Fatalf("expired cursor error = %v, want ErrMobileCursorExpired", err)
		}
		if _, err := repository.ReadCommittedChanges(ctx, first.NextPosition, 10); err != nil {
			t.Fatalf("boundary cursor should remain valid: %v", err)
		}
	})

	t.Run("MSYNC-PUBLISH-001_GlobalPublisherPublishesCommittedRowsAndPrunesByRetention", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		clientID := "31313131-3131-4131-8131-313131313131"
		title := "Background publisher"
		if _, err := repository.ApplyNoteMutation(ctx, model.MobileNoteMutation{
			MutationID: uuid.NewString(), DeviceClientID: "32323232-3232-4232-8232-323232323232",
			EntityClientID: clientID, Operation: model.MobileOperationNoteCreate,
			RequestSHA256: "background-publisher-create", Payload: model.MobileNotePayload{Title: &title},
		}); err != nil {
			t.Fatal(err)
		}
		before, err := repository.ReadCommittedChanges(ctx, 0, 10)
		if err != nil || len(before.Changes) != 0 {
			t.Fatalf("changes before publisher=%+v err=%v", before, err)
		}
		publisher, err := storage.MobileSyncPublisherRepositoryFrom(store)
		if err != nil {
			t.Fatal(err)
		}
		publishedAt := int64(1800000000)
		published, err := publisher.PublishNextWorkspace(context.Background(), 100, publishedAt)
		if err != nil || published != 1 {
			t.Fatalf("published=%d err=%v", published, err)
		}
		after, err := repository.ReadCommittedChanges(ctx, 0, 10)
		if err != nil || len(after.Changes) != 1 || after.Changes[0].Entity.ClientID != clientID {
			t.Fatalf("changes after publisher=%+v err=%v", after, err)
		}
		pruned, err := publisher.PruneExpired(context.Background(), publishedAt+1)
		if err != nil || pruned != 1 {
			t.Fatalf("pruned=%d err=%v", pruned, err)
		}
		if _, err := repository.ReadCommittedChanges(ctx, 0, 10); !errors.Is(err, storage.ErrMobileCursorExpired) {
			t.Fatalf("expired cursor error=%v", err)
		}
	})

	t.Run("MSYNC-SNAPSHOT-001_SessionIsImmutableAndReturnsBoundaryCursor", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		deviceID := "41414141-4141-4141-8141-414141414141"
		clientIDs := []string{
			"51515151-5151-4151-8151-515151515151",
			"61616161-6161-4161-8161-616161616161",
		}
		for index, clientID := range clientIDs {
			title := "Snapshot note " + string(rune('A'+index))
			if _, err := repository.ApplyNoteMutation(ctx, model.MobileNoteMutation{
				MutationID: uuid.NewString(), DeviceClientID: deviceID, EntityClientID: clientID,
				Operation: model.MobileOperationNoteCreate, RequestSHA256: "snapshot-create-" + clientID,
				Payload: model.MobileNotePayload{Title: &title},
			}); err != nil {
				t.Fatal(err)
			}
		}
		now := time.Now().UTC()
		if _, err := repository.PublishPendingChanges(ctx, 100, now.Unix()); err != nil {
			t.Fatal(err)
		}
		snapshot, err := repository.BeginSnapshot(ctx, model.BeginMobileSnapshot{
			SessionID: uuid.NewString(), Scope: "iphone", Now: now.Unix(), ExpiresAt: now.Add(15 * time.Minute).Unix(),
		})
		if err != nil {
			t.Fatal(err)
		}
		first, err := repository.ReadSnapshot(ctx, model.ReadMobileSnapshot{
			SessionID: snapshot.SessionID, Offset: 0, Limit: 1, Now: now.Unix(),
		})
		if err != nil || len(first.Entities) != 1 || !first.HasMore || first.NextOffset != 1 {
			t.Fatalf("first snapshot page=%+v err=%v", first, err)
		}
		base := int64(1)
		updatedTitle := "Changed after snapshot"
		if _, err := repository.ApplyNoteMutation(ctx, model.MobileNoteMutation{
			MutationID: uuid.NewString(), DeviceClientID: deviceID, EntityClientID: clientIDs[1],
			Operation: model.MobileOperationNoteUpdate, BaseRevision: &base, RequestSHA256: "snapshot-update",
			Payload: model.MobileNotePayload{Title: &updatedTitle},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := repository.PublishPendingChanges(ctx, 100, now.Add(time.Second).Unix()); err != nil {
			t.Fatal(err)
		}
		second, err := repository.ReadSnapshot(ctx, model.ReadMobileSnapshot{
			SessionID: snapshot.SessionID, Offset: first.NextOffset, Limit: 10, Now: now.Add(time.Second).Unix(),
		})
		if err != nil || len(second.Entities) != 1 || second.HasMore || second.BoundaryPosition != snapshot.BoundaryPosition {
			t.Fatalf("second snapshot page=%+v err=%v", second, err)
		}
		var frozenPayload map[string]string
		if err := json.Unmarshal(second.Entities[0].Payload, &frozenPayload); err != nil {
			t.Fatal(err)
		}
		if frozenPayload["title"] == updatedTitle {
			t.Fatalf("snapshot observed post-boundary update: %+v", frozenPayload)
		}
		changes, err := repository.ReadCommittedChanges(ctx, snapshot.BoundaryPosition, 10)
		if err != nil || len(changes.Changes) != 1 || changes.Changes[0].Entity.Revision != 2 {
			t.Fatalf("post-snapshot changes=%+v err=%v", changes, err)
		}
		if _, err := repository.ReadSnapshot(ctx, model.ReadMobileSnapshot{
			SessionID: snapshot.SessionID, Offset: 0, Limit: 10, Now: snapshot.ExpiresAt + 1,
		}); !errors.Is(err, storage.ErrMobileSnapshotExpired) {
			t.Fatalf("expired snapshot error = %v", err)
		}
	})

	t.Run("MSYNC-SNAPSHOT-002_IncludesEveryImplementedMobileEntityType", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		deviceID := "81818181-8181-4181-8181-818181818181"
		title := "Snapshot note"
		if _, err := repository.ApplyNoteMutation(ctx, model.MobileNoteMutation{
			MutationID: uuid.NewString(), DeviceClientID: deviceID,
			EntityClientID: "82828282-8282-4282-8282-828282828282", Operation: model.MobileOperationNoteCreate,
			RequestSHA256: "snapshot-note", Payload: model.MobileNotePayload{Title: &title},
		}); err != nil {
			t.Fatal(err)
		}
		baseOne := int64(1)
		remoteTitle := "Snapshot note remote"
		if _, err := repository.ApplyNoteMutation(ctx, model.MobileNoteMutation{
			MutationID: uuid.NewString(), DeviceClientID: deviceID,
			EntityClientID: "82828282-8282-4282-8282-828282828282", Operation: model.MobileOperationNoteUpdate,
			BaseRevision: &baseOne, RequestSHA256: "snapshot-note-remote", Payload: model.MobileNotePayload{Title: &remoteTitle},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := repository.CreateConflict(ctx, model.CreateMobileSyncConflict{
			ConflictID: uuid.NewString(), MutationID: uuid.NewString(), DeviceClientID: deviceID,
			RequestSHA256: "snapshot-conflict", EntityType: "note",
			EntityClientID: "82828282-8282-4282-8282-828282828282", Operation: model.MobileOperationNoteUpdate,
			BaseRevision: 1, LocalPayload: json.RawMessage(`{"title":"Snapshot local draft"}`),
		}); err != nil {
			t.Fatal(err)
		}
		for _, mutation := range []model.MobileEntityMutation{
			{
				MutationID: uuid.NewString(), DeviceClientID: deviceID, EntityType: "task",
				EntityClientID: "83838383-8383-4383-8383-838383838383", Operation: "task.create",
				RequestSHA256: "snapshot-task", Payload: json.RawMessage(`{"title":"Snapshot task"}`),
			},
			{
				MutationID: uuid.NewString(), DeviceClientID: deviceID, EntityType: "event",
				EntityClientID: "84848484-8484-4484-8484-848484848484", Operation: "event.create",
				RequestSHA256: "snapshot-event", Payload: json.RawMessage(`{"title":"Snapshot event","start_time":1800000000,"end_time":1800003600}`),
			},
			{
				MutationID: uuid.NewString(), DeviceClientID: deviceID, EntityType: "inbox",
				EntityClientID: "85858585-8585-4585-8585-858585858585", Operation: "inbox.create",
				RequestSHA256: "snapshot-inbox", Payload: json.RawMessage(`{"kind":"note","title":"Snapshot inbox"}`),
			},
		} {
			if _, err := repository.ApplyEntityMutation(ctx, mutation); err != nil {
				t.Fatal(err)
			}
		}
		now := time.Now().UTC()
		snapshot, err := repository.BeginSnapshot(ctx, model.BeginMobileSnapshot{
			SessionID: uuid.NewString(), Scope: "iphone", Now: now.Unix(), ExpiresAt: now.Add(15 * time.Minute).Unix(),
		})
		if err != nil {
			t.Fatal(err)
		}
		page, err := repository.ReadSnapshot(ctx, model.ReadMobileSnapshot{
			SessionID: snapshot.SessionID, Offset: 0, Limit: 100, Now: now.Unix(),
		})
		if err != nil {
			t.Fatal(err)
		}
		gotTypes := make(map[string]int)
		for _, entity := range page.Entities {
			gotTypes[entity.EntityType]++
		}
		for _, entityType := range []string{"note", "task", "event", "inbox", "sync_conflict"} {
			if gotTypes[entityType] != 1 {
				t.Fatalf("snapshot entity type counts=%v, want one %s", gotTypes, entityType)
			}
		}
	})

	for _, testCase := range []struct {
		name          string
		entityType    string
		clientID      string
		createPayload json.RawMessage
		updatePayload json.RawMessage
	}{
		{
			name: "task", entityType: "task", clientID: "a3a3a3a3-a3a3-43a3-83a3-a3a3a3a3a3a3",
			createPayload: json.RawMessage(`{"title":"Offline task","content":"Captured offline","priority":2}`),
			updatePayload: json.RawMessage(`{"done":1}`),
		},
		{
			name: "event", entityType: "event", clientID: "b4b4b4b4-b4b4-44b4-84b4-b4b4b4b4b4b4",
			createPayload: json.RawMessage(`{"title":"Offline event","start_time":1800000000,"end_time":1800003600,"kind":"work"}`),
			updatePayload: json.RawMessage(`{"location":"Room 2"}`),
		},
		{
			name: "inbox", entityType: "inbox", clientID: "c5c5c5c5-c5c5-45c5-85c5-c5c5c5c5c5c5",
			createPayload: json.RawMessage(`{"kind":"note","title":"Offline inbox","body":"Captured offline"}`),
			updatePayload: json.RawMessage(`{"archived":1}`),
		},
	} {
		t.Run("MSYNC-ENTITY-001_"+testCase.name+"_CreateCASDelete", func(t *testing.T) {
			store := factory(t)
			defer store.Close()
			ctx := scopedContractContext(t, store)
			repository := mobileSyncRepository(t, store)
			deviceID := "d6d6d6d6-d6d6-46d6-86d6-d6d6d6d6d6d6"
			create, err := repository.ApplyEntityMutation(ctx, model.MobileEntityMutation{
				MutationID: uuid.NewString(), DeviceClientID: deviceID, EntityType: testCase.entityType,
				EntityClientID: testCase.clientID, Operation: testCase.entityType + ".create",
				RequestSHA256: "create-" + testCase.name, Payload: testCase.createPayload,
			})
			if err != nil || create.Entity == nil || create.Entity.Revision != 1 || create.Entity.EntityType != testCase.entityType {
				t.Fatalf("create result=%+v err=%v", create, err)
			}
			staleBase := int64(0)
			if _, err := repository.ApplyEntityMutation(ctx, model.MobileEntityMutation{
				MutationID: uuid.NewString(), DeviceClientID: deviceID, EntityType: testCase.entityType,
				EntityClientID: testCase.clientID, Operation: testCase.entityType + ".update",
				BaseRevision: &staleBase, RequestSHA256: "stale-" + testCase.name, Payload: testCase.updatePayload,
			}); !errors.Is(err, storage.ErrRevisionConflict) {
				t.Fatalf("stale update error=%v", err)
			}
			baseOne := int64(1)
			updated, err := repository.ApplyEntityMutation(ctx, model.MobileEntityMutation{
				MutationID: uuid.NewString(), DeviceClientID: deviceID, EntityType: testCase.entityType,
				EntityClientID: testCase.clientID, Operation: testCase.entityType + ".update",
				BaseRevision: &baseOne, RequestSHA256: "update-" + testCase.name, Payload: testCase.updatePayload,
			})
			if err != nil || updated.Entity == nil || updated.Entity.Revision != 2 {
				t.Fatalf("update result=%+v err=%v", updated, err)
			}
			baseTwo := int64(2)
			deleted, err := repository.ApplyEntityMutation(ctx, model.MobileEntityMutation{
				MutationID: uuid.NewString(), DeviceClientID: deviceID, EntityType: testCase.entityType,
				EntityClientID: testCase.clientID, Operation: testCase.entityType + ".delete",
				BaseRevision: &baseTwo, RequestSHA256: "delete-" + testCase.name, Payload: json.RawMessage(`{}`),
			})
			if err != nil || deleted.Entity == nil || deleted.Entity.Revision != 3 || deleted.Entity.DeletedAt == nil {
				t.Fatalf("delete result=%+v err=%v", deleted, err)
			}
			loaded, err := repository.GetEntityByClientID(ctx, testCase.entityType, testCase.clientID)
			if err != nil || loaded.Revision != 3 || loaded.DeletedAt == nil {
				t.Fatalf("loaded tombstone=%+v err=%v", loaded, err)
			}
			changes, err := repository.ListPendingChanges(ctx)
			if err != nil || len(changes) != 3 {
				t.Fatalf("changes=%+v err=%v", changes, err)
			}
		})
	}

	t.Run("MSYNC-OCCURRENCE-001_DatesHaveIndependentRevisionAndReplay", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		projectID := "personal"
		task := &model.Task{Title: "Recurring mobile task", ProjectID: &projectID, ExecutionType: "recurring"}
		if err := store.Tasks().Create(ctx, task); err != nil {
			t.Fatal(err)
		}
		deviceID := "91919191-9191-4191-8191-919191919191"
		complete := func(mutationID, occurrenceID, date string) *model.MobileMutationResult {
			t.Helper()
			result, err := repository.ApplyEntityMutation(ctx, model.MobileEntityMutation{
				MutationID: mutationID, DeviceClientID: deviceID, EntityType: "task_occurrence",
				EntityClientID: occurrenceID, Operation: "task_occurrence.complete", RequestSHA256: "complete-" + date,
				Payload: json.RawMessage(fmt.Sprintf(`{"task_id":%q,"occurrence_date":%q,"completed_at":1800000000}`, task.ID, date)),
			})
			if err != nil {
				t.Fatal(err)
			}
			return result
		}
		firstMutationID := uuid.NewString()
		firstOccurrenceID := "92929292-9292-4292-8292-929292929292"
		first := complete(firstMutationID, firstOccurrenceID, "2027-01-01")
		second := complete(uuid.NewString(), "93939393-9393-4393-8393-939393939393", "2027-01-02")
		if first.Entity == nil || second.Entity == nil || first.Entity.Revision != 1 || second.Entity.Revision != 1 {
			t.Fatalf("first=%+v second=%+v", first, second)
		}
		replayed := complete(firstMutationID, firstOccurrenceID, "2027-01-01")
		if !reflect.DeepEqual(replayed, first) {
			t.Fatalf("replayed=%+v first=%+v", replayed, first)
		}
		baseOne := int64(1)
		reopened, err := repository.ApplyEntityMutation(ctx, model.MobileEntityMutation{
			MutationID: uuid.NewString(), DeviceClientID: deviceID, EntityType: "task_occurrence",
			EntityClientID: firstOccurrenceID, Operation: "task_occurrence.reopen", BaseRevision: &baseOne,
			RequestSHA256: "reopen-2027-01-01", Payload: json.RawMessage(`{}`),
		})
		if err != nil || reopened.Entity == nil || reopened.Entity.Revision != 2 {
			t.Fatalf("reopened=%+v err=%v", reopened, err)
		}
		unchanged, err := repository.GetEntityByClientID(ctx, "task_occurrence", second.Entity.ClientID)
		if err != nil || unchanged.Revision != 1 {
			t.Fatalf("second occurrence changed=%+v err=%v", unchanged, err)
		}
		now := time.Now().UTC()
		snapshot, err := repository.BeginSnapshot(ctx, model.BeginMobileSnapshot{
			SessionID: uuid.NewString(), Scope: "iphone", Now: now.Unix(), ExpiresAt: now.Add(15 * time.Minute).Unix(),
		})
		if err != nil {
			t.Fatal(err)
		}
		page, err := repository.ReadSnapshot(ctx, model.ReadMobileSnapshot{SessionID: snapshot.SessionID, Limit: 100, Now: now.Unix()})
		if err != nil {
			t.Fatal(err)
		}
		occurrenceCount := 0
		for _, entity := range page.Entities {
			if entity.EntityType == "task_occurrence" {
				occurrenceCount++
			}
		}
		if occurrenceCount != 2 {
			t.Fatalf("snapshot occurrence count=%d entities=%+v", occurrenceCount, page.Entities)
		}
	})

	t.Run("MSYNC-ADAPTER-003_LegacyOccurrenceWritesRevisionAndOutbox", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		projectID := "personal"
		task := &model.Task{Title: "Legacy recurring task", ProjectID: &projectID, ExecutionType: "recurring"}
		if err := store.Tasks().Create(ctx, task); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Recurrence().CompleteOccurrence(ctx, task.ID, "2027-02-01", 1800000000); err != nil {
			t.Fatal(err)
		}
		changes, err := repository.ListPendingChanges(ctx)
		if err != nil || len(changes) != 2 || changes[1].Entity.EntityType != "task_occurrence" || changes[1].Entity.Revision != 1 {
			t.Fatalf("complete changes=%+v err=%v", changes, err)
		}
		occurrenceID := changes[1].Entity.ClientID
		if _, err := store.Recurrence().ReopenOccurrence(ctx, task.ID, "2027-02-01"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Recurrence().SkipOccurrence(ctx, task.ID, "2027-02-01"); err != nil {
			t.Fatal(err)
		}
		changes, err = repository.ListPendingChanges(ctx)
		if err != nil || len(changes) != 4 || changes[2].Entity.Revision != 2 || changes[3].Entity.Revision != 3 {
			t.Fatalf("legacy occurrence changes=%+v err=%v", changes, err)
		}
		loaded, err := repository.GetEntityByClientID(ctx, "task_occurrence", occurrenceID)
		if err != nil || loaded.Revision != 3 {
			t.Fatalf("loaded occurrence=%+v err=%v", loaded, err)
		}
	})

	t.Run("MSYNC-VOICE-001_CreateIsIdempotentAndIncludedInSnapshot", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		mutation := model.MobileEntityMutation{
			MutationID:     "b1b1b1b1-b1b1-41b1-81b1-b1b1b1b1b1b1",
			DeviceClientID: "b2b2b2b2-b2b2-42b2-82b2-b2b2b2b2b2b2", EntityType: "voice_note",
			EntityClientID: "b3b3b3b3-b3b3-43b3-83b3-b3b3b3b3b3b3", Operation: "voice.create",
			RequestSHA256: "voice-create", Payload: json.RawMessage(`{"title":"Offline voice","duration_ms":1200,"recorded_at":1800000000,"language":"zh"}`),
		}
		created, err := repository.ApplyEntityMutation(ctx, mutation)
		if err != nil || created.Entity == nil || created.Entity.EntityType != "voice_note" || created.Entity.Revision != 1 {
			t.Fatalf("created=%+v err=%v", created, err)
		}
		replayed, err := repository.ApplyEntityMutation(ctx, mutation)
		if err != nil || !reflect.DeepEqual(replayed, created) {
			t.Fatalf("replayed=%+v err=%v", replayed, err)
		}
		nativeStore, err := storage.NativeStoreFrom(store)
		if err != nil {
			t.Fatal(err)
		}
		voice, err := nativeStore.VoiceNotes().GetByClientID(ctx, mutation.EntityClientID)
		if err != nil || voice.Title != "Offline voice" || voice.UploadState != model.VoiceUploadPending {
			t.Fatalf("voice=%+v err=%v", voice, err)
		}
		now := time.Now().UTC()
		snapshot, err := repository.BeginSnapshot(ctx, model.BeginMobileSnapshot{
			SessionID: uuid.NewString(), Scope: "watch", Now: now.Unix(), ExpiresAt: now.Add(15 * time.Minute).Unix(),
		})
		if err != nil {
			t.Fatal(err)
		}
		page, err := repository.ReadSnapshot(ctx, model.ReadMobileSnapshot{SessionID: snapshot.SessionID, Limit: 100, Now: now.Unix()})
		if err != nil {
			t.Fatal(err)
		}
		voiceCount := 0
		for _, entity := range page.Entities {
			if entity.EntityType == "voice_note" && entity.ClientID == mutation.EntityClientID {
				voiceCount++
			}
		}
		if voiceCount != 1 {
			t.Fatalf("snapshot voice count=%d entities=%+v", voiceCount, page.Entities)
		}
	})

	t.Run("MSYNC-VOICE-002_AudioAndTranscriptionStateBumpRevisionAndOutbox", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		clientID := "b4b4b4b4-b4b4-44b4-84b4-b4b4b4b4b4b4"
		if _, err := repository.ApplyEntityMutation(ctx, model.MobileEntityMutation{
			MutationID: uuid.NewString(), DeviceClientID: "b5b5b5b5-b5b5-45b5-85b5-b5b5b5b5b5b5",
			EntityType: "voice_note", EntityClientID: clientID, Operation: "voice.create", RequestSHA256: "voice-state-create",
			Payload: json.RawMessage(`{"title":"Voice state","recorded_at":1800000000}`),
		}); err != nil {
			t.Fatal(err)
		}
		nativeStore, err := storage.NativeStoreFrom(store)
		if err != nil {
			t.Fatal(err)
		}
		checksum := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		if _, err := nativeStore.VoiceNotes().ClaimUpload(ctx, clientID, model.VoiceUploadClaim{
			ObjectKey: "voice/test.m4a", MimeType: "audio/mp4", Size: 8, SHA256: checksum,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := nativeStore.VoiceNotes().MarkUploaded(ctx, clientID, checksum); err != nil {
			t.Fatal(err)
		}
		if _, err := nativeStore.VoiceNotes().SetTranscriptionState(ctx, clientID, model.TranscriptionProcessing, ""); err != nil {
			t.Fatal(err)
		}
		entity, err := repository.GetEntityByClientID(ctx, "voice_note", clientID)
		if err != nil || entity.Revision != 4 {
			t.Fatalf("voice entity=%+v err=%v", entity, err)
		}
		changes, err := repository.ListPendingChanges(ctx)
		if err != nil || len(changes) != 5 {
			t.Fatalf("voice changes=%+v err=%v", changes, err)
		}
		for index, wantRevision := range []int64{2, 3, 4} {
			change := changes[index+2]
			if change.Entity.EntityType != "voice_note" || change.Entity.Revision != wantRevision {
				t.Fatalf("state change[%d]=%+v want revision %d", index, change, wantRevision)
			}
		}
	})

	t.Run("MSYNC-VOICE-003_AudioDeleteAndVoiceDeleteHaveDistinctDeleteWinsSemantics", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		deviceID := "b6b6b6b6-b6b6-46b6-86b6-b6b6b6b6b6b6"
		clientID := "b7b7b7b7-b7b7-47b7-87b7-b7b7b7b7b7b7"
		created, err := repository.ApplyEntityMutation(ctx, model.MobileEntityMutation{
			MutationID: uuid.NewString(), DeviceClientID: deviceID,
			EntityType: "voice_note", EntityClientID: clientID, Operation: "voice.create", RequestSHA256: "voice-delete-create",
			Payload: json.RawMessage(`{"title":"Keep the transcript","recorded_at":1800000000}`),
		})
		if err != nil || created.Entity == nil || created.Entity.Revision != 1 {
			t.Fatalf("create voice=%+v err=%v", created, err)
		}
		var createdPayload map[string]any
		if err := json.Unmarshal(created.Entity.Payload, &createdPayload); err != nil {
			t.Fatal(err)
		}
		noteID, _ := createdPayload["note_id"].(string)
		if noteID == "" {
			t.Fatalf("created payload=%+v", createdPayload)
		}

		jobs := transcriptionJobRepository(t, store)
		job, err := jobs.CreateOrGet(ctx, model.CreateTranscriptionJob{
			JobID: uuid.NewString(), MutationID: uuid.NewString(), RequestSHA256: "voice-delete-job",
			VoiceNoteID: clientID, Now: 1800000001,
		})
		if err != nil || job.State != model.TranscriptionJobWaitingForAudio {
			t.Fatalf("create transcription job=%+v err=%v", job, err)
		}

		baseOne := int64(1)
		audioDeleteMutation := model.MobileEntityMutation{
			MutationID: uuid.NewString(), DeviceClientID: deviceID,
			EntityType: "voice_note", EntityClientID: clientID, Operation: "voice_audio.delete", BaseRevision: &baseOne,
			RequestSHA256: "voice-audio-delete", Payload: json.RawMessage(`{}`),
		}
		audioDeleted, err := repository.ApplyEntityMutation(ctx, audioDeleteMutation)
		if err != nil || audioDeleted.Entity == nil || audioDeleted.Entity.Revision != 2 || audioDeleted.Entity.DeletedAt != nil {
			t.Fatalf("audio delete=%+v err=%v", audioDeleted, err)
		}
		var audioDeletedPayload map[string]any
		if err := json.Unmarshal(audioDeleted.Entity.Payload, &audioDeletedPayload); err != nil {
			t.Fatal(err)
		}
		if audioDeletedPayload["audio_state"] != model.VoiceAudioDeleted {
			t.Fatalf("audio delete payload=%+v", audioDeletedPayload)
		}
		if _, err := store.Notes().GetByID(ctx, noteID); err != nil {
			t.Fatalf("audio delete removed logical note: %v", err)
		}
		job, err = jobs.Get(ctx, job.JobID)
		if err != nil || job.State != model.TranscriptionJobCanceled {
			t.Fatalf("job after audio delete=%+v err=%v", job, err)
		}
		nativeStore, err := storage.NativeStoreFrom(store)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := nativeStore.VoiceNotes().ClaimUpload(ctx, clientID, model.VoiceUploadClaim{
			ObjectKey: "voice/late.m4a", MimeType: "audio/mp4", Size: 4, SHA256: strings.Repeat("a", 64),
		}); !errors.Is(err, storage.ErrVoiceAudioGone) {
			t.Fatalf("late upload error=%v, want ErrVoiceAudioGone", err)
		}
		if _, err := jobs.CreateOrGet(ctx, model.CreateTranscriptionJob{
			JobID: uuid.NewString(), MutationID: uuid.NewString(), RequestSHA256: "voice-delete-late-job",
			VoiceNoteID: clientID, Now: 1800000002,
		}); !errors.Is(err, storage.ErrVoiceAudioGone) {
			t.Fatalf("late transcription job error=%v, want ErrVoiceAudioGone", err)
		}

		baseTwo := int64(2)
		voiceDeleteMutation := model.MobileEntityMutation{
			MutationID: uuid.NewString(), DeviceClientID: deviceID,
			EntityType: "voice_note", EntityClientID: clientID, Operation: "voice_note.delete", BaseRevision: &baseTwo,
			RequestSHA256: "voice-note-delete", Payload: json.RawMessage(`{}`),
		}
		voiceDeleted, err := repository.ApplyEntityMutation(ctx, voiceDeleteMutation)
		if err != nil || voiceDeleted.Entity == nil || voiceDeleted.Entity.Revision != 3 || voiceDeleted.Entity.DeletedAt == nil {
			t.Fatalf("voice delete=%+v err=%v", voiceDeleted, err)
		}
		replayed, err := repository.ApplyEntityMutation(ctx, voiceDeleteMutation)
		if err != nil || !reflect.DeepEqual(replayed, voiceDeleted) {
			t.Fatalf("voice delete replay=%+v err=%v, want %+v", replayed, err, voiceDeleted)
		}
		if _, err := store.Notes().GetByID(ctx, noteID); err == nil {
			t.Fatal("voice_note.delete left the logical note visible")
		}
		if _, err := repository.ApplyEntityMutation(ctx, model.MobileEntityMutation{
			MutationID: uuid.NewString(), DeviceClientID: deviceID,
			EntityType: "voice_note", EntityClientID: clientID, Operation: "voice.create", RequestSHA256: "voice-delete-recreate",
			Payload: json.RawMessage(`{"title":"must stay gone"}`),
		}); !errors.Is(err, storage.ErrMobileEntityGone) {
			t.Fatalf("recreate retired voice error=%v", err)
		}

		unknownID := "b8b8b8b8-b8b8-48b8-88b8-b8b8b8b8b8b8"
		baseZero := int64(0)
		pretombstone, err := repository.ApplyEntityMutation(ctx, model.MobileEntityMutation{
			MutationID: uuid.NewString(), DeviceClientID: deviceID,
			EntityType: "voice_note", EntityClientID: unknownID, Operation: "voice_note.delete", BaseRevision: &baseZero,
			RequestSHA256: "voice-delete-unknown", Payload: json.RawMessage(`{}`),
		})
		if err != nil || pretombstone.Entity == nil || pretombstone.Entity.Revision != 1 || pretombstone.Entity.DeletedAt == nil {
			t.Fatalf("unknown delete pretombstone=%+v err=%v", pretombstone, err)
		}
		if _, err := repository.ApplyEntityMutation(ctx, model.MobileEntityMutation{
			MutationID: uuid.NewString(), DeviceClientID: deviceID,
			EntityType: "voice_note", EntityClientID: unknownID, Operation: "voice.create", RequestSHA256: "voice-delete-unknown-create",
			Payload: json.RawMessage(`{"title":"late create"}`),
		}); !errors.Is(err, storage.ErrMobileEntityGone) {
			t.Fatalf("late create after pretombstone error=%v", err)
		}
	})

	t.Run("MSYNC-VOICE-004_UploadedAudioDeleteUsesDurableLeasedCleanup", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		deviceID := "b9b9b9b9-b9b9-49b9-89b9-b9b9b9b9b9b9"
		clientID := "cacacaca-caca-4aca-8aca-cacacacacaca"
		if _, err := repository.ApplyEntityMutation(ctx, model.MobileEntityMutation{
			MutationID: uuid.NewString(), DeviceClientID: deviceID,
			EntityType: "voice_note", EntityClientID: clientID, Operation: "voice.create", RequestSHA256: "voice-cleanup-create",
			Payload: json.RawMessage(`{"title":"Durable cleanup","recorded_at":1800000000}`),
		}); err != nil {
			t.Fatal(err)
		}
		nativeStore, err := storage.NativeStoreFrom(store)
		if err != nil {
			t.Fatal(err)
		}
		checksum := strings.Repeat("b", 64)
		objectKey := "voice/durable-cleanup.m4a"
		if _, err := nativeStore.VoiceNotes().ClaimUpload(ctx, clientID, model.VoiceUploadClaim{
			ObjectKey: objectKey, MimeType: "audio/mp4", Size: 8, SHA256: checksum,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := nativeStore.VoiceNotes().MarkUploaded(ctx, clientID, checksum); err != nil {
			t.Fatal(err)
		}
		baseThree := int64(3)
		deleted, err := repository.ApplyEntityMutation(ctx, model.MobileEntityMutation{
			MutationID: uuid.NewString(), DeviceClientID: deviceID,
			EntityType: "voice_note", EntityClientID: clientID, Operation: "voice_audio.delete", BaseRevision: &baseThree,
			RequestSHA256: "voice-cleanup-delete", Payload: json.RawMessage(`{}`),
		})
		if err != nil || deleted.Entity == nil || deleted.Entity.Revision != 4 {
			t.Fatalf("request cleanup=%+v err=%v", deleted, err)
		}
		var requestedPayload map[string]any
		if err := json.Unmarshal(deleted.Entity.Payload, &requestedPayload); err != nil {
			t.Fatal(err)
		}
		if requestedPayload["audio_state"] != model.VoiceAudioDeleteRequested {
			t.Fatalf("requested payload=%+v", requestedPayload)
		}
		cleanup := voiceAudioCleanupRepository(t, store)
		lease, err := cleanup.ClaimNext(ctx, model.ClaimVoiceAudioCleanupJob{
			WorkerID: "cleanup-worker-a", LeaseToken: "cleanup-lease-a", Now: 1800000010, LeaseExpiresAt: 1800000040,
		})
		if err != nil || lease.Job.VoiceNoteID != clientID || lease.Job.ObjectKey != objectKey || lease.Job.State != model.VoiceAudioCleanupProcessing || lease.Job.Attempt != 1 {
			t.Fatalf("cleanup lease=%+v err=%v", lease, err)
		}
		if _, err := cleanup.ClaimNext(ctx, model.ClaimVoiceAudioCleanupJob{
			WorkerID: "cleanup-worker-b", LeaseToken: "cleanup-lease-b", Now: 1800000020, LeaseExpiresAt: 1800000050,
		}); !errors.Is(err, storage.ErrNoVoiceAudioCleanupJob) {
			t.Fatalf("early second cleanup claim error=%v", err)
		}
		completed, err := cleanup.Complete(ctx, model.CompleteVoiceAudioCleanupJob{
			JobID: lease.Job.JobID, LeaseToken: lease.LeaseToken, Now: 1800000021,
		})
		if err != nil || completed.State != model.VoiceAudioCleanupCompleted {
			t.Fatalf("complete cleanup=%+v err=%v", completed, err)
		}
		voice, err := nativeStore.VoiceNotes().GetByClientID(ctx, clientID)
		if err != nil || voice.Revision != 5 || voice.AudioState != model.VoiceAudioDeleted || voice.AudioRevision != 5 || voice.ObjectKey != "" || voice.AudioSize != 0 || voice.AudioSHA256 != "" {
			t.Fatalf("voice after cleanup=%+v err=%v", voice, err)
		}
		if _, err := cleanup.Complete(ctx, model.CompleteVoiceAudioCleanupJob{
			JobID: lease.Job.JobID, LeaseToken: lease.LeaseToken, Now: 1800000022,
		}); !errors.Is(err, storage.ErrVoiceAudioCleanupLeaseLost) {
			t.Fatalf("stale cleanup completion error=%v", err)
		}
	})

	for _, testCase := range []struct {
		name       string
		entityType string
		create     func(context.Context, storage.Store) (string, error)
		update     func(context.Context, storage.Store, string) error
		delete     func(context.Context, storage.Store, string) error
		get        func(context.Context, storage.Store, string) error
	}{
		{
			name: "task", entityType: "task",
			create: func(ctx context.Context, store storage.Store) (string, error) {
				task := &model.Task{Title: "Legacy task", Content: "Legacy content"}
				err := store.Tasks().Create(ctx, task)
				return task.ID, err
			},
			update: func(ctx context.Context, store storage.Store, id string) error {
				title := "Legacy task updated"
				_, err := store.Tasks().Update(ctx, id, &model.UpdateTaskRequest{Title: &title})
				return err
			},
			delete: func(ctx context.Context, store storage.Store, id string) error { return store.Tasks().Delete(ctx, id) },
			get: func(ctx context.Context, store storage.Store, id string) error {
				_, err := store.Tasks().GetByID(ctx, id)
				return err
			},
		},
		{
			name: "event", entityType: "event",
			create: func(ctx context.Context, store storage.Store) (string, error) {
				event := &model.Event{Title: "Legacy event", StartTime: 1800000000, EndTime: 1800003600}
				err := store.Events().Create(ctx, event)
				return event.ID, err
			},
			update: func(ctx context.Context, store storage.Store, id string) error {
				location := "Legacy room"
				_, err := store.Events().Update(ctx, id, &model.UpdateEventRequest{Location: &location})
				return err
			},
			delete: func(ctx context.Context, store storage.Store, id string) error { return store.Events().Delete(ctx, id) },
			get: func(ctx context.Context, store storage.Store, id string) error {
				_, err := store.Events().GetByID(ctx, id)
				return err
			},
		},
		{
			name: "inbox", entityType: "inbox",
			create: func(ctx context.Context, store storage.Store) (string, error) {
				item := &model.InboxItem{Kind: "note", Title: "Legacy inbox"}
				err := store.Inbox().Create(ctx, item)
				return item.ID, err
			},
			update: func(ctx context.Context, store storage.Store, id string) error {
				_, err := store.Inbox().BatchArchive(ctx, []string{id})
				return err
			},
			delete: func(ctx context.Context, store storage.Store, id string) error { return store.Inbox().Delete(ctx, id) },
			get: func(ctx context.Context, store storage.Store, id string) error {
				_, err := store.Inbox().GetByID(ctx, id)
				return err
			},
		},
	} {
		t.Run("MSYNC-ADAPTER-002_"+testCase.name+"_LegacyCRUDWritesRevisionTombstoneAndOutbox", func(t *testing.T) {
			store := factory(t)
			defer store.Close()
			ctx := scopedContractContext(t, store)
			repository := mobileSyncRepository(t, store)
			id, err := testCase.create(ctx, store)
			if err != nil {
				t.Fatal(err)
			}
			changes, err := repository.ListPendingChanges(ctx)
			if err != nil || len(changes) != 1 || changes[0].Entity.EntityType != testCase.entityType || changes[0].Entity.Revision != 1 {
				t.Fatalf("create changes=%+v err=%v", changes, err)
			}
			clientID := changes[0].Entity.ClientID
			if clientID == "" || id == "" {
				t.Fatalf("create id=%q clientID=%q", id, clientID)
			}
			if err := testCase.update(ctx, store, id); err != nil {
				t.Fatal(err)
			}
			changes, err = repository.ListPendingChanges(ctx)
			if err != nil || len(changes) != 2 || changes[1].Entity.Revision != 2 || changes[1].Operation != testCase.entityType+".server_updated" {
				t.Fatalf("update changes=%+v err=%v", changes, err)
			}
			if err := testCase.delete(ctx, store, id); err != nil {
				t.Fatal(err)
			}
			changes, err = repository.ListPendingChanges(ctx)
			if err != nil || len(changes) != 3 || changes[2].Entity.Revision != 3 || changes[2].Entity.DeletedAt == nil || changes[2].Operation != testCase.entityType+".server_deleted" {
				t.Fatalf("delete changes=%+v err=%v", changes, err)
			}
			if err := testCase.get(ctx, store, id); err == nil {
				t.Fatal("legacy read still exposes tombstoned entity")
			}
			tombstone, err := repository.GetEntityByClientID(ctx, testCase.entityType, clientID)
			if err != nil || tombstone.Revision != 3 || tombstone.DeletedAt == nil {
				t.Fatalf("tombstone=%+v err=%v", tombstone, err)
			}
		})
	}

	t.Run("MSYNC-ADAPTER-004_TaskReadModelsHideTombstones", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		now := time.Now().UTC()
		todayStart := now.Truncate(24 * time.Hour).Unix()
		todayEnd := todayStart + int64((24 * time.Hour).Seconds())
		plannedDate := now.Format("2006-01-02")

		openTask := &model.Task{Title: "Deleted from today", Due: int64Pointer(now.Unix()), PlannedDate: &plannedDate}
		if err := store.Tasks().Create(ctx, openTask); err != nil {
			t.Fatal(err)
		}
		if err := store.Tasks().Delete(ctx, openTask.ID); err != nil {
			t.Fatal(err)
		}
		today, overdue, err := store.Tasks().Today(ctx, todayStart, todayEnd, todayStart-int64((30*24*time.Hour).Seconds()))
		if err != nil {
			t.Fatal(err)
		}
		if len(today) != 0 || len(overdue) != 0 {
			t.Fatalf("today=%+v overdue=%+v, want no tombstoned tasks", today, overdue)
		}

		completedAt := now.Unix()
		completedTask := &model.Task{Title: "Deleted from history", Done: 1, Status: "done", CompletedAt: &completedAt}
		if err := store.Tasks().Create(ctx, completedTask); err != nil {
			t.Fatal(err)
		}
		if err := store.Tasks().Delete(ctx, completedTask.ID); err != nil {
			t.Fatal(err)
		}
		completed, total, err := store.Tasks().GetCompletedTasksByRange(ctx, completedAt-1, completedAt+2, 1, 20)
		if err != nil {
			t.Fatal(err)
		}
		if total != 0 || len(completed) != 0 {
			t.Fatalf("completed total=%d rows=%+v, want no tombstoned tasks", total, completed)
		}
		activeDays, projectCount, err := store.Tasks().GetSummaryStats(ctx, completedAt-1, completedAt+2)
		if err != nil {
			t.Fatal(err)
		}
		if activeDays != 0 || projectCount != 0 {
			t.Fatalf("summary activeDays=%d projectCount=%d, want zero", activeDays, projectCount)
		}
	})

	t.Run("MSYNC-OCC-004_ParentTaskDeleteWritesOccurrenceTombstones", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		projectID := "personal"
		task := &model.Task{Title: "Recurring parent", ProjectID: &projectID, ExecutionType: "recurring"}
		if err := store.Tasks().Create(ctx, task); err != nil {
			t.Fatal(err)
		}
		occurrenceIDs := make([]string, 0, 2)
		for index, date := range []string{"2026-07-14", "2026-07-15"} {
			if _, err := store.Recurrence().CompleteOccurrence(ctx, task.ID, date, time.Now().UTC().Unix()+int64(index)); err != nil {
				t.Fatal(err)
			}
			changes, err := repository.ListPendingChanges(ctx)
			if err != nil {
				t.Fatal(err)
			}
			occurrenceIDs = append(occurrenceIDs, changes[len(changes)-1].Entity.ClientID)
		}
		beforeDelete, err := repository.ListPendingChanges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Tasks().Delete(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
		afterDelete, err := repository.ListPendingChanges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		deleteChanges := afterDelete[len(beforeDelete):]
		if len(deleteChanges) != 3 {
			t.Fatalf("parent delete changes=%d, want two occurrence tombstones and one task tombstone: %+v", len(deleteChanges), deleteChanges)
		}
		occurrenceTombstones := 0
		taskTombstones := 0
		for _, change := range deleteChanges {
			switch change.Entity.EntityType {
			case "task_occurrence":
				if change.Operation != "task_occurrence.server_deleted" || change.Entity.DeletedAt == nil || change.Entity.Revision != 2 {
					t.Fatalf("occurrence delete change=%+v", change)
				}
				occurrenceTombstones++
			case "task":
				if change.Operation != "task.server_deleted" || change.Entity.DeletedAt == nil {
					t.Fatalf("task delete change=%+v", change)
				}
				taskTombstones++
			}
		}
		if occurrenceTombstones != 2 || taskTombstones != 1 {
			t.Fatalf("occurrence tombstones=%d task tombstones=%d", occurrenceTombstones, taskTombstones)
		}
		for _, occurrenceID := range occurrenceIDs {
			tombstone, err := repository.GetEntityByClientID(ctx, "task_occurrence", occurrenceID)
			if err != nil || tombstone.Revision != 2 || tombstone.DeletedAt == nil {
				t.Fatalf("occurrence tombstone=%+v err=%v", tombstone, err)
			}
		}
		visible, err := store.Recurrence().ListOccurrences(ctx, "2026-07-14", "2026-07-15")
		if err != nil {
			t.Fatal(err)
		}
		if len(visible) != 0 {
			t.Fatalf("legacy occurrence list exposes tombstones: %+v", visible)
		}
	})

	t.Run("MSYNC-CONFLICT-002_ResolveDoubleCASRejectsAdvancedTarget", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		deviceID := "abababab-abab-4bab-8bab-abababababab"
		entityID := "cdcdcdcd-cdcd-4dcd-8dcd-cdcdcdcdcdcd"
		title := "Base"
		if _, err := repository.ApplyNoteMutation(ctx, model.MobileNoteMutation{
			MutationID: uuid.NewString(), DeviceClientID: deviceID, EntityClientID: entityID,
			Operation: model.MobileOperationNoteCreate, RequestSHA256: "conflict-create",
			Payload: model.MobileNotePayload{Title: &title},
		}); err != nil {
			t.Fatal(err)
		}
		baseOne := int64(1)
		remoteTitle := "Remote revision two"
		if _, err := repository.ApplyNoteMutation(ctx, model.MobileNoteMutation{
			MutationID: uuid.NewString(), DeviceClientID: deviceID, EntityClientID: entityID,
			Operation: model.MobileOperationNoteUpdate, BaseRevision: &baseOne, RequestSHA256: "conflict-remote-update",
			Payload: model.MobileNotePayload{Title: &remoteTitle},
		}); err != nil {
			t.Fatal(err)
		}
		createConflict := model.CreateMobileSyncConflict{
			ConflictID: uuid.NewString(), MutationID: uuid.NewString(), DeviceClientID: deviceID,
			RequestSHA256: "conflict-local-request", EntityType: "note", EntityClientID: entityID,
			Operation:    model.MobileOperationNoteUpdate,
			BaseRevision: 1, LocalPayload: json.RawMessage(`{"title":"Local draft"}`),
		}
		created, err := repository.CreateConflict(ctx, createConflict)
		if err != nil {
			t.Fatalf("create conflict: %v", err)
		}
		if created.Status != model.MobileMutationConflict || created.ErrorCode != "revision_conflict" || created.Entity == nil || created.Entity.EntityType != "sync_conflict" || created.Entity.Revision != 1 {
			t.Fatalf("created conflict result=%+v", created)
		}
		replayed, err := repository.CreateConflict(ctx, createConflict)
		if err != nil || !reflect.DeepEqual(replayed, created) {
			t.Fatalf("replayed conflict=%+v err=%v, want %+v", replayed, err, created)
		}
		changedConflict := createConflict
		changedConflict.RequestSHA256 = "different-local-request"
		if _, err := repository.CreateConflict(ctx, changedConflict); !errors.Is(err, storage.ErrMutationIDReused) {
			t.Fatalf("changed conflict replay error=%v", err)
		}
		conflicts, err := repository.ListUnresolvedConflicts(ctx)
		if err != nil || len(conflicts) != 1 {
			t.Fatalf("conflicts=%+v err=%v", conflicts, err)
		}
		conflict := conflicts[0]
		if conflict.ConflictID != createConflict.ConflictID || conflict.BaseRevision != 1 || conflict.RemoteRevision != 2 || conflict.Revision != 1 {
			t.Fatalf("conflict=%+v", conflict)
		}

		baseTwo := int64(2)
		thirdTitle := "Third writer"
		third, err := repository.ApplyNoteMutation(ctx, model.MobileNoteMutation{
			MutationID: uuid.NewString(), DeviceClientID: deviceID, EntityClientID: entityID,
			Operation: model.MobileOperationNoteUpdate, BaseRevision: &baseTwo, RequestSHA256: "conflict-third-writer",
			Payload: model.MobileNotePayload{Title: &thirdTitle},
		})
		if err != nil || third.Entity == nil || third.Entity.Revision != 3 {
			t.Fatalf("third writer=%+v err=%v", third, err)
		}
		if _, err := repository.ResolveConflict(ctx, model.ResolveMobileSyncConflict{
			ConflictID: conflict.ConflictID, MutationID: uuid.NewString(), RequestSHA256: "resolve-stale", ConflictRevision: 1,
			TargetRevision: 2, Resolution: "keep_local",
		}); !errors.Is(err, storage.ErrMobileTargetAdvanced) {
			t.Fatalf("stale target resolve error=%v, want ErrMobileTargetAdvanced", err)
		}
		conflicts, err = repository.ListUnresolvedConflicts(ctx)
		if err != nil || len(conflicts) != 1 || conflicts[0].Revision != 2 || conflicts[0].RemoteRevision != 3 {
			t.Fatalf("advanced conflict=%+v err=%v", conflicts, err)
		}
		conflict = conflicts[0]
		resolved, err := repository.ResolveConflict(ctx, model.ResolveMobileSyncConflict{
			ConflictID: conflict.ConflictID, MutationID: uuid.NewString(), RequestSHA256: "resolve-final", ConflictRevision: 2,
			TargetRevision: 3, Resolution: "keep_local",
		})
		if err != nil {
			t.Fatalf("resolve conflict: %v", err)
		}
		if resolved.Revision != 4 {
			t.Fatalf("resolved entity revision=%d, want 4", resolved.Revision)
		}
		var resolvedPayload map[string]any
		if err := json.Unmarshal(resolved.Payload, &resolvedPayload); err != nil {
			t.Fatal(err)
		}
		if resolvedPayload["title"] != "Local draft" {
			t.Fatalf("resolved payload=%+v", resolvedPayload)
		}
		conflicts, err = repository.ListUnresolvedConflicts(ctx)
		if err != nil || len(conflicts) != 0 {
			t.Fatalf("unresolved conflicts after resolve=%+v err=%v", conflicts, err)
		}
		if _, err := repository.ResolveConflict(ctx, model.ResolveMobileSyncConflict{
			ConflictID: conflict.ConflictID, MutationID: uuid.NewString(), RequestSHA256: "resolve-second", ConflictRevision: 2,
			TargetRevision: 4, Resolution: "keep_remote",
		}); !errors.Is(err, storage.ErrMobileConflictAdvanced) {
			t.Fatalf("second resolve error=%v, want ErrMobileConflictAdvanced", err)
		}
		changes, err := repository.ListPendingChanges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		createdChange := false
		resolvedChange := false
		for _, change := range changes {
			createdChange = createdChange || change.Operation == "sync_conflict.created"
			resolvedChange = resolvedChange || change.Operation == "sync_conflict.resolved"
		}
		if !createdChange || !resolvedChange {
			t.Fatalf("conflict outbox changes=%+v", changes)
		}
	})

	t.Run("MSYNC-CONFLICT-001_TwoWritersWithSameBaseYieldOnePersistedConflict", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		repository := mobileSyncRepository(t, store)
		entityID := "dededede-dede-4ede-8ede-dededededede"
		creatorID := "dfdfdfdf-dfdf-4fdf-8fdf-dfdfdfdfdfdf"
		title := "Concurrent base"
		if _, err := repository.ApplyNoteMutation(ctx, model.MobileNoteMutation{
			MutationID: uuid.NewString(), DeviceClientID: creatorID, EntityClientID: entityID,
			Operation: model.MobileOperationNoteCreate, RequestSHA256: "two-writer-create",
			Payload: model.MobileNotePayload{Title: &title},
		}); err != nil {
			t.Fatal(err)
		}
		baseOne := int64(1)
		batches := []mobilesync.MutationBatch{
			{
				ClientID: "e1e1e1e1-e1e1-41e1-81e1-e1e1e1e1e1e1",
				Mutations: []mobilesync.MutationInput{{
					MutationID: uuid.NewString(), Operation: model.MobileOperationNoteUpdate,
					EntityID: entityID, BaseRevision: &baseOne, Payload: json.RawMessage(`{"title":"Writer A"}`),
				}},
			},
			{
				ClientID: "e2e2e2e2-e2e2-42e2-82e2-e2e2e2e2e2e2",
				Mutations: []mobilesync.MutationInput{{
					MutationID: uuid.NewString(), Operation: model.MobileOperationNoteUpdate,
					EntityID: entityID, BaseRevision: &baseOne, Payload: json.RawMessage(`{"title":"Writer B"}`),
				}},
			},
		}
		start := make(chan struct{})
		results := make(chan *mobilesync.BatchResult, 2)
		errorsFound := make(chan error, 2)
		var ready sync.WaitGroup
		ready.Add(2)
		for _, batch := range batches {
			batch := batch
			go func() {
				ready.Done()
				<-start
				result, err := mobilesync.ApplyBatch(ctx, store, batch)
				results <- result
				errorsFound <- err
			}()
		}
		ready.Wait()
		close(start)
		applied := 0
		conflicted := 0
		for range 2 {
			result := <-results
			if err := <-errorsFound; err != nil {
				t.Fatal(err)
			}
			if result == nil || len(result.Results) != 1 {
				t.Fatalf("two-writer result=%+v", result)
			}
			switch result.Results[0].Status {
			case model.MobileMutationApplied:
				applied++
			case model.MobileMutationConflict:
				if result.Results[0].Entity == nil || result.Results[0].Entity.EntityType != "sync_conflict" {
					t.Fatalf("conflict result=%+v", result.Results[0])
				}
				conflicted++
			default:
				t.Fatalf("two-writer status=%+v", result.Results[0])
			}
		}
		if applied != 1 || conflicted != 1 {
			t.Fatalf("applied=%d conflicted=%d, want one each", applied, conflicted)
		}
		conflicts, err := repository.ListUnresolvedConflicts(ctx)
		if err != nil || len(conflicts) != 1 || conflicts[0].RemoteRevision != 2 {
			t.Fatalf("persisted conflicts=%+v err=%v", conflicts, err)
		}
	})
}

func int64Pointer(value int64) *int64 { return &value }

func mobileSyncRepository(t *testing.T, store storage.Store) storage.MobileSyncRepository {
	t.Helper()
	repository, err := storage.MobileSyncRepositoryFrom(store)
	if err != nil {
		t.Fatalf("mobile sync repository: %v", err)
	}
	return repository
}

func voiceAudioCleanupRepository(t *testing.T, store storage.Store) storage.VoiceAudioCleanupRepository {
	t.Helper()
	repository, err := storage.VoiceAudioCleanupRepositoryFrom(store)
	if err != nil {
		t.Fatalf("voice audio cleanup repository: %v", err)
	}
	return repository
}
