package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

const (
	notionAPIBaseURL = "https://api.notion.com"
	notionVersion    = "2025-09-03"
)

type notionHTTPClient struct {
	token      string
	baseURL    string
	httpClient *http.Client
	retrySleep func(time.Duration)
}

type notionPage struct {
	ID             string         `json:"id"`
	URL            string         `json:"url"`
	LastEditedTime string         `json:"last_edited_time"`
	Archived       bool           `json:"archived,omitempty"`
	InTrash        bool           `json:"in_trash,omitempty"`
	Properties     map[string]any `json:"properties,omitempty"`
}

type notionHTTPError struct {
	StatusCode int
	Message    string
}

func (err notionHTTPError) Error() string {
	if err.Message == "" {
		return fmt.Sprintf("notion API error %d", err.StatusCode)
	}
	return fmt.Sprintf("notion API error %d: %s", err.StatusCode, err.Message)
}

func newNotionHTTPClient(token string, baseURL string) *notionHTTPClient {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = notionAPIBaseURL
	}
	return &notionHTTPClient{
		token:      strings.TrimSpace(token),
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		retrySleep: time.Sleep,
	}
}

func (client *notionHTTPClient) QueryDataSource(dataSourceID string) ([]notionPage, error) {
	dataSourceID = strings.TrimSpace(dataSourceID)
	if dataSourceID == "" {
		return nil, fmt.Errorf("notion data source id is required")
	}

	pages := make([]notionPage, 0)
	cursor := ""
	for {
		body := map[string]any{}
		if cursor != "" {
			body["start_cursor"] = cursor
		}

		var response struct {
			Results    []notionPage `json:"results"`
			HasMore    bool         `json:"has_more"`
			NextCursor string       `json:"next_cursor"`
		}
		path := "/v1/data_sources/" + url.PathEscape(dataSourceID) + "/query"
		if err := client.doJSON(http.MethodPost, path, body, &response); err != nil {
			return nil, err
		}

		pages = append(pages, response.Results...)
		if !response.HasMore {
			return pages, nil
		}
		cursor = strings.TrimSpace(response.NextCursor)
		if cursor == "" {
			return nil, fmt.Errorf("notion data source query has_more without next_cursor")
		}
	}
}

func (client *notionHTTPClient) RetrievePageBlocks(pageID string) ([]notionBlock, error) {
	pageID = strings.TrimSpace(pageID)
	if pageID == "" {
		return nil, fmt.Errorf("notion page id is required")
	}

	blocks := make([]notionBlock, 0)
	cursor := ""
	for {
		path := "/v1/blocks/" + url.PathEscape(pageID) + "/children"
		if cursor != "" {
			path += "?start_cursor=" + url.QueryEscape(cursor)
		}

		var response struct {
			Results    []notionBlock `json:"results"`
			HasMore    bool          `json:"has_more"`
			NextCursor string        `json:"next_cursor"`
		}
		if err := client.doJSON(http.MethodGet, path, nil, &response); err != nil {
			return nil, err
		}

		blocks = append(blocks, response.Results...)
		if !response.HasMore {
			return blocks, nil
		}
		cursor = strings.TrimSpace(response.NextCursor)
		if cursor == "" {
			return nil, fmt.Errorf("notion block children response has_more without next_cursor")
		}
	}
}

func (client *notionHTTPClient) CreatePage(config notionTargetConfig, note *model.Note, blocks []notionBlock) (notionPage, error) {
	var page notionPage
	if note == nil {
		return page, fmt.Errorf("note is required")
	}
	body := map[string]any{
		"parent": map[string]any{
			"type":           "data_source_id",
			"data_source_id": strings.TrimSpace(config.DataSourceID),
		},
		"properties": notionNotePropertyPayload(config, note),
		"children":   notionBlockChildrenPayload(blocks),
	}
	err := client.doJSON(http.MethodPost, "/v1/pages", body, &page)
	return page, err
}

