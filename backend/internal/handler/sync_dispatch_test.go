package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestGetTargetDeletionsUsesTargetID(t *testing.T) {
	openHandlerSyncStoreTestDB(t)
	targetA := saveHandlerObsidianTargetNamed(t, "Handler Delete A", t.TempDir())
	targetB := saveHandlerObsidianTargetNamed(t, "Handler Delete B", t.TempDir())
	noteA := insertHandlerNoteForTest(t, "Handler Deleted A", "Body\n")
	noteB := insertHandlerNoteForTest(t, "Handler Deleted B", "Body\n")
	putHandlerExternalDeletedState(t, noteA.ID, targetA.ID, filepath.Join(targetA.VaultPath, targetA.BaseFolder, "A.md"))
	putHandlerExternalDeletedState(t, noteB.ID, targetB.ID, filepath.Join(targetB.VaultPath, targetB.BaseFolder, "B.md"))

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "target_id", Value: targetB.ID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/sync/targets/"+targetB.ID+"/deletions", nil)

	ListTargetDeletions(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var body struct {
		Data struct {
			Items []model.ExternalDeletedNote `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Data.Items) != 1 || body.Data.Items[0].NoteID != noteB.ID {
		t.Fatalf("items = %+v, want only %s", body.Data.Items, noteB.ID)
	}
}

func TestPostTargetDeletionConfirmUsesTargetID(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	target := saveHandlerObsidianTargetNamed(t, "Handler Confirm", t.TempDir())
	note := insertHandlerNoteForTest(t, "Handler Confirm", "Body\n")
	path := filepath.Join(target.VaultPath, target.BaseFolder, "Handler Confirm.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create base: %v", err)
	}
	putHandlerBinding(t, store, note.ID, target.ID)
	putHandlerExternalDeletedState(t, note.ID, target.ID, path)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "target_id", Value: target.ID}, {Key: "note_id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/targets/"+target.ID+"/deletions/"+note.ID+"/confirm", nil)

	ConfirmTargetDeletion(c)

	if c.Writer.Status() != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %s", c.Writer.Status(), http.StatusNoContent, recorder.Body.String())
	}
}

func TestPostTargetDeletionRestoreUsesTargetID(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	target := saveHandlerObsidianTargetNamed(t, "Handler Restore", t.TempDir())
	note := insertHandlerNoteForTest(t, "Handler Restore", "Body\n")
	path := filepath.Join(target.VaultPath, target.BaseFolder, "Handler Restore.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create base: %v", err)
	}
	putHandlerBinding(t, store, note.ID, target.ID)
	putHandlerExternalDeletedState(t, note.ID, target.ID, path)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "target_id", Value: target.ID}, {Key: "note_id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/targets/"+target.ID+"/deletions/"+note.ID+"/restore", nil)

	RestoreTargetDeletion(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("restored file: %v", err)
	}
}

func TestGetTargetNotionDeletionsUsesTargetID(t *testing.T) {
	openHandlerSyncStoreTestDB(t)
	targetA := saveHandlerNotionTargetNamed(t, "Handler Notion Delete A", `{"data_source_id":"ds-a"}`)
	targetB := saveHandlerNotionTargetNamed(t, "Handler Notion Delete B", `{"data_source_id":"ds-b"}`)
	noteA := insertHandlerNoteForTest(t, "Handler Notion Deleted A", "Body\n")
	noteB := insertHandlerNoteForTest(t, "Handler Notion Deleted B", "Body\n")
	putHandlerNotionExternalDeletedStateForDispatch(t, noteA.ID, targetA.ID, "page-a")
	putHandlerNotionExternalDeletedStateForDispatch(t, noteB.ID, targetB.ID, "page-b")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "target_id", Value: targetB.ID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/sync/targets/"+targetB.ID+"/deletions", nil)

	ListTargetDeletions(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var body struct {
		Data struct {
			Items []model.ExternalDeletedNote `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Data.Items) != 1 || body.Data.Items[0].NoteID != noteB.ID {
		t.Fatalf("items = %+v, want only %s", body.Data.Items, noteB.ID)
	}
}

func TestPostTargetNotionDeletionConfirmUsesTargetID(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	target := saveHandlerNotionTargetNamed(t, "Handler Notion Confirm", `{"data_source_id":"ds-confirm"}`)
	note := insertHandlerNoteForTest(t, "Handler Notion Confirm", "Body\n")
	putHandlerBinding(t, store, note.ID, target.ID)
	putHandlerNotionExternalDeletedStateForDispatch(t, note.ID, target.ID, "page-confirm")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "target_id", Value: target.ID}, {Key: "note_id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/targets/"+target.ID+"/deletions/"+note.ID+"/confirm", nil)

	ConfirmTargetDeletion(c)

	if c.Writer.Status() != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %s", c.Writer.Status(), http.StatusNoContent, recorder.Body.String())
	}
	if _, err := repository.GetNoteByID(note.ID); err == nil {
		t.Fatalf("note still exists after confirm")
	}
}

func TestPostTargetNotionDeletionRestoreUsesTargetID(t *testing.T) {
	store := openHandlerSyncStoreTestDB(t)
	t.Setenv("NOTION_PROVIDER", "mock")
	target := saveHandlerNotionTargetNamed(t, "Handler Notion Restore", `{"data_source_id":"ds-restore"}`)
	note := insertHandlerNoteForTest(t, "Handler Notion Restore", "Body\n")
	putHandlerBinding(t, store, note.ID, target.ID)
	putHandlerNotionExternalDeletedStateForDispatch(t, note.ID, target.ID, "page-restore")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "target_id", Value: target.ID}, {Key: "note_id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/targets/"+target.ID+"/deletions/"+note.ID+"/restore", nil)

	RestoreTargetDeletion(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var body struct {
		Data struct {
			Item model.SyncResultItem `json:"item"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.Item.ExternalID != "page-restore" {
		t.Fatalf("item = %+v", body.Data.Item)
	}
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

func saveHandlerObsidianTargetNamed(t *testing.T, name string, vault string) model.SyncTarget {
	t.Helper()
	target := &model.SyncTarget{
		Type:       "obsidian",
		Name:       name,
		VaultPath:  vault,
		BaseFolder: "FlowSpace Notes",
		ConfigJSON: "{}",
		Enabled:    true,
		IsDefault:  true,
	}
	if err := repository.SaveSyncTarget(target); err != nil {
		t.Fatalf("save obsidian target: %v", err)
	}
	return *target
}

func putHandlerExternalDeletedState(t *testing.T, noteID string, targetID string, externalPath string) {
	t.Helper()
	now := int64(1700000000)
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        noteID,
		TargetID:      targetID,
		ExternalPath:  externalPath,
		ContentHash:   "content",
		ExternalHash:  "external",
		LastDirection: "delete_detected",
		LastSyncedAt:  &now,
		Status:        "external_deleted",
	}); err != nil {
		t.Fatalf("upsert external deleted state: %v", err)
	}
}

func putHandlerNotionExternalDeletedStateForDispatch(t *testing.T, noteID string, targetID string, pageID string) {
	t.Helper()
	now := int64(1700000000)
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        noteID,
		TargetID:      targetID,
		ExternalPath:  "notion:" + pageID,
		ExternalID:    pageID,
		ExternalURL:   "https://www.notion.so/" + pageID,
		ContentHash:   "content",
		ExternalHash:  "external",
		LastDirection: "delete_detected",
		LastSyncedAt:  &now,
		Status:        "external_deleted",
	}); err != nil {
		t.Fatalf("upsert notion external deleted state: %v", err)
	}
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
