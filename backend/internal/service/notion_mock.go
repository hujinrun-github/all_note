package service

import (
	"os"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

func notionGatewayFromEnv(token string) notionSyncGateway {
	if notionMockProviderEnabled() {
		return newMockNotionGateway()
	}
	return newRealNotionGateway(token)
}

func notionMockProviderEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("NOTION_PROVIDER")), "mock")
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
		blocks, err := gateway.client.RetrievePageBlocks(page.ID)
		if err != nil {
			return nil, err
		}
		converted := notionBlocksToMarkdown(blocks)
		notes = append(notes, notionRemoteNote{
			PageID:           page.ID,
			URL:              page.URL,
			Title:            notionPageTitle(page, config),
			Markdown:         converted.Markdown,
			Hash:             notionMarkdownHash(converted.Markdown),
			LastEditedAt:     notionPageEditedUnix(page.LastEditedTime),
			FlowSpaceID:      notionPageRichTextProperty(page, config.FlowSpaceIDProperty),
			UnsupportedTypes: converted.UnsupportedTypes,
		})
	}
	return notes, nil
}

func (gateway *realNotionSyncGateway) CreateRemoteNote(config notionTargetConfig, note *model.Note) (notionRemoteNote, error) {
	blocks := markdownToNotionBlocks(note.Body)
	page, err := gateway.client.CreatePage(config, note, blocks)
	if err != nil {
		return notionRemoteNote{}, err
	}
	return notionRemoteFromPageAndLocalNote(page, note), nil
}

func (gateway *realNotionSyncGateway) UpdateRemoteNote(config notionTargetConfig, pageID string, note *model.Note) (notionRemoteNote, error) {
	page, err := gateway.client.UpdatePage(config, pageID, note)
	if err != nil {
		return notionRemoteNote{}, err
	}
	children, err := gateway.client.RetrievePageBlocks(pageID)
	if err != nil {
		return notionRemoteNote{}, err
	}
	for _, child := range children {
		if strings.TrimSpace(child.ID) == "" {
			continue
		}
		if err := gateway.client.ArchiveBlock(child.ID); err != nil {
			return notionRemoteNote{}, err
		}
	}
	if err := gateway.client.AppendBlockChildren(pageID, markdownToNotionBlocks(note.Body)); err != nil {
		return notionRemoteNote{}, err
	}
	return notionRemoteFromPageAndLocalNote(page, note), nil
}

func (gateway *realNotionSyncGateway) RestoreRemoteNote(config notionTargetConfig, note *model.Note, previous notionSyncStateSnapshot) (notionRemoteNote, error) {
	pageID := strings.TrimSpace(previous.ExternalID)
	if pageID == "" {
		pageID = strings.TrimPrefix(strings.TrimSpace(previous.ExternalPath), "notion:")
	}
	if pageID == "" {
		return gateway.CreateRemoteNote(config, note)
	}
	page, err := gateway.client.RestorePage(pageID)
	if err != nil {
		return notionRemoteNote{}, err
	}
	return notionRemoteFromPageAndLocalNote(page, note), nil
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

func notionRemoteFromPageAndLocalNote(page notionPage, note *model.Note) notionRemoteNote {
	remote := notionRemoteFromLocalNote(page.ID, note, notionPageEditedUnix(page.LastEditedTime))
	remote.URL = page.URL
	if remote.PageID == "" {
		remote.PageID = strings.TrimSpace(page.ID)
	}
	return remote
}

func notionPageTitle(page notionPage, config notionTargetConfig) string {
	if title := notionPageTextProperty(page, config.TitleProperty, "title"); title != "" {
		return title
	}
	return page.ID
}

func notionPageRichTextProperty(page notionPage, propertyName string) string {
	return notionPageTextProperty(page, propertyName, "rich_text")
}

func notionPageTextProperty(page notionPage, propertyName, listKey string) string {
	propertyName = strings.TrimSpace(propertyName)
	if propertyName == "" || page.Properties == nil {
		return ""
	}
	raw, ok := page.Properties[propertyName]
	if !ok {
		return ""
	}
	property, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	rawText, ok := property[listKey]
	if !ok {
		return ""
	}
	parts, ok := rawText.([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, part := range parts {
		item, ok := part.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := item["plain_text"].(string); ok {
			b.WriteString(text)
		}
	}
	return strings.TrimSpace(b.String())
}

func notionPageEditedUnix(value string) int64 {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed.Unix()
}