func (client *notionHTTPClient) UpdatePage(config notionTargetConfig, pageID string, note *model.Note) (notionPage, error) {
	var page notionPage
	pageID = strings.TrimSpace(pageID)
	if pageID == "" {
		return page, fmt.Errorf("notion page id is required")
	}
	if note == nil {
		return page, fmt.Errorf("note is required")
	}
	body := map[string]any{
		"properties": notionNotePropertyPayload(config, note),
	}
	err := client.doJSON(http.MethodPatch, "/v1/pages/"+url.PathEscape(pageID), body, &page)
	return page, err
}

func (client *notionHTTPClient) RestorePage(pageID string) (notionPage, error) {
	var page notionPage
	pageID = strings.TrimSpace(pageID)
	if pageID == "" {
		return page, fmt.Errorf("notion page id is required")
	}
	body := map[string]any{
		"archived": false,
		"in_trash": false,
	}
	err := client.doJSON(http.MethodPatch, "/v1/pages/"+url.PathEscape(pageID), body, &page)
	return page, err
}

func (client *notionHTTPClient) AppendBlockChildren(parentID string, blocks []notionBlock) error {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		return fmt.Errorf("notion parent block id is required")
	}
	body := map[string]any{
		"children": notionBlockChildrenPayload(blocks),
	}
	return client.doJSON(http.MethodPatch, "/v1/blocks/"+url.PathEscape(parentID)+"/children", body, nil)
}

func (client *notionHTTPClient) ArchiveBlock(blockID string) error {
	blockID = strings.TrimSpace(blockID)
	if blockID == "" {
		return fmt.Errorf("notion block id is required")
	}
	return client.doJSON(http.MethodPatch, "/v1/blocks/"+url.PathEscape(blockID), map[string]any{"archived": true}, nil)
}

func (client *notionHTTPClient) doJSON(method, path string, body any, out any) error {
	for attempt := 0; ; attempt++ {
		payload, err := jsonPayload(body)
		if err != nil {
			return err
		}

		req, err := http.NewRequest(method, client.baseURL+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+client.token)
		req.Header.Set("Notion-Version", notionVersion)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := client.httpClient.Do(req)
		if err != nil {
			return client.redactError(err)
		}
		err = decodeNotionResponse(resp, out)
		if err == nil {
			return nil
		}
		err = client.redactError(err)
		if !isRetryableNotionError(err) || attempt >= 2 {
			return err
		}
		client.retrySleep(notionRetryAfter(resp.Header.Get("Retry-After")))
	}
}

func notionNotePropertyPayload(config notionTargetConfig, note *model.Note) map[string]any {
	titleProperty := defaultString(config.TitleProperty, "Name")
	flowSpaceIDProperty := defaultString(config.FlowSpaceIDProperty, "FlowSpace ID")
	tagsProperty := defaultString(config.TagsProperty, "Tags")
	payload := map[string]any{
		titleProperty: map[string]any{
			"title": []map[string]any{notionTextRequest(note.Title)},
		},
		flowSpaceIDProperty: map[string]any{
			"rich_text": []map[string]any{notionTextRequest(note.ID)},
		},
	}
	if strings.TrimSpace(tagsProperty) != "" {
		payload[tagsProperty] = map[string]any{
			"multi_select": notionMultiSelectRequest(parseTags(note.Tags)),
		}
	}
	return payload
}

func notionMultiSelectRequest(tags []string) []map[string]any {
	cleaned := cleanSyncTags(tags)
	items := make([]map[string]any, 0, len(cleaned))
	for _, tag := range cleaned {
		items = append(items, map[string]any{"name": tag})
	}
	return items
}

func notionBlockChildrenPayload(blocks []notionBlock) []map[string]any {
	children := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		children = append(children, notionBlockPayload(block))
	}
	return children
}

