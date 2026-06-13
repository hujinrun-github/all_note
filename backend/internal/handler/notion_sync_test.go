package handler

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	_ "modernc.org/sqlite"
)

func TestSyncTargetFromRequestPreservesNotionTypeAndConfig(t *testing.T) {
	req := &model.SaveSyncTargetRequest{
		Type:       "notion",
		Name:       "Personal Notion",
		ConfigJSON: `{"data_source_id":"ds-123","title_property":"Name"}`,
		Enabled:    true,
		AutoSync:   false,
	}

	target := syncTargetFromRequest(req)

	if target.Type != "notion" {
		t.Fatalf("type = %q, want notion", target.Type)
	}
	if target.ConfigJSON != `{"data_source_id":"ds-123","title_property":"Name"}` {
		t.Fatalf("config_json = %q", target.ConfigJSON)
	}
	if target.VaultPath != "" || target.BaseFolder != "" {
		t.Fatalf("notion target should not require local path fields: %+v", target)
	}
}

func TestSyncTargetFromRequestDefaultsTypeToObsidian(t *testing.T) {
	req := &model.SaveSyncTargetRequest{
		Name:       "Local Vault",
		VaultPath:  "D:\\Vault",
		BaseFolder: "FlowSpace Notes",
		Enabled:    true,
	}

	target := syncTargetFromRequest(req)

	if target.Type != "obsidian" {
		t.Fatalf("type = %q, want obsidian", target.Type)
	}
	if target.ConfigJSON != "{}" {
		t.Fatalf("config_json = %q, want {}", target.ConfigJSON)
	}
}

