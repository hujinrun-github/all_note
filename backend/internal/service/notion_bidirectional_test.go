package service

import (
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	_ "modernc.org/sqlite"
)

type fakeNotionGateway struct {
	pages      []notionRemoteNote
	created    []string
	updated    []string
	restored   []string
	restoreErr error
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
		Hash:         notionTitleBodyHash(note.Title, note.Body),
		LastEditedAt: 1900000000,
		FlowSpaceID:  note.ID,
		Tags:         parseTags(note.Tags),
	}, nil
}

func (fake *fakeNotionGateway) UpdateRemoteNote(config notionTargetConfig, pageID string, note *model.Note) (notionRemoteNote, error) {
	fake.updated = append(fake.updated, pageID)
	return notionRemoteNote{
		PageID:       pageID,
		URL:          "https://www.notion.so/" + pageID,
		Title:        note.Title,
		Markdown:     note.Body,
		Hash:         notionTitleBodyHash(note.Title, note.Body),
		LastEditedAt: 1900000001,
		FlowSpaceID:  note.ID,
		Tags:         parseTags(note.Tags),
	}, nil
}

func (fake *fakeNotionGateway) RestoreRemoteNote(config notionTargetConfig, note *model.Note, previous notionSyncStateSnapshot) (notionRemoteNote, error) {
	if fake.restoreErr != nil {
		return notionRemoteNote{}, fake.restoreErr
	}
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
			Hash:         notionTitleBodyHash("Remote Only", "Remote body\n"),
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
		ContentHash:   notionTitleBodyHash("Local Title", "Old local\n"),
		ExternalHash:  notionTitleBodyHash("Old Remote", "Old remote\n"),
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
			Hash:         notionTitleBodyHash("Remote Wins", "Remote changed\n"),
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

func TestSyncNotionPullsRemoteTitleOnlyChangeBeforeLocalPush(t *testing.T) {
	openServiceSyncTestDB(t)
	target := saveNotionTargetForTest(t)
	note := insertServiceNoteForTest(t, "Local Title", "Same body\n")
	lastSync := int64(1700000000)
	bodyOnlyHash := notionMarkdownHash("Same body\n")
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  "notion:page-title",
		ExternalID:    "page-title",
		ExternalURL:   "https://www.notion.so/page-title",
		ContentHash:   bodyOnlyHash,
		ExternalHash:  bodyOnlyHash,
		ExternalMTime: &lastSync,
		LastSyncedAt:  &lastSync,
		LastDirection: "pull",
		Status:        "synced",
	}); err != nil {
		t.Fatalf("upsert state: %v", err)
	}
	gateway := &fakeNotionGateway{
		pages: []notionRemoteNote{{
			PageID:       "page-title",
			URL:          "https://www.notion.so/page-title",
			Title:        "Remote Title",
			Markdown:     "Same body\n",
			LastEditedAt: 1800000000,
			FlowSpaceID:  note.ID,
		}},
	}

	result := NewNotionSyncService(gateway).SyncBidirectional(target)

	if result.Pulled != 1 || result.ConflictPulled != 1 || result.Pushed != 0 || len(gateway.updated) != 0 {
		t.Fatalf("result = %+v updated = %#v", result, gateway.updated)
	}
	got, err := repository.GetNoteByID(note.ID)
	if err != nil {
		t.Fatalf("get note: %v", err)
	}
	if got.Title != "Remote Title" || got.Body != "Same body\n" {
		t.Fatalf("note = %+v", got)
	}
	state, err := repository.GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get sync state: %v", err)
	}
	if state.ContentHash == bodyOnlyHash || state.ExternalHash == bodyOnlyHash {
		t.Fatalf("state hashes should include title, got %+v", state)
	}
}