func notionBlockPayload(block notionBlock) map[string]any {
	switch block.Type {
	case "heading_1":
		return notionTextBlockPayload("heading_1", block.Heading1.RichText)
	case "heading_2":
		return notionTextBlockPayload("heading_2", block.Heading2.RichText)
	case "heading_3":
		return notionTextBlockPayload("heading_3", block.Heading3.RichText)
	case "bulleted_list_item":
		return notionTextBlockPayload("bulleted_list_item", block.BulletedListItem.RichText)
	case "numbered_list_item":
		return notionTextBlockPayload("numbered_list_item", block.NumberedListItem.RichText)
	case "to_do":
		return map[string]any{
			"type": "to_do",
			"to_do": map[string]any{
				"rich_text": notionRichTextPayload(block.ToDo.RichText),
				"checked":   block.ToDo.Checked,
			},
		}
	case "quote":
		return notionTextBlockPayload("quote", block.Quote.RichText)
	case "code":
		return map[string]any{
			"type": "code",
			"code": map[string]any{
				"rich_text": notionRichTextPayload(block.Code.RichText),
				"language":  defaultString(block.Code.Language, "plain text"),
			},
		}
	case "divider":
		return map[string]any{
			"type":    "divider",
			"divider": map[string]any{},
		}
	default:
		return notionTextBlockPayload("paragraph", block.Paragraph.RichText)
	}
}

func notionTextBlockPayload(blockType string, richText []notionRichText) map[string]any {
	return map[string]any{
		"type": blockType,
		blockType: map[string]any{
			"rich_text": notionRichTextPayload(richText),
		},
	}
}

func notionRichTextPayload(richText []notionRichText) []map[string]any {
	if len(richText) == 0 {
		return []map[string]any{notionTextRequest("")}
	}
	payload := make([]map[string]any, 0, len(richText))
	for _, part := range richText {
		item := notionTextRequest(part.PlainText)
		if part.Href != nil && strings.TrimSpace(*part.Href) != "" {
			item["href"] = strings.TrimSpace(*part.Href)
		}
		if part.Annotations.Bold || part.Annotations.Italic || part.Annotations.Strikethrough || part.Annotations.Code {
			item["annotations"] = map[string]any{
				"bold":          part.Annotations.Bold,
				"italic":        part.Annotations.Italic,
				"strikethrough": part.Annotations.Strikethrough,
				"code":          part.Annotations.Code,
			}
		}
		payload = append(payload, item)
	}
	return payload
}

func notionTextRequest(content string) map[string]any {
	return map[string]any{
		"type": "text",
		"text": map[string]any{
			"content": content,
		},
	}
}

func (client *notionHTTPClient) redactError(err error) error {
	if err == nil || client == nil || client.token == "" {
		return err
	}
	if httpErr, ok := err.(notionHTTPError); ok {
		httpErr.Message = strings.ReplaceAll(httpErr.Message, client.token, "[REDACTED]")
		return httpErr
	}
	redacted := strings.ReplaceAll(err.Error(), client.token, "[REDACTED]")
	if redacted == err.Error() {
		return err
	}
	return errors.New(redacted)
}

func jsonPayload(body any) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func decodeNotionResponse(resp *http.Response, out any) error {
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return notionHTTPError{
			StatusCode: resp.StatusCode,
			Message:    notionErrorMessage(data),
		}
	}
	if out == nil || len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode notion response: %w", err)
	}
	return nil
}

func notionErrorMessage(data []byte) string {
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &payload); err == nil && strings.TrimSpace(payload.Message) != "" {
		return strings.TrimSpace(payload.Message)
	}
	return strings.TrimSpace(string(data))
}

func isRetryableNotionError(err error) bool {
	httpErr, ok := err.(notionHTTPError)
	return ok && (httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode == 529)
}

func notionRetryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return time.Second
	}
	if seconds, err := strconv.Atoi(header); err == nil {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(header); err == nil {
		delay := time.Until(retryAt)
		if delay > 0 {
			return delay
		}
		return 0
	}
	return time.Second
}