func TestValidateSyncTargetRequestRejectsObsidianWithoutPaths(t *testing.T) {
	req := &model.SaveSyncTargetRequest{
		Name: "Local Vault",
	}

	target := syncTargetFromRequest(req)

	err := validateSyncTargetRequest(target)
	if err == nil {
		t.Fatal("expected validation error for obsidian target without paths")
	}
	if err.Error() != "obsidian sync target requires vault_path and base_folder" {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestValidateSyncTargetRequestAllowsNotionWithoutPaths(t *testing.T) {
	req := &model.SaveSyncTargetRequest{
		Type:       "notion",
		Name:       "Personal Notion",
		ConfigJSON: `{"data_source_id":"ds-123","title_property":"Name"}`,
	}

	target := syncTargetFromRequest(req)

	if err := validateSyncTargetRequest(target); err != nil {
		t.Fatalf("validation error = %v", err)
	}
}

func TestValidateSyncTargetRequestRejectsSaveObsidianWithoutPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/targets", bytes.NewBufferString(`{"name":"Local Vault"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	SaveSyncTarget(c)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "obsidian sync target requires vault_path and base_folder") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestValidateSyncTargetRequestRejectsUpdateObsidianWithoutPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "target-1"}}
	c.Request = httptest.NewRequest(http.MethodPut, "/sync/targets/target-1", bytes.NewBufferString(`{"name":"Local Vault"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	UpdateSyncTarget(c)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "obsidian sync target requires vault_path and base_folder") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestNotionTargetWithMockProviderForcesNotionEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("NOTION_PROVIDER", "mock")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/notion/test", bytes.NewBufferString(`{
		"type":"obsidian",
		"name":"Personal Notion",
		"config_json":"{\"data_source_id\":\"ds-123\"}",
		"enabled":false
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	TestNotionTarget(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var body struct {
		Data struct {
			OK bool `json:"ok"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Data.OK {
		t.Fatalf("ok = false; body = %s", recorder.Body.String())
	}
}

func TestSyncNotionNoteWithMockProviderPushesSingleLocalNote(t *testing.T) {
	openHandlerSyncTestDB(t)
	t.Setenv("NOTION_PROVIDER", "mock")
	target := saveHandlerNotionTarget(t)
	note := insertHandlerNoteForTest(t, "Local Notion", "Local body\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/notion/notes/"+note.ID, nil)

	SyncNotionNote(c)

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
	if body.Data.Item.Status != "pushed" {
		t.Fatalf("item = %+v", body.Data.Item)
	}
	if body.Data.Item.ExternalID != "mock-created-"+note.ID {
		t.Fatalf("external id = %q", body.Data.Item.ExternalID)
	}
	state, err := repository.GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get sync state: %v", err)
	}
	if state.Status != "synced" || state.ExternalID != "mock-created-"+note.ID {
		t.Fatalf("state = %+v", state)
	}
}

func TestSyncNotionNoteMissingNoteReturnsNotFound(t *testing.T) {
	openHandlerSyncTestDB(t)
	t.Setenv("NOTION_PROVIDER", "mock")
	saveHandlerNotionTarget(t)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "missing-note"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/notion/notes/missing-note", nil)

	SyncNotionNote(c)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
}

func TestSyncNotionNoteEmptyIDReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: ""}}
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/notion/notes/", nil)

	SyncNotionNote(c)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestListNotionDeletionsReturnsExternalDeletedStates(t *testing.T) {
	openHandlerSyncTestDB(t)
	target := saveHandlerNotionTarget(t)
	note := insertHandlerNoteForTest(t, "Deleted In Notion", "Body\n")
	insertHandlerNotionExternalDeletedState(t, note.ID, target.ID)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/sync/notion/deletions", nil)

	ListNotionDeletions(c)

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
	if len(body.Data.Items) != 1 || body.Data.Items[0].NoteID != note.ID || body.Data.Items[0].ExternalPath != "notion:page-deleted" {
		t.Fatalf("items = %+v", body.Data.Items)
	}
}

func TestListNotionDeletionsWithoutTargetReturnsNotFound(t *testing.T) {
	openHandlerSyncTestDB(t)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/sync/notion/deletions", nil)

	ListNotionDeletions(c)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
}

func TestConfirmNotionDeletionDeletesFlowSpaceNoteAndState(t *testing.T) {
	openHandlerSyncTestDB(t)
	target := saveHandlerNotionTarget(t)
	note := insertHandlerNoteForTest(t, "Confirm Notion Delete", "Body\n")
	insertHandlerNotionExternalDeletedState(t, note.ID, target.ID)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "note_id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/notion/deletions/"+note.ID+"/confirm", nil)

	ConfirmNotionDeletion(c)

	if c.Writer.Status() != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %s", c.Writer.Status(), http.StatusNoContent, recorder.Body.String())
	}
	if _, err := repository.GetNoteByID(note.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get note error = %v, want sql.ErrNoRows", err)
	}
	if _, err := repository.GetSyncState(note.ID, target.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get sync state error = %v, want sql.ErrNoRows", err)
	}
}

func TestRestoreNotionDeletionWithMockProviderMarksStateSynced(t *testing.T) {
	openHandlerSyncTestDB(t)
	t.Setenv("NOTION_PROVIDER", "mock")
	target := saveHandlerNotionTarget(t)
	note := insertHandlerNoteForTest(t, "Restore Notion Delete", "Body\n")
	insertHandlerNotionExternalDeletedState(t, note.ID, target.ID)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "note_id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/notion/deletions/"+note.ID+"/restore", nil)

	RestoreNotionDeletion(c)

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
	if body.Data.Item.Status != "synced" || body.Data.Item.ExternalID != "page-deleted" {
		t.Fatalf("item = %+v", body.Data.Item)
	}
	state, err := repository.GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get sync state: %v", err)
	}
	if state.Status != "synced" || state.ExternalID != "page-deleted" {
		t.Fatalf("state = %+v", state)
	}
}

func TestConfirmNotionDeletionRejectsNonDeletedState(t *testing.T) {
	openHandlerSyncTestDB(t)
	target := saveHandlerNotionTarget(t)
	note := insertHandlerNoteForTest(t, "Not Deleted", "Body\n")
	now := int64(1700000000)
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  "notion:page-live",
		ExternalID:    "page-live",
		ExternalURL:   "https://www.notion.so/page-live",
		ContentHash:   "content",
		ExternalHash:  "external",
		ExternalMTime: &now,
		LastSyncedAt:  &now,
		LastDirection: "push",
		Status:        "synced",
	}); err != nil {
		t.Fatalf("upsert state: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "note_id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/notion/deletions/"+note.ID+"/confirm", nil)

	ConfirmNotionDeletion(c)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func openHandlerSyncTestDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)

	schema, err := os.ReadFile("../../db/schema.sql")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("exec schema: %v", err)
	}

	repository.DB = db
	t.Cleanup(func() {
		repository.DB = nil
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	})
}

func saveHandlerNotionTarget(t *testing.T) model.SyncTarget {
	t.Helper()
	target := &model.SyncTarget{
		Type:       "notion",
		Name:       "Personal Notion",
		ConfigJSON: `{"data_source_id":"ds-123"}`,
		Enabled:    true,
	}
	if err := repository.SaveSyncTarget(target); err != nil {
		t.Fatalf("save target: %v", err)
	}
	return *target
}

func insertHandlerNoteForTest(t *testing.T, title, body string) model.Note {
	t.Helper()
	note := &model.Note{Title: title, Body: body, FolderID: "__uncategorized", Tags: "[]"}
	if err := repository.CreateNote(note); err != nil {
		t.Fatalf("create note: %v", err)
	}
	return *note
}

func insertHandlerNotionExternalDeletedState(t *testing.T, noteID, targetID string) {
	t.Helper()
	now := int64(1700000000)
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        noteID,
		TargetID:      targetID,
		ExternalPath:  "notion:page-deleted",
		ExternalID:    "page-deleted",
		ExternalURL:   "https://www.notion.so/page-deleted",
		ContentHash:   "content",
		ExternalHash:  "external",
		ExternalMTime: &now,
		LastSyncedAt:  &now,
		LastDirection: "delete_detected",
		Status:        "external_deleted",
	}); err != nil {
		t.Fatalf("upsert external deleted state: %v", err)
	}
}
