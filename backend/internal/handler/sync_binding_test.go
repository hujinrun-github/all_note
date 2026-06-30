package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/provisioning"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/sqlite"
)

func TestGetNoteSyncBindingReturnsNullWhenUnbound(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	note := insertHandlerNoteForTest(t, "Unbound Note", "Body\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/notes/"+note.ID+"/sync-binding", nil)

	GetNoteSyncBinding(store)(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var body struct {
		Data model.NoteSyncBindingResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.Binding != nil {
		t.Fatalf("binding = %+v, want nil", body.Data.Binding)
	}
}

func TestPutNoteSyncBindingCreatesBinding(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	target := saveHandlerObsidianTarget(t)
	note := insertHandlerNoteForTest(t, "Bind Note", "Body\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodPut, "/notes/"+note.ID+"/sync-binding", bytes.NewBufferString(`{"target_id":"`+target.ID+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	PutNoteSyncBinding(store)(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	binding, err := store.Sync().GetBinding(c.Request.Context(), note.ID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if binding.TargetID != target.ID {
		t.Fatalf("target_id = %q, want %q", binding.TargetID, target.ID)
	}
}

func TestPutNoteSyncBindingRequiresConfirmWhenChangingTarget(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	oldTarget := saveHandlerObsidianTarget(t)
	newTarget := saveHandlerNotionTarget(t)
	note := insertHandlerNoteForTest(t, "Change Needs Confirm", "Body\n")
	putHandlerBinding(t, store, note.ID, oldTarget.ID)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodPut, "/notes/"+note.ID+"/sync-binding", bytes.NewBufferString(`{
		"target_id":"`+newTarget.ID+`",
		"expected_target_id":"`+oldTarget.ID+`"
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	PutNoteSyncBinding(store)(c)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	binding, err := store.Sync().GetBinding(c.Request.Context(), note.ID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if binding.TargetID != oldTarget.ID {
		t.Fatalf("target_id = %q, want unchanged %q", binding.TargetID, oldTarget.ID)
	}
}

func TestPutNoteSyncBindingRejectsExpectedTargetMismatch(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	oldTarget := saveHandlerObsidianTarget(t)
	newTarget := saveHandlerNotionTarget(t)
	note := insertHandlerNoteForTest(t, "Stale Put", "Body\n")
	putHandlerBinding(t, store, note.ID, oldTarget.ID)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodPut, "/notes/"+note.ID+"/sync-binding", bytes.NewBufferString(`{
		"target_id":"`+newTarget.ID+`",
		"expected_target_id":"missing-target",
		"confirm_changed_target":true
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	PutNoteSyncBinding(store)(c)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	binding, err := store.Sync().GetBinding(c.Request.Context(), note.ID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if binding.TargetID != oldTarget.ID {
		t.Fatalf("target_id = %q, want unchanged %q", binding.TargetID, oldTarget.ID)
	}
}

func TestPutNoteSyncBindingDeletesSuppressionAndTombstoneOnExplicitRebind(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	target := saveHandlerNotionTarget(t)
	note := insertHandlerNoteForTest(t, "Explicit Rebind", "Body\n")
	ctx := httptest.NewRequest(http.MethodPut, "/", nil).Context()
	if err := store.Sync().PutSuppression(ctx, model.NoteSyncSuppression{NoteID: note.ID, TargetID: target.ID, Reason: "user_unbound"}); err != nil {
		t.Fatalf("put suppression: %v", err)
	}
	if err := store.Sync().PutImportTombstone(ctx, model.SyncImportTombstone{
		ExternalKey:  "notion:page-rebind",
		TargetID:     target.ID,
		FormerNoteID: note.ID,
		ExternalType: "notion_page",
		ExternalID:   "page-rebind",
		Reason:       "user_unbound",
	}); err != nil {
		t.Fatalf("put tombstone: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodPut, "/notes/"+note.ID+"/sync-binding", bytes.NewBufferString(`{"target_id":"`+target.ID+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	PutNoteSyncBinding(store)(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if _, err := store.Sync().GetSuppression(c.Request.Context(), note.ID, target.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("suppression error = %v, want sql.ErrNoRows", err)
	}
	if _, err := store.Sync().FindImportTombstone(c.Request.Context(), target.ID, "notion:page-rebind", note.ID, "notion_page"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("tombstone error = %v, want sql.ErrNoRows", err)
	}
}

func TestPutNoteSyncBindingAcquiresBindingSlotLockBeforeWrite(t *testing.T) {
	repo := &bindingSlotLockFakeSync{
		target: model.SyncTarget{ID: "target-1", Type: "notion", Name: "Target", Enabled: true},
	}
	repository.SetStore(&bindingSlotLockFakeStore{syncRepo: repo})
	t.Cleanup(func() { repository.SetStore(nil) })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "note-1"}}
	c.Request = httptest.NewRequest(http.MethodPut, "/notes/note-1/sync-binding", bytes.NewBufferString(`{"target_id":"target-1"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	PutNoteSyncBinding(&bindingSlotLockFakeStore{syncRepo: repo})(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !repo.slotLocked {
		t.Fatal("binding slot was not locked")
	}
}

func TestDeleteNoteSyncBindingRequiresExpectedTarget(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	note := insertHandlerNoteForTest(t, "Delete Missing Expected", "Body\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/notes/"+note.ID+"/sync-binding", bytes.NewBufferString(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")

	DeleteNoteSyncBinding(store)(c)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestDeleteNoteSyncBindingRejectsExpectedUpdatedAtMismatch(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	target := saveHandlerNotionTarget(t)
	note := insertHandlerNoteForTest(t, "Stale Delete", "Body\n")
	binding := putHandlerBinding(t, store, note.ID, target.ID)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/notes/"+note.ID+"/sync-binding", bytes.NewBufferString(`{
		"expected_target_id":"`+target.ID+`",
		"expected_updated_at":`+strconv.FormatInt(binding.UpdatedAt+1, 10)+`
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	DeleteNoteSyncBinding(store)(c)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	if _, err := store.Sync().GetBinding(c.Request.Context(), note.ID); err != nil {
		t.Fatalf("binding should remain: %v", err)
	}
}

func TestDeleteNoteSyncBindingWritesSuppressionAndTombstoneBeforeClaimRelease(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	target := saveHandlerNotionTarget(t)
	note := insertHandlerNoteForTest(t, "Delete Writes Tombstone", "Body\n")
	binding := putHandlerBinding(t, store, note.ID, target.ID)
	ctx := httptest.NewRequest(http.MethodDelete, "/", nil).Context()
	if err := store.Sync().PutExternalClaim(ctx, model.SyncExternalClaim{
		ExternalKey:  "notion:page-delete",
		NoteID:       note.ID,
		TargetID:     target.ID,
		ExternalType: "notion_page",
		ExternalID:   "page-delete",
	}); err != nil {
		t.Fatalf("put external claim: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/notes/"+note.ID+"/sync-binding", bytes.NewBufferString(`{
		"expected_target_id":"`+target.ID+`",
		"expected_updated_at":`+strconv.FormatInt(binding.UpdatedAt, 10)+`
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	DeleteNoteSyncBinding(store)(c)

	if c.Writer.Status() != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %s", c.Writer.Status(), http.StatusNoContent, recorder.Body.String())
	}
	if _, err := store.Sync().GetBinding(c.Request.Context(), note.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("binding error = %v, want sql.ErrNoRows", err)
	}
	if _, err := store.Sync().GetExternalClaimByNote(c.Request.Context(), note.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("claim error = %v, want sql.ErrNoRows", err)
	}
	suppression, err := store.Sync().GetSuppression(c.Request.Context(), note.ID, target.ID)
	if err != nil {
		t.Fatalf("get suppression: %v", err)
	}
	if suppression.Reason != "user_unbound" {
		t.Fatalf("suppression reason = %q", suppression.Reason)
	}
	tombstone, err := store.Sync().FindImportTombstone(c.Request.Context(), target.ID, "notion:page-delete", note.ID, "notion_page")
	if err != nil {
		t.Fatalf("get tombstone: %v", err)
	}
	if tombstone.ExternalID != "page-delete" || tombstone.Reason != "user_unbound" {
		t.Fatalf("tombstone = %+v", tombstone)
	}
}

func TestDeleteNoteSyncBindingAcquiresBindingSlotLockBeforeDelete(t *testing.T) {
	repo := &bindingSlotLockFakeSync{
		binding: &model.NoteSyncBinding{NoteID: "note-1", TargetID: "target-1", UpdatedAt: 42},
	}
	repository.SetStore(&bindingSlotLockFakeStore{syncRepo: repo})
	t.Cleanup(func() { repository.SetStore(nil) })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "note-1"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/notes/note-1/sync-binding", bytes.NewBufferString(`{
		"expected_target_id":"target-1",
		"expected_updated_at":42
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	DeleteNoteSyncBinding(&bindingSlotLockFakeStore{syncRepo: repo})(c)

	if c.Writer.Status() != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %s", c.Writer.Status(), http.StatusNoContent, recorder.Body.String())
	}
	if !repo.slotLocked {
		t.Fatal("binding slot was not locked")
	}
}

func openHandlerSyncStoreTestDB(t *testing.T) storage.Store {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dbPath := filepath.Join(t.TempDir(), "handler.flowspace.test.db")
	store, err := sqlite.Provider{}.Open(t.Context(), storage.Config{
		Env:        "test",
		Driver:     storage.DriverSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	if err := provisioning.EnsureDefaultWorkspaceData(handlerSyncStoreTestContext(t), store); err != nil {
		t.Fatalf("seed workspace defaults: %v", err)
	}
	repository.SetStore(store)
	t.Cleanup(func() {
		repository.SetStore(nil)
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})
	return store
}

func handlerSyncStoreTestContext(t *testing.T) context.Context {
	t.Helper()
	return auth.ContextWithWorkspaceScope(t.Context(), "handler-sync-test-workspace")
}

func handlerSyncStoreTestRequest(t *testing.T, method string, target string, body io.Reader) *http.Request {
	t.Helper()
	return httptest.NewRequest(method, target, body).WithContext(handlerSyncStoreTestContext(t))
}

func putHandlerBinding(t *testing.T, store storage.Store, noteID, targetID string) model.NoteSyncBinding {
	t.Helper()
	if err := store.Sync().PutBinding(t.Context(), model.NoteSyncBinding{NoteID: noteID, TargetID: targetID}); err != nil {
		t.Fatalf("put binding: %v", err)
	}
	binding, err := store.Sync().GetBinding(t.Context(), noteID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	return *binding
}

type bindingSlotLockFakeStore struct {
	storage.Store
	syncRepo *bindingSlotLockFakeSync
}

func (s *bindingSlotLockFakeStore) Transact(ctx context.Context, fn func(storage.Store) error) error {
	return fn(s)
}

func (s *bindingSlotLockFakeStore) Sync() storage.SyncRepository {
	return s.syncRepo
}

type bindingSlotLockFakeSync struct {
	storage.SyncRepository
	slotLocked bool
	target     model.SyncTarget
	binding    *model.NoteSyncBinding
}

func (r *bindingSlotLockFakeSync) LockBindingSlot(ctx context.Context, noteID string) error {
	r.slotLocked = true
	return nil
}

func (r *bindingSlotLockFakeSync) GetTarget(ctx context.Context, targetID string) (*model.SyncTarget, error) {
	if r.target.ID == "" || r.target.ID == targetID {
		target := r.target
		if target.ID == "" {
			target = model.SyncTarget{ID: targetID, Type: "notion", Name: "Target", Enabled: true}
		}
		return &target, nil
	}
	return nil, sql.ErrNoRows
}

func (r *bindingSlotLockFakeSync) GetBinding(ctx context.Context, noteID string) (*model.NoteSyncBinding, error) {
	if r.binding == nil {
		return nil, sql.ErrNoRows
	}
	binding := *r.binding
	return &binding, nil
}

func (r *bindingSlotLockFakeSync) PutBinding(ctx context.Context, binding model.NoteSyncBinding) error {
	if !r.slotLocked {
		return fmt.Errorf("binding slot not locked")
	}
	binding.UpdatedAt = 42
	r.binding = &binding
	return nil
}

func (r *bindingSlotLockFakeSync) DeleteBinding(ctx context.Context, noteID string) error {
	if !r.slotLocked {
		return fmt.Errorf("binding slot not locked")
	}
	r.binding = nil
	return nil
}

func (r *bindingSlotLockFakeSync) GetExternalClaimByNote(ctx context.Context, noteID string) (*model.SyncExternalClaim, error) {
	return nil, sql.ErrNoRows
}

func (r *bindingSlotLockFakeSync) DeleteSuppression(ctx context.Context, noteID string, targetID string) error {
	return nil
}

func (r *bindingSlotLockFakeSync) DeleteImportTombstonesForNoteTarget(ctx context.Context, noteID string, targetID string) error {
	return nil
}

func (r *bindingSlotLockFakeSync) PutSuppression(ctx context.Context, suppression model.NoteSyncSuppression) error {
	return nil
}