func TestSyncNotionPushesLocalChangeWhenRemoteUnchanged(t *testing.T) {
	openServiceSyncTestDB(t)
	target := saveNotionTargetForTest(t)
	note := insertServiceNoteForTest(t, "Local Push", "Local changed\n")
	lastSync := int64(1700000000)
	remoteHash := notionTitleBodyHash("Local Push", "Old body\n")
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  "notion:page-1",
		ExternalID:    "page-1",
		ExternalURL:   "https://www.notion.so/page-1",
		ContentHash:   notionTitleBodyHash("Local Push", "Old body\n"),
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

func TestSyncNotionPullDoesNotPushLocalOnlyChange(t *testing.T) {
	openServiceSyncTestDB(t)
	target := saveNotionTargetForTest(t)
	note := insertServiceNoteForTest(t, "Local Pull Guard", "Local changed\n")
	lastSync := int64(1700000000)
	remoteHash := notionTitleBodyHash("Local Pull Guard", "Old body\n")
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  "notion:page-pull-guard",
		ExternalID:    "page-pull-guard",
		ExternalURL:   "https://www.notion.so/page-pull-guard",
		ContentHash:   notionTitleBodyHash("Local Pull Guard", "Old body\n"),
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
			PageID:       "page-pull-guard",
			URL:          "https://www.notion.so/page-pull-guard",
			Title:        "Local Pull Guard",
			Markdown:     "Old body\n",
			Hash:         remoteHash,
			LastEditedAt: lastSync,
			FlowSpaceID:  note.ID,
		}},
	}

	result := NewNotionSyncService(gateway).PullRemote(target)

	if result.Pushed != 0 || len(gateway.updated) != 0 {
		t.Fatalf("pull should not push local changes: result = %+v updated = %#v", result, gateway.updated)
	}
	if result.Pulled != 0 || result.Imported != 0 || result.Failed != 0 {
		t.Fatalf("unexpected pull result = %+v", result)
	}
}

func TestSyncNotionPullSkipsAllRemotePagesWhenSyncTagsAreEmpty(t *testing.T) {
	openServiceSyncTestDB(t)
	target := saveNotionTargetForTest(t)
	gateway := &fakeNotionGateway{
		pages: []notionRemoteNote{{
			PageID:       "page-tagged",
			URL:          "https://www.notion.so/page-tagged",
			Title:        "Remote Tagged",
			Markdown:     "Remote body\n",
			Hash:         notionTitleBodyHash("Remote Tagged", "Remote body\n"),
			LastEditedAt: 1800000000,
			Tags:         []string{"sync"},
		}},
	}

	result := NewNotionSyncService(gateway).PullRemote(target)

	if result.Imported != 0 || result.Pulled != 0 || result.Failed != 0 || len(result.Items) != 0 {
		t.Fatalf("expected empty pull result, got %+v", result)
	}
	notes, err := repository.ListAllNotes()
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no imported notes, got %+v", notes)
	}
}

func TestSyncNotionPullOnlyImportsRemotePagesWithRequiredTag(t *testing.T) {
	openServiceSyncTestDB(t)
	target := saveNotionTargetForTest(t)
	target.ConfigJSON = `{"data_source_id":"ds-123","required_tags":["sync"]}`
	if err := repository.SaveSyncTarget(&target); err != nil {
		t.Fatalf("save target config: %v", err)
	}
	gateway := &fakeNotionGateway{
		pages: []notionRemoteNote{
			{
				PageID:       "page-tagged",
				URL:          "https://www.notion.so/page-tagged",
				Title:        "Remote Tagged",
				Markdown:     "Remote body\n",
				Hash:         notionTitleBodyHash("Remote Tagged", "Remote body\n"),
				LastEditedAt: 1800000000,
				Tags:         []string{"sync", "work"},
			},
			{
				PageID:       "page-private",
				URL:          "https://www.notion.so/page-private",
				Title:        "Remote Private",
				Markdown:     "Private body\n",
				Hash:         notionTitleBodyHash("Remote Private", "Private body\n"),
				LastEditedAt: 1800000001,
				Tags:         []string{"private"},
			},
		},
	}

	result := NewNotionSyncService(gateway).PullRemote(target)

	if result.Imported != 1 || result.Pulled != 0 || result.Failed != 0 {
		t.Fatalf("unexpected pull result = %+v", result)
	}
	notes, err := repository.ListAllNotes()
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 1 || notes[0].Title != "Remote Tagged" || notes[0].Tags != `["sync","work"]` {
		t.Fatalf("unexpected imported notes: %+v", notes)
	}
}

