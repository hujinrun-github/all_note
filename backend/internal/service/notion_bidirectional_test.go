package service

import (
	"database/sql"
	"os"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	_ "modernc.org/sqlite"
)

type fakeNotionGateway struct {
	pages    []notionRemoteNote
	created  []string
	updated  []string
	restored []string
}

func (fake *fakeNotionGateway) TestDataSource(config notionTargetConfig) error { return nil }

func (fake *fakeNotionGateway) QueryRemoteNotes(config notionTargetConfig) ([]notionRemoteNote, error) {
	return fake.pages, nil
}

func (fake *fakeNotionGateway) CreateRemoteNote(config notionTargetConfig, note *model.Note) (notionRemoteNote, error) {
	fake.created = append(fake.created, note.ID)
	return notionRemoteNote{
		PageID:       "created-" + note.ID,
		URL:          "https://www.notion.so/created-" + note.ID,
		Title:        note.Title,
		Markdown:     note.Body,
		Hash:         notionMarkdownHash(note.Body),
		LastEditedAt: 1900000000,
		FlowSpaceID:  note.ID,
	}, nil
}

func (fake *fakeNotionGateway) UpdateRemoteNote(config notionTargetConfig, pageID string, note *model.Note) (notionRemoteNote, error) {
	fake.updated = append(fake.updated, pageID)
	return notionRemoteNote{
		PageID:       pageID,
		URL:          "https://www.notion.so/" + pageID,
		Title:        note.Title,
		Markdown:     note.Body,
		Hash:         notionMarkdownHash(note.Body),
		LastEditedAt: 1900000001,
		FlowSpaceID:  note.ID,
	}, nil
}

func (fake *fakeNotionGateway) RestoreRemoteNote(config notionTargetConfig, note *model.Note, previous notionSyncStateSnapshot) (notionRemoteNote, error) {
	fake.restored = append(fake.restored, note.ID)
	return fake.CreateRemoteNote(config, note)
}

func TestSyncNotionImportsNewRemotePage(t *testing.T) {
	openServiceSyncTestDB(t)
	target := saveNotionTargetForTest(t)
	gateway := &fakeNotionGateway{
		pages: []notionRemoteNote{{
			PageID:       "page-1",
			URL:          "https://www.notion.so/page-1",
			Title:        "Remote Only",
			Markdown:     "Remote body\n",
			Hash:         notionMarkdownHash("Remote body\n"),
			LastEditedAt: 1800000000,
		}},
	}

	result := NewNotionSyncService(gateway).SyncBidirectional(target)

	if result.Imported != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}
	notes, err := repository.ListAllNotes()
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 1 || notes[0].Title != "Remote Only" || notes[0].Body != "Remote body\n" {
		t.Fatalf("notes = %+v", notes)
	}
}

func TestSyncNotionPullsRemoteChangeOverLocalChange(t *testing.T) {
	openServiceSyncTestDB(t)
	target := saveNotionTargetForTest(t)
	note := insertServiceNoteForTest(t, "Local Title", "Local changed\n")
	lastSync := int64(1700000000)
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  "notion:page-1",
		ExternalID:    "page-1",
		ExternalURL:   "https://www.notion.so/page-1",
		ContentHash:   notionMarkdownHash("Old local\n"),
		ExternalHash:  notionMarkdownHash("Old remote\n"),
		ExternalMTime: &lastSync,
		LastSyncedAt:  &lastSync,
		LastDirection: "push",
		Status:        "synced",
	}); err != nil {
		t.Fatalf("upsert state: %v", err)
	}
	gateway := &fakeNotionGateway{
		pages: []notionRemoteNote{{
			PageID:       "page-1",
			URL:          "https://www.notion.so/page-1",
			Title:        "Remote Wins",
			Markdown:     "Remote changed\n",
			Hash:         notionMarkdownHash("Remote changed\n"),
			LastEditedAt: 1800000000,
			FlowSpaceID:  note.ID,
		}},
	}

	result := NewNotionSyncService(gateway).SyncBidirectional(target)

	if result.ConflictPulled != 1 {
		t.Fatalf("result = %+v", result)
	}
	got, err := repository.GetNoteByID(note.ID)
	if err != nil {
		t.Fatalf("get note: %v", err)
	}
	if got.Title != "Remote Wins" || got.Body != "Remote changed\n" {
		t.Fatalf("note = %+v", got)
	}
}

