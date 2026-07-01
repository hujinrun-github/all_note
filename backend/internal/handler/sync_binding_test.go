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
	"strings"
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

func TestGetNoteSyncBindingRedactsTargetToken(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	const rawToken = "ntn_unit_test_binding_token"
	target := saveHandlerNotionTarget(t)
	target.ConfigJSON = `{"data_source_id":"ds-123","token":"` + rawToken + `","required_tags":["sync"]}`
	if err := store.Sync().SaveTarget(handlerSyncStoreTestContext(t), &target); err != nil {
		t.Fatalf("save target config: %v", err)
	}
	note := insertHandlerNoteForTest(t, "Bound Notion", "Body\n")
	putHandlerBinding(t, store, note.ID, target.ID)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/notes/"+note.ID+"/sync-binding", nil)

	GetNoteSyncBinding(store)(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), rawToken) {
		t.Fatalf("response leaked raw token: %s", recorder.Body.String())
	}
	var body struct {
		Data model.NoteSyncBindingResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.Target == nil {
		t.Fatalf("target = nil; body = %s", recorder.Body.String())
	}
	var responseConfig map[string]any
	if err := json.Unmarshal([]byte(body.Data.Target.ConfigJSON), &responseConfig); err != nil {
		t.Fatalf("decode response config: %v", err)
	}
	if _, ok := responseConfig["token"]; ok {
		t.Fatalf("response config includes raw token: %v", responseConfig)
	}
	if responseConfig["token_set"] != true {
		t.Fatalf("token_set = %v, want true", responseConfig["token_set"])
	}
	if len(body.Data.Candidates) == 0 {
		t.Fatalf("candidates = empty; body = %s", recorder.Body.String())
	}
	var candidateConfig map[string]any
	if err := json.Unmarshal([]byte(body.Data.Candidates[0].Target.ConfigJSON), &candidateConfig); err != nil {
		t.Fatalf("decode candidate config: %v", err)
	}
	if _, ok := candidateConfig["token"]; ok || candidateConfig["token_set"] != true {
		t.Fatalf("candidate config is not redacted: %v", candidateConfig)
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

func TestPutNoteSyncBindingUsesRequestWorkspaceScope(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	scopedStore, ok := store.(handlerScopedStore)
	if !ok {
		t.Fatalf("store = %T, want handlerScopedStore", store)
	}
	baseStore := scopedStore.Store
	ctxB := auth.ContextWithWorkspaceScope(t.Context(), "handler-sync-request-workspace")
	if err := provisioning.EnsureDefaultWorkspaceData(ctxB, baseStore); err != nil {
		t.Fatalf("seed workspace B defaults: %v", err)
	}
	targetB := &model.SyncTarget{
		Type:       "notion",
		Name:       "Workspace B Target",
		ConfigJSON: `{"data_source_id":"workspace-b"}`,
		Enabled:    true,
	}
	if err := baseStore.Sync().SaveTarget(ctxB, targetB); err != nil {
		t.Fatalf("save workspace B target: %v", err)
	}
	noteB, err := baseStore.Notes().Create(ctxB, &model.CreateNoteRequest{
		Title:    "Workspace B Bind Note",
		Body:     "Body\n",
		FolderID: "__uncategorized",
		Tags:     "[]",
	})
	if err != nil {
		t.Fatalf("create workspace B note: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: noteB.ID}}
	c.Request = httptest.NewRequest(http.MethodPut, "/notes/"+noteB.ID+"/sync-binding", bytes.NewBufferString(`{"target_id":"`+targetB.ID+`"}`)).WithContext(ctxB)
	c.Request.Header.Set("Content-Type", "application/json")

	PutNoteSyncBinding(store)(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if _, err := baseStore.Sync().GetBinding(handlerSyncStoreTestContext(t), noteB.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("workspace A binding err=%v, want sql.ErrNoRows", err)
	}
	binding, err := baseStore.Sync().GetBinding(ctxB, noteB.ID)
	if err != nil {
		t.Fatalf("get workspace B binding: %v", err)
	}
	if binding.TargetID != targetB.ID {
		t.Fatalf("target_id = %q, want %q", binding.TargetID, targetB.ID)
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
	baseStore, err := sqlite.Provider{}.Open(t.Context(), storage.Config{
		Env:        "test",
		Driver:     storage.DriverSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	ctx := handlerSyncStoreTestContext(t)
	if err := provisioning.EnsureDefaultWorkspaceData(ctx, baseStore); err != nil {
		t.Fatalf("seed workspace defaults: %v", err)
	}
	store := handlerScopedStore{Store: baseStore, ctx: ctx}
	repository.SetStore(store)
	t.Cleanup(func() {
		repository.SetStore(nil)
		if err := baseStore.Close(); err != nil {
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
	ctx := handlerSyncStoreTestContext(t)
	if err := store.Sync().PutBinding(ctx, model.NoteSyncBinding{NoteID: noteID, TargetID: targetID}); err != nil {
		t.Fatalf("put binding: %v", err)
	}
	binding, err := store.Sync().GetBinding(ctx, noteID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	return *binding
}

type handlerScopedStore struct {
	storage.Store
	ctx context.Context
}

func (store handlerScopedStore) Transact(ctx context.Context, fn func(storage.Store) error) error {
	effectiveCtx := handlerScopedContext(store.ctx, ctx)
	return store.Store.Transact(effectiveCtx, func(txStore storage.Store) error {
		return fn(handlerScopedStore{Store: txStore, ctx: effectiveCtx})
	})
}

func (store handlerScopedStore) Notes() storage.NoteRepository {
	return handlerScopedNoteRepository{NoteRepository: store.Store.Notes(), ctx: store.ctx}
}

func (store handlerScopedStore) Sync() storage.SyncRepository {
	return handlerScopedSyncRepository{base: store.Store.Sync(), ctx: store.ctx}
}

type handlerScopedNoteRepository struct {
	storage.NoteRepository
	ctx context.Context
}

func (repo handlerScopedNoteRepository) List(ctx context.Context, filter storage.NoteFilter) ([]model.Note, int, error) {
	return repo.NoteRepository.List(handlerScopedContext(repo.ctx, ctx), filter)
}

func (repo handlerScopedNoteRepository) GetByID(ctx context.Context, id string) (*model.Note, error) {
	return repo.NoteRepository.GetByID(handlerScopedContext(repo.ctx, ctx), id)
}

func (repo handlerScopedNoteRepository) Create(ctx context.Context, req *model.CreateNoteRequest) (*model.Note, error) {
	return repo.NoteRepository.Create(handlerScopedContext(repo.ctx, ctx), req)
}

func (repo handlerScopedNoteRepository) CreateWithID(ctx context.Context, note *model.Note) error {
	return repo.NoteRepository.CreateWithID(handlerScopedContext(repo.ctx, ctx), note)
}

func (repo handlerScopedNoteRepository) Update(ctx context.Context, id string, req *model.UpdateNoteRequest) (*model.Note, error) {
	return repo.NoteRepository.Update(handlerScopedContext(repo.ctx, ctx), id, req)
}

func (repo handlerScopedNoteRepository) Delete(ctx context.Context, id string) error {
	return repo.NoteRepository.Delete(handlerScopedContext(repo.ctx, ctx), id)
}

func (repo handlerScopedNoteRepository) ListAll(ctx context.Context) ([]model.Note, error) {
	return repo.NoteRepository.ListAll(handlerScopedContext(repo.ctx, ctx))
}

func (repo handlerScopedNoteRepository) Recent(ctx context.Context, limit int) ([]model.Note, error) {
	return repo.NoteRepository.Recent(handlerScopedContext(repo.ctx, ctx), limit)
}

func (repo handlerScopedNoteRepository) GetNotesByProjectIDs(ctx context.Context, projectIDs []string) (map[string][]model.NoteRef, error) {
	return repo.NoteRepository.GetNotesByProjectIDs(handlerScopedContext(repo.ctx, ctx), projectIDs)
}

type handlerScopedSyncRepository struct {
	base storage.SyncRepository
	ctx  context.Context
}

func (repo handlerScopedSyncRepository) SaveTarget(ctx context.Context, target *model.SyncTarget) error {
	return repo.base.SaveTarget(handlerScopedContext(repo.ctx, ctx), target)
}

func (repo handlerScopedSyncRepository) GetTarget(ctx context.Context, targetID string) (*model.SyncTarget, error) {
	return repo.base.GetTarget(handlerScopedContext(repo.ctx, ctx), targetID)
}

func (repo handlerScopedSyncRepository) LockTarget(ctx context.Context, targetID string) (*model.SyncTarget, error) {
	return repo.base.LockTarget(handlerScopedContext(repo.ctx, ctx), targetID)
}

func (repo handlerScopedSyncRepository) GetDefaultTarget(ctx context.Context, targetType string) (*model.SyncTarget, error) {
	return repo.base.GetDefaultTarget(handlerScopedContext(repo.ctx, ctx), targetType)
}

func (repo handlerScopedSyncRepository) ListTargets(ctx context.Context) ([]model.SyncTarget, error) {
	return repo.base.ListTargets(handlerScopedContext(repo.ctx, ctx))
}

func (repo handlerScopedSyncRepository) DeleteTarget(ctx context.Context, targetID string) error {
	return repo.base.DeleteTarget(handlerScopedContext(repo.ctx, ctx), targetID)
}

func (repo handlerScopedSyncRepository) CountBindingsByTarget(ctx context.Context, targetID string) (int, error) {
	return repo.base.CountBindingsByTarget(handlerScopedContext(repo.ctx, ctx), targetID)
}

func (repo handlerScopedSyncRepository) CountClaimsByTarget(ctx context.Context, targetID string) (int, error) {
	return repo.base.CountClaimsByTarget(handlerScopedContext(repo.ctx, ctx), targetID)
}

func (repo handlerScopedSyncRepository) CountStatesByTarget(ctx context.Context, targetID string) (int, error) {
	return repo.base.CountStatesByTarget(handlerScopedContext(repo.ctx, ctx), targetID)
}

func (repo handlerScopedSyncRepository) UpsertState(ctx context.Context, state *model.SyncState) error {
	return repo.base.UpsertState(handlerScopedContext(repo.ctx, ctx), state)
}

func (repo handlerScopedSyncRepository) GetState(ctx context.Context, noteID string, targetID string) (*model.SyncState, error) {
	return repo.base.GetState(handlerScopedContext(repo.ctx, ctx), noteID, targetID)
}

func (repo handlerScopedSyncRepository) ListStatesByTarget(ctx context.Context, targetID string) ([]model.SyncState, error) {
	return repo.base.ListStatesByTarget(handlerScopedContext(repo.ctx, ctx), targetID)
}

func (repo handlerScopedSyncRepository) DeleteState(ctx context.Context, noteID string, targetID string) error {
	return repo.base.DeleteState(handlerScopedContext(repo.ctx, ctx), noteID, targetID)
}

func (repo handlerScopedSyncRepository) ListExternalDeletedStates(ctx context.Context, targetID string) ([]model.ExternalDeletedNote, error) {
	return repo.base.ListExternalDeletedStates(handlerScopedContext(repo.ctx, ctx), targetID)
}

func (repo handlerScopedSyncRepository) LockBindingSlot(ctx context.Context, noteID string) error {
	return repo.base.LockBindingSlot(handlerScopedContext(repo.ctx, ctx), noteID)
}

func (repo handlerScopedSyncRepository) GetBinding(ctx context.Context, noteID string) (*model.NoteSyncBinding, error) {
	return repo.base.GetBinding(handlerScopedContext(repo.ctx, ctx), noteID)
}

func (repo handlerScopedSyncRepository) PutBinding(ctx context.Context, binding model.NoteSyncBinding) error {
	return repo.base.PutBinding(handlerScopedContext(repo.ctx, ctx), binding)
}

func (repo handlerScopedSyncRepository) DeleteBinding(ctx context.Context, noteID string) error {
	return repo.base.DeleteBinding(handlerScopedContext(repo.ctx, ctx), noteID)
}

func (repo handlerScopedSyncRepository) ListBindingsByTarget(ctx context.Context, targetID string) ([]model.NoteSyncBinding, error) {
	return repo.base.ListBindingsByTarget(handlerScopedContext(repo.ctx, ctx), targetID)
}

func (repo handlerScopedSyncRepository) GetExternalClaim(ctx context.Context, externalKey string) (*model.SyncExternalClaim, error) {
	return repo.base.GetExternalClaim(handlerScopedContext(repo.ctx, ctx), externalKey)
}

func (repo handlerScopedSyncRepository) GetExternalClaimByNote(ctx context.Context, noteID string) (*model.SyncExternalClaim, error) {
	return repo.base.GetExternalClaimByNote(handlerScopedContext(repo.ctx, ctx), noteID)
}

func (repo handlerScopedSyncRepository) PutExternalClaim(ctx context.Context, claim model.SyncExternalClaim) error {
	return repo.base.PutExternalClaim(handlerScopedContext(repo.ctx, ctx), claim)
}

func (repo handlerScopedSyncRepository) ReleaseExternalClaim(ctx context.Context, noteID string) error {
	return repo.base.ReleaseExternalClaim(handlerScopedContext(repo.ctx, ctx), noteID)
}

func (repo handlerScopedSyncRepository) PutSuppression(ctx context.Context, suppression model.NoteSyncSuppression) error {
	return repo.base.PutSuppression(handlerScopedContext(repo.ctx, ctx), suppression)
}

func (repo handlerScopedSyncRepository) DeleteSuppression(ctx context.Context, noteID string, targetID string) error {
	return repo.base.DeleteSuppression(handlerScopedContext(repo.ctx, ctx), noteID, targetID)
}

func (repo handlerScopedSyncRepository) GetSuppression(ctx context.Context, noteID string, targetID string) (*model.NoteSyncSuppression, error) {
	return repo.base.GetSuppression(handlerScopedContext(repo.ctx, ctx), noteID, targetID)
}

func (repo handlerScopedSyncRepository) PutImportTombstone(ctx context.Context, tombstone model.SyncImportTombstone) error {
	return repo.base.PutImportTombstone(handlerScopedContext(repo.ctx, ctx), tombstone)
}

func (repo handlerScopedSyncRepository) DeleteImportTombstone(ctx context.Context, externalKey string) error {
	return repo.base.DeleteImportTombstone(handlerScopedContext(repo.ctx, ctx), externalKey)
}

func (repo handlerScopedSyncRepository) DeleteImportTombstonesForNoteTarget(ctx context.Context, noteID string, targetID string) error {
	return repo.base.DeleteImportTombstonesForNoteTarget(handlerScopedContext(repo.ctx, ctx), noteID, targetID)
}

func (repo handlerScopedSyncRepository) FindImportTombstone(ctx context.Context, targetID string, externalKey string, formerNoteID string, externalType string) (*model.SyncImportTombstone, error) {
	return repo.base.FindImportTombstone(handlerScopedContext(repo.ctx, ctx), targetID, externalKey, formerNoteID, externalType)
}

func handlerScopedContext(fallback context.Context, ctx context.Context) context.Context {
	if _, err := auth.WorkspaceIDFromContext(ctx); err == nil {
		return ctx
	}
	return fallback
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
