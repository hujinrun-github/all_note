package service

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

func notionGatewayFromEnv(token string) notionSyncGateway {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("NOTION_PROVIDER")), "mock") {
		return newMockNotionGateway()
	}
	return newRealNotionGateway(token)
}

type mockNotionGateway struct{}

func newMockNotionGateway() notionSyncGateway {
	return &mockNotionGateway{}
}

func (gateway *mockNotionGateway) TestDataSource(config notionTargetConfig) error {
	return nil
}

func (gateway *mockNotionGateway) QueryRemoteNotes(config notionTargetConfig) ([]notionRemoteNote, error) {
	markdown := "Imported from mock Notion.\n"
	return []notionRemoteNote{
		{
			PageID:       "mock-page-1",
			URL:          "https://www.notion.so/mock-page-1",
			Title:        "Mock Notion Note",
			Markdown:     markdown,
			Hash:         notionMarkdownHash(markdown),
			LastEditedAt: 1900000000,
		},
	}, nil
}

func (gateway *mockNotionGateway) CreateRemoteNote(config notionTargetConfig, note *model.Note) (notionRemoteNote, error) {
	return notionRemoteFromLocalNote("mock-created-"+note.ID, note, 1900000001), nil
}

func (gateway *mockNotionGateway) UpdateRemoteNote(config notionTargetConfig, pageID string, note *model.Note) (notionRemoteNote, error) {
	return notionRemoteFromLocalNote(pageID, note, 1900000002), nil
}

func (gateway *mockNotionGateway) RestoreRemoteNote(config notionTargetConfig, note *model.Note, previous notionSyncStateSnapshot) (notionRemoteNote, error) {
	pageID := previous.ExternalID
	if strings.TrimSpace(pageID) == "" {
		pageID = "mock-restored-" + note.ID
	}
	return notionRemoteFromLocalNote(pageID, note, 1900000003), nil
}

type realNotionSyncGateway struct {
	client *notionHTTPClient
}

func newRealNotionGateway(token string) notionSyncGateway {
	return &realNotionSyncGateway{client: newNotionHTTPClient(token, "")}
}

func (gateway *realNotionSyncGateway) TestDataSource(config notionTargetConfig) error {
	_, err := gateway.client.QueryDataSource(config.DataSourceID)
	return err
}

func (gateway *realNotionSyncGateway) QueryRemoteNotes(config notionTargetConfig) ([]notionRemoteNote, error) {
	pages, err := gateway.client.QueryDataSource(config.DataSourceID)
	if err != nil {
		return nil, err
	}
	notes := make([]notionRemoteNote, 0, len(pages))
	for _, page := range pages {
		if page.Archived || page.InTrash {
			continue
		}
		notes = append(notes, notionRemoteNote{
			PageID:           page.ID,
			URL:              page.URL,
			Title:            page.ID,
			Hash:             notionMarkdownHash(""),
			LastEditedAt:     notionPageEditedUnix(page.LastEditedTime),
			UnsupportedTypes: []string{"page_blocks_not_loaded"},
		})
	}
	return notes, nil
}

func (gateway *realNotionSyncGateway) CreateRemoteNote(config notionTargetConfig, note *model.Note) (notionRemoteNote, error) {
	return notionRemoteNote{}, errors.New("real Notion page creation is not implemented")
}

func (gateway *realNotionSyncGateway) UpdateRemoteNote(config notionTargetConfig, pageID string, note *model.Note) (notionRemoteNote, error) {
	return notionRemoteNote{}, errors.New("real Notion page update is not implemented")
}

func (gateway *realNotionSyncGateway) RestoreRemoteNote(config notionTargetConfig, note *model.Note, previous notionSyncStateSnapshot) (notionRemoteNote, error) {
	return notionRemoteNote{}, errors.New("real Notion page restore is not implemented")
}

func notionRemoteFromLocalNote(pageID string, note *model.Note, editedAt int64) notionRemoteNote {
	return notionRemoteNote{
		PageID:       pageID,
		URL:          "https://www.notion.so/" + pageID,
		Title:        note.Title,
		Markdown:     note.Body,
		Hash:         notionMarkdownHash(note.Body),
		LastEditedAt: editedAt,
		FlowSpaceID:  note.ID,
	}
}

func notionPageEditedUnix(value string) int64 {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed.Unix()
}
