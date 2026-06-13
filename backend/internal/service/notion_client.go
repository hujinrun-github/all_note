package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	notionAPIBaseURL = "https://api.notion.com"
	notionVersion    = "2022-06-28"
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
			return err
		}
		err = decodeNotionResponse(resp, out)
		if err == nil {
			return nil
		}
		if !isRetryableNotionError(err) || attempt >= 2 {
			return err
		}
		client.retrySleep(notionRetryAfter(resp.Header.Get("Retry-After")))
	}
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
