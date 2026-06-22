package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
)

func TestGetNoteSyncStateReturnsBindingMismatchForOtherType(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	notionTarget := saveHandlerNotionTarget(t)
	obsidianTarget := saveHandlerObsidianTarget(t)
	note := insertHandlerNoteForTest(t, "Mismatch State", "Body\n")
	putHandlerBinding(t, store, note.ID, notionTarget.ID)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/notes/"+note.ID+"/sync-state?target=obsidian", nil)

	GetNoteSyncState(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var body struct {
		Data model.NoteSyncStateCompatibilityResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Data.Flags.BindingMismatch {
		t.Fatalf("flags = %+v, want binding mismatch", body.Data.Flags)
	}
	if body.Data.Flags.BoundTargetID != notionTarget.ID || body.Data.Flags.BoundTargetName != notionTarget.Name {
		t.Fatalf("flags = %+v, want bound notion target", body.Data.Flags)
	}
	if body.Data.Target != nil && body.Data.Target.ID != obsidianTarget.ID {
		t.Fatalf("target = %+v, want nil or queried obsidian target", body.Data.Target)
	}
}

func TestGetNoteSyncStateReturnsDefaultTargetMissingWhenNoBindingAndNoDefault(t *testing.T) {
	openHandlerSyncStoreTestDB(t)
	note := insertHandlerNoteForTest(t, "No Default State", "Body\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/notes/"+note.ID+"/sync-state?target=notion", nil)

	GetNoteSyncState(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var body struct {
		Data model.NoteSyncStateCompatibilityResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Data.Flags.DefaultTargetMissing {
		t.Fatalf("flags = %+v, want default target missing", body.Data.Flags)
	}
	if body.Data.State != nil || body.Data.Target != nil {
		t.Fatalf("state/target = %+v/%+v, want nil", body.Data.State, body.Data.Target)
	}
}

func TestLegacyExecutionReturnsDefaultTargetMissingConflict(t *testing.T) {
	openHandlerSyncStoreTestDB(t)
	note := insertHandlerNoteForTest(t, "No Default Execute", "Body\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/obsidian/notes/"+note.ID, nil)

	SyncObsidianNote(c)

	assertSyncCompatibilityConflict(t, recorder, "default_target_missing")
}

func TestLegacyExecutionReturnsBindingMismatchConflict(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	notionTarget := saveHandlerNotionTarget(t)
	note := insertHandlerNoteForTest(t, "Mismatch Execute", "Body\n")
	putHandlerBinding(t, store, note.ID, notionTarget.ID)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/obsidian/notes/"+note.ID, nil)

	SyncObsidianNote(c)

	assertSyncCompatibilityConflict(t, recorder, "binding_mismatch")
}

func TestLegacyExecutionReturnsBindingMismatchWhenDefaultDiffersFromBinding(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	boundTarget := saveHandlerNotionTarget(t)
	defaultTarget := model.SyncTarget{
		Type:       "notion",
		Name:       "Default Notion",
		ConfigJSON: `{"data_source_id":"ds-default"}`,
		Enabled:    true,
		IsDefault:  true,
	}
	if err := repository.SaveSyncTarget(&defaultTarget); err != nil {
		t.Fatalf("save default target: %v", err)
	}
	note := insertHandlerNoteForTest(t, "Same Type Mismatch", "Body\n")
	putHandlerBinding(t, store, note.ID, boundTarget.ID)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/notion/notes/"+note.ID, nil)

	SyncNotionNote(c)

	assertSyncCompatibilityConflict(t, recorder, "binding_mismatch")
}

func TestLegacyBatchExecutionReturnsBindingMismatchConflict(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	saveHandlerObsidianTarget(t)
	notionTarget := saveHandlerNotionTarget(t)
	note := insertHandlerNoteForTest(t, "Batch Mismatch", "Body\n")
	putHandlerBinding(t, store, note.ID, notionTarget.ID)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/obsidian/all", nil)

	SyncObsidianAll(c)

	assertSyncCompatibilityConflict(t, recorder, "binding_mismatch")
}

func assertSyncCompatibilityConflict(t *testing.T, recorder *httptest.ResponseRecorder, code string) {
	t.Helper()
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	var body model.APIResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error == nil {
		t.Fatalf("error = nil; body = %s", recorder.Body.String())
	}
	if body.Error.Code != code {
		t.Fatalf("error code = %q, want %q; body = %s", body.Error.Code, code, recorder.Body.String())
	}
	if strings.TrimSpace(body.Error.Message) == "" {
		t.Fatalf("empty error message for %s", code)
	}
}

func syncBindingForTest(t *testing.T, store storage.Store, noteID string) *model.NoteSyncBinding {
	t.Helper()
	binding, err := store.Sync().GetBinding(t.Context(), noteID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	return binding
}