func TestSyncNotionPushesLocalChangeWhenRemoteUnchanged(t *testing.T) {
	openServiceSyncTestDB(t)
	target := saveNotionTargetForTest(t)
	note := insertServiceNoteForTest(t, "Local Push", "Local changed\n")
	lastSync := int64(1700000000)
	remoteHash := notionMarkdownHash("Old body\n")
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  "notion:page-1",
		ExternalID:    "page-1",
		ExternalURL:   "https://www.notion.so/page-1",
		ContentHash:   notionMarkdownHash("Old body\n"),
		ExternalHash:  remoteHash,
		ExternalMTime: &lastSync,
		LastSyncedAt:  &lastSync,
		LastDirection: "pull",
		Status:        "synced",
	}); err != nil {
		t.Fatalf("upsert state: %v", err)
	}
	gateway := &fakeNotionGateway{
		pages: []notionRemoteNote{{
			PageID:       "page-1",
			URL:          "https://www.notion.so/page-1",
			Title:        "Local Push",
			Markdown:     "Old body\n",
			Hash:         remoteHash,
			LastEditedAt: lastSync,
			FlowSpaceID:  note.ID,
		}},
	}

	result := NewNotionSyncService(gateway).SyncBidirectional(target)

	if result.Pushed != 1 || len(gateway.updated) != 1 || gateway.updated[0] != "page-1" {
		t.Fatalf("result = %+v updated = %#v", result, gateway.updated)
	}
}

func TestSyncNotionMarksMissingRemoteAsExternalDeleted(t *testing.T) {
	openServiceSyncTestDB(t)
	target := saveNotionTargetForTest(t)
	note := insertServiceNoteForTest(t, "Deleted Remote", "Body\n")
	lastSync := int64(1700000000)
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  "notion:page-deleted",
		ExternalID:    "page-deleted",
		ExternalURL:   "https://www.notion.so/page-deleted",
		ContentHash:   notionMarkdownHash("Body\n"),
		ExternalHash:  notionMarkdownHash("Body\n"),
		ExternalMTime: &lastSync,
		LastSyncedAt:  &lastSync,
		LastDirection: "push",
		Status:        "synced",
	}); err != nil {
		t.Fatalf("upsert state: %v", err)
	}

	result := NewNotionSyncService(&fakeNotionGateway{}).SyncBidirectional(target)

	if result.ExternalDeleted != 1 {
		t.Fatalf("result = %+v", result)
	}
	state, err := repository.GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state.Status != "external_deleted" || state.LastDirection != "delete_detected" {
		t.Fatalf("state = %+v", state)
	}
}

func TestSyncNotionBidirectionalUsesMockProviderWithoutToken(t *testing.T) {
	openServiceSyncTestDB(t)
	saveNotionTargetForTest(t)
	t.Setenv("NOTION_PROVIDER", "mock")
	t.Setenv("FLOWSPACE_NOTION_TOKEN", "")

	result := SyncNotionBidirectional()

	if result.Imported != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}
	notes, err := repository.ListAllNotes()
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 1 || notes[0].Title != "Mock Notion Note" || notes[0].Body != "Imported from mock Notion.\n" {
		t.Fatalf("notes = %+v", notes)
	}
}

func openServiceSyncTestDB(t *testing.T) {
	t.Helper()
	repositoryTestDB(t)
}

func repositoryTestDB(t *testing.T) {
	t.Helper()
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

func saveNotionTargetForTest(t *testing.T) model.SyncTarget {
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

func insertServiceNoteForTest(t *testing.T, title, body string) model.Note {
	t.Helper()
	note := &model.Note{Title: title, Body: body, FolderID: "__uncategorized", Tags: "[]"}
	if err := repository.CreateNote(note); err != nil {
		t.Fatalf("create note: %v", err)
	}
	return *note
}