func TestSyncNotionAllSkipsAllLocalNotesWhenSyncTagsAreEmpty(t *testing.T) {
	openServiceSyncTestDB(t)
	target := saveNotionTargetForTest(t)
	insertServiceTaggedNoteForTest(t, "Tagged But Disabled", "Body\n", `["sync"]`)
	gateway := &fakeNotionGateway{}

	result := NewNotionSyncService(gateway).PushAll(target)

	if result.Synced != 0 || result.Failed != 0 || len(result.Items) != 0 {
		t.Fatalf("expected empty push result, got %+v", result)
	}
	if len(gateway.created) != 0 {
		t.Fatalf("created = %#v, want none", gateway.created)
	}
}

func TestSyncNotionAllPushesLocalNotes(t *testing.T) {
	openServiceSyncTestDB(t)
	target := saveNotionTargetForTest(t)
	target.ConfigJSON = `{"data_source_id":"ds-123","required_tags":["sync"]}`
	if err := repository.SaveSyncTarget(&target); err != nil {
		t.Fatalf("save target config: %v", err)
	}
	note := insertServiceTaggedNoteForTest(t, "Local Push All", "Body\n", `["sync"]`)
	insertServiceTaggedNoteForTest(t, "Local Private", "Body\n", `["private"]`)
	gateway := &fakeNotionGateway{}

	result := NewNotionSyncService(gateway).PushAll(target)

	if result.Synced != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}
	if len(gateway.created) != 1 || gateway.created[0] != note.ID {
		t.Fatalf("created = %#v, want %s", gateway.created, note.ID)
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
		ContentHash:   notionTitleBodyHash("Deleted Remote", "Body\n"),
		ExternalHash:  notionTitleBodyHash("Deleted Remote", "Body\n"),
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

func TestNotionRestoreGatewayFailureIsNotDeletionConflict(t *testing.T) {
	openServiceSyncTestDB(t)
	t.Setenv("NOTION_PROVIDER", "mock")
	target := saveNotionTargetForTest(t)
	note := insertServiceNoteForTest(t, "Restore Provider Failure", "Body\n")
	lastSync := int64(1700000000)
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  "notion:page-restore-fails",
		ExternalID:    "page-restore-fails",
		ExternalURL:   "https://www.notion.so/page-restore-fails",
		ContentHash:   notionTitleBodyHash("Restore Provider Failure", "Body\n"),
		ExternalHash:  notionTitleBodyHash("Restore Provider Failure", "Body\n"),
		ExternalMTime: &lastSync,
		LastSyncedAt:  &lastSync,
		LastDirection: "delete_detected",
		Status:        "external_deleted",
	}); err != nil {
		t.Fatalf("upsert state: %v", err)
	}
	providerErr := errors.New("notion API error 503: unavailable")
	originalFactory := notionGatewayFactory
	notionGatewayFactory = func(token string) notionSyncGateway {
		return &fakeNotionGateway{restoreErr: providerErr}
	}
	t.Cleanup(func() {
		notionGatewayFactory = originalFactory
	})

	_, err := RestoreNotionDeletion(note.ID)

	if err == nil {
		t.Fatal("expected restore error")
	}
	if errors.Is(err, ErrNotionDeletionConflict) {
		t.Fatalf("restore provider error was classified as conflict: %v", err)
	}
	if !errors.Is(err, providerErr) {
		t.Fatalf("error = %v, want provider error", err)
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
		IsDefault:  true,
	}
	if err := repository.SaveSyncTarget(target); err != nil {
		t.Fatalf("save target: %v", err)
	}
	return *target
}

func insertServiceNoteForTest(t *testing.T, title, body string) model.Note {
	t.Helper()
	return insertServiceTaggedNoteForTest(t, title, body, "[]")
}

func insertServiceTaggedNoteForTest(t *testing.T, title, body, tags string) model.Note {
	t.Helper()
	note := &model.Note{Title: title, Body: body, FolderID: "__uncategorized", Tags: tags}
	if err := repository.CreateNote(note); err != nil {
		t.Fatalf("create note: %v", err)
	}
	return *note
}
