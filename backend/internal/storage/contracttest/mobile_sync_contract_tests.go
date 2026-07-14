package contracttest

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
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
}

func mobileSyncRepository(t *testing.T, store storage.Store) storage.MobileSyncRepository {
	t.Helper()
	repository, err := storage.MobileSyncRepositoryFrom(store)
	if err != nil {
		t.Fatalf("mobile sync repository: %v", err)
	}
	return repository
}
