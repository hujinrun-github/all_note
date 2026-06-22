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

func TestSaveSyncTargetDefaultsNewTargetToDefaultWhenOmitted(t *testing.T) {
	openHandlerSyncTestDB(t)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/targets", bytes.NewBufferString(`{
		"type":"notion",
		"name":"Personal Notion",
		"config_json":"{\"data_source_id\":\"ds-123\"}",
		"enabled":true,
		"auto_sync":false
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	SaveSyncTarget(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	target, err := repository.GetDefaultSyncTarget("notion")
	if err != nil {
		t.Fatalf("new target should be default: %v", err)
	}
	if target.Name != "Personal Notion" || !target.IsDefault {
		t.Fatalf("unexpected default target: %+v", target)
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

func TestPatchSyncTargetAllowsDisplayFieldsWhenUsed(t *testing.T) {
	openHandlerSyncTestDB(t)
	target := saveHandlerObsidianTarget(t)
	note := insertHandlerNoteForTest(t, "Locked Display Update", "Body\n")
	insertHandlerSyncBinding(t, note.ID, target.ID)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: target.ID}}
	c.Request = httptest.NewRequest(http.MethodPatch, "/sync/targets/"+target.ID, bytes.NewBufferString(`{
		"type":"obsidian",
		"name":"Renamed Vault",
		"vault_path":"D:/Vault",
		"base_folder":"FlowSpace Notes",
		"config_json":"{\"theme\":\"dark\"}",
		"enabled":false,
		"auto_sync":true
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	UpdateSyncTarget(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	updated, err := repository.GetSyncTarget(target.ID)
	if err != nil {
		t.Fatalf("get updated target: %v", err)
	}
	if updated.Name != "Renamed Vault" || updated.Enabled || !updated.AutoSync || !updated.IsDefault {
		t.Fatalf("updated display fields = %+v", updated)
	}
	if updated.VaultPath != "D:/Vault" || updated.BaseFolder != "FlowSpace Notes" {
		t.Fatalf("identity fields changed unexpectedly: %+v", updated)
	}
}

func TestPatchSyncTargetRejectsObsidianIdentityChangeWhenUsed(t *testing.T) {
	openHandlerSyncTestDB(t)
	target := saveHandlerObsidianTarget(t)
	note := insertHandlerNoteForTest(t, "Locked Obsidian Identity", "Body\n")
	insertHandlerSyncBinding(t, note.ID, target.ID)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: target.ID}}
	c.Request = httptest.NewRequest(http.MethodPatch, "/sync/targets/"+target.ID, bytes.NewBufferString(`{
		"type":"obsidian",
		"name":"Local Vault",
		"vault_path":"D:/OtherVault",
		"base_folder":"FlowSpace Notes",
		"config_json":"{}",
		"enabled":true
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	UpdateSyncTarget(c)

	assertTargetIdentityLocked(t, recorder)
	unchanged, err := repository.GetSyncTarget(target.ID)
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	if unchanged.VaultPath != target.VaultPath {
		t.Fatalf("vault_path = %q, want %q", unchanged.VaultPath, target.VaultPath)
	}
}

func TestPatchSyncTargetRejectsNotionDataSourceChangeWhenUsed(t *testing.T) {
	openHandlerSyncTestDB(t)
	target := saveHandlerNotionTarget(t)
	note := insertHandlerNoteForTest(t, "Locked Notion Identity", "Body\n")
	insertHandlerSyncBinding(t, note.ID, target.ID)
	insertHandlerExternalClaim(t, note.ID, target.ID)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: target.ID}}
	c.Request = httptest.NewRequest(http.MethodPatch, "/sync/targets/"+target.ID, bytes.NewBufferString(`{
		"type":"notion",
		"name":"Personal Notion",
		"config_json":"{\"data_source_id\":\"ds-other\",\"title_property\":\"Name\"}",
		"enabled":true
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	UpdateSyncTarget(c)

	assertTargetIdentityLocked(t, recorder)
	unchanged, err := repository.GetSyncTarget(target.ID)
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	if unchanged.ConfigJSON != target.ConfigJSON {
		t.Fatalf("config_json = %q, want %q", unchanged.ConfigJSON, target.ConfigJSON)
	}
}

func TestPatchSyncTargetRejectsIdentityChangeWhenUsedByState(t *testing.T) {
	openHandlerSyncTestDB(t)
	target := saveHandlerNotionTarget(t)
	note := insertHandlerNoteForTest(t, "Locked State Identity", "Body\n")
	insertHandlerSyncedState(t, note.ID, target.ID)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: target.ID}}
	c.Request = httptest.NewRequest(http.MethodPatch, "/sync/targets/"+target.ID, bytes.NewBufferString(`{
		"type":"notion",
		"name":"Personal Notion",
		"config_json":"{\"data_source_id\":\"ds-state-other\"}",
		"enabled":true
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	UpdateSyncTarget(c)

	assertTargetIdentityLocked(t, recorder)
}

func TestDeleteSyncTargetRejectsBoundTarget(t *testing.T) {
	openHandlerSyncTestDB(t)
	target := saveHandlerObsidianTarget(t)
	note := insertHandlerNoteForTest(t, "Delete Bound Target", "Body\n")
	insertHandlerSyncBinding(t, note.ID, target.ID)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: target.ID}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/sync/targets/"+target.ID, nil)

	DeleteSyncTarget(c)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	if _, err := repository.GetSyncTarget(target.ID); err != nil {
		t.Fatalf("target should remain: %v", err)
	}
}

func TestDeleteSyncTargetDeletesUnusedTarget(t *testing.T) {
	openHandlerSyncTestDB(t)
	target := saveHandlerNotionTarget(t)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: target.ID}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/sync/targets/"+target.ID, nil)

	DeleteSyncTarget(c)

	if c.Writer.Status() != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %s", c.Writer.Status(), http.StatusNoContent, recorder.Body.String())
	}
	if _, err := repository.GetSyncTarget(target.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get target error = %v, want sql.ErrNoRows", err)
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
	target.ConfigJSON = `{"data_source_id":"ds-123","required_tags":["sync"]}`
	if err := repository.SaveSyncTarget(&target); err != nil {
		t.Fatalf("save target config: %v", err)
	}
	note := insertHandlerTaggedNoteForTest(t, "Local Notion", "Local body\n", `["sync"]`)

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

func TestGetNoteSyncStateDefaultsToObsidianTarget(t *testing.T) {
	openHandlerSyncTestDB(t)
	obsidianTarget := saveHandlerObsidianTarget(t)
	notionTarget := saveHandlerNotionTarget(t)
	note := insertHandlerNoteForTest(t, "Default Sync State", "Body\n")
	now := int64(1800000000)

	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      obsidianTarget.ID,
		ExternalPath:  "FlowSpace Notes/Default Sync State.md",
		ContentHash:   "obsidian-content",
		ExternalHash:  "obsidian-external",
		ExternalMTime: &now,
		LastSyncedAt:  &now,
		LastDirection: "push",
		Status:        "synced",
	}); err != nil {
		t.Fatalf("upsert obsidian state: %v", err)
	}
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      notionTarget.ID,
		ExternalPath:  "notion:page-default",
		ExternalID:    "page-default",
		ExternalURL:   "https://www.notion.so/page-default",
		ContentHash:   "notion-content",
		ExternalHash:  "notion-external",
		ExternalMTime: &now,
		LastSyncedAt:  &now,
		LastDirection: "pull",
		Status:        "synced",
	}); err != nil {
		t.Fatalf("upsert notion state: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/notes/"+note.ID+"/sync-state", nil)

	GetNoteSyncState(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var body struct {
		Data struct {
			State *model.SyncState `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.State == nil {
		t.Fatalf("state = nil; body = %s", recorder.Body.String())
	}
	if body.Data.State.TargetID != obsidianTarget.ID || body.Data.State.ExternalPath != "FlowSpace Notes/Default Sync State.md" {
		t.Fatalf("state = %+v, want obsidian target %s", body.Data.State, obsidianTarget.ID)
	}
}

func TestGetNoteSyncStateHonorsNotionTargetQuery(t *testing.T) {
	openHandlerSyncTestDB(t)
	obsidianTarget := saveHandlerObsidianTarget(t)
	notionTarget := saveHandlerNotionTarget(t)
	note := insertHandlerNoteForTest(t, "Targeted Sync State", "Body\n")
	now := int64(1800000000)

	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      obsidianTarget.ID,
		ExternalPath:  "FlowSpace Notes/Targeted Sync State.md",
		ContentHash:   "obsidian-content",
		ExternalHash:  "obsidian-external",
		ExternalMTime: &now,
		LastSyncedAt:  &now,
		LastDirection: "push",
		Status:        "synced",
	}); err != nil {
		t.Fatalf("upsert obsidian state: %v", err)
	}
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      notionTarget.ID,
		ExternalPath:  "notion:page-1",
		ExternalID:    "page-1",
		ExternalURL:   "https://www.notion.so/page-1",
		ContentHash:   "notion-content",
		ExternalHash:  "notion-external",
		ExternalMTime: &now,
		LastSyncedAt:  &now,
		LastDirection: "pull",
		Status:        "synced",
	}); err != nil {
		t.Fatalf("upsert notion state: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: note.ID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/notes/"+note.ID+"/sync-state?target=notion", nil)

	GetNoteSyncState(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var body struct {
		Data struct {
			State *model.SyncState `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.State == nil {
		t.Fatalf("state = nil; body = %s", recorder.Body.String())
	}
	if body.Data.State.TargetID != notionTarget.ID || body.Data.State.ExternalID != "page-1" {
		t.Fatalf("state = %+v, want notion target %s", body.Data.State, notionTarget.ID)
	}
}

func TestGetNoteSyncStateRejectsUnsupportedTarget(t *testing.T) {
	openHandlerSyncTestDB(t)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "note-1"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/notes/note-1/sync-state?target=dropbox", nil)

	GetNoteSyncState(c)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "unsupported sync target") {
		t.Fatalf("body = %s", recorder.Body.String())
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
		IsDefault:  true,
	}
	if err := repository.SaveSyncTarget(target); err != nil {
		t.Fatalf("save target: %v", err)
	}
	return *target
}

func saveHandlerObsidianTarget(t *testing.T) model.SyncTarget {
	t.Helper()
	target := &model.SyncTarget{
		Type:       "obsidian",
		Name:       "Local Vault",
		VaultPath:  "D:\\Vault",
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

func insertHandlerSyncBinding(t *testing.T, noteID, targetID string) {
	t.Helper()
	if _, err := repository.DB.Exec(`
		INSERT INTO note_sync_bindings (note_id, target_id, created_at, updated_at)
		VALUES (?, ?, 1700000000, 1700000000)
	`, noteID, targetID); err != nil {
		t.Fatalf("insert sync binding: %v", err)
	}
}

func insertHandlerExternalClaim(t *testing.T, noteID, targetID string) {
	t.Helper()
	if _, err := repository.DB.Exec(`
		INSERT INTO sync_external_claims (external_key, note_id, target_id, external_type, external_id, created_at, updated_at)
		VALUES (?, ?, ?, 'notion_page', 'page-locked', 1700000000, 1700000000)
	`, "notion:page-locked", noteID, targetID); err != nil {
		t.Fatalf("insert external claim: %v", err)
	}
}

func insertHandlerSyncedState(t *testing.T, noteID, targetID string) {
	t.Helper()
	now := int64(1700000000)
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        noteID,
		TargetID:      targetID,
		ExternalPath:  "notion:page-state",
		ExternalID:    "page-state",
		ContentHash:   "content",
		LastDirection: "push",
		LastSyncedAt:  &now,
		Status:        "synced",
	}); err != nil {
		t.Fatalf("upsert sync state: %v", err)
	}
}

func assertTargetIdentityLocked(t *testing.T, recorder *httptest.ResponseRecorder) {
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
	if body.Error.Code != "target_identity_locked" {
		t.Fatalf("error code = %q, want target_identity_locked; body = %s", body.Error.Code, recorder.Body.String())
	}
	if !strings.Contains(body.Error.Message, "同步目标") {
		t.Fatalf("message should be Chinese, got %q", body.Error.Message)
	}
}

func insertHandlerNoteForTest(t *testing.T, title, body string) model.Note {
	t.Helper()
	return insertHandlerTaggedNoteForTest(t, title, body, "[]")
}

func insertHandlerTaggedNoteForTest(t *testing.T, title, body, tags string) model.Note {
	t.Helper()
	note := &model.Note{Title: title, Body: body, FolderID: "__uncategorized", Tags: tags}
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
