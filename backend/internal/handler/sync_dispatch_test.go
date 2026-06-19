package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

func TestPostUnifiedNoteSyncRequiresBinding(t *testing.T) {
	openHandlerSyncStoreTestDB(t)
	note := insertHandlerNoteForTest(t, "Unbound Sync", "Body\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/notes/"+note.ID, nil)

	SyncNote(c)

	assertSyncDispatchError(t, recorder, http.StatusConflict, "binding_required")
}

func TestPostTargetPushReturnsTargetNotFound(t *testing.T) {
	openHandlerSyncStoreTestDB(t)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "target_id", Value: "missing-target"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/targets/missing-target/push", nil)

	SyncTargetPush(c)

	assertSyncDispatchError(t, recorder, http.StatusNotFound, "sync_target_not_found")
}

func TestPostTargetPullReturnsConflictForForeignBinding(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	targetA := saveHandlerNotionTargetNamed(t, "Handler Target A", `{"data_source_id":"ds-a","required_tags":["sync"]}`)
	targetB := saveHandlerNotionTargetNamed(t, "Handler Target B", `{"data_source_id":"ds-b","required_tags":["sync"]}`)
	note := insertHandlerTaggedNoteForTest(t, "Foreign Pull", "Body\n", `["sync"]`)
	putHandlerBinding(t, store, note.ID, targetA.ID)
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      targetB.ID,
		ExternalPath:  "notion:page-b",
		ExternalID:    "page-b",
		ContentHash:   "old-local",
		ExternalHash:  "old-remote",
		LastDirection: "pull",
		Status:        "synced",
	}); err != nil {
		t.Fatalf("upsert sync state: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "target_id", Value: targetB.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/targets/"+targetB.ID+"/pull", nil)

	SyncTargetPull(c)

	assertSyncDispatchError(t, recorder, http.StatusConflict, "binding_mismatch")
}

func saveHandlerNotionTargetNamed(t *testing.T, name string, configJSON string) model.SyncTarget {
	t.Helper()
	target := &model.SyncTarget{
		Type:       "notion",
		Name:       name,
		ConfigJSON: configJSON,
		Enabled:    true,
		IsDefault:  true,
	}
	if err := repository.SaveSyncTarget(target); err != nil {
		t.Fatalf("save notion target: %v", err)
	}
	return *target
}

func assertSyncDispatchError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, status, recorder.Body.String())
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
}
