package contentsource

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

var (
	ErrInvalidURL        = errors.New("source URL is invalid")
	ErrUnsupportedSource = errors.New("source is not supported")
	ErrEpisodeRequired   = errors.New("an episode URL is required")
	ErrSourceUnavailable = errors.New("source is unavailable")
)

const maxSourceDocumentBytes = 4 << 20

type Episode struct {
	SourceType          string `json:"source_type"`
	SubmittedURL        string `json:"submitted_url"`
	CanonicalURL        string `json:"canonical_url"`
	ExternalID          string `json:"external_id"`
	FeedURL             string `json:"feed_url,omitempty"`
	Title               string `json:"title"`
	PodcastTitle        string `json:"podcast_title,omitempty"`
	CoverURL            string `json:"cover_url,omitempty"`
	Description         string `json:"description,omitempty"`
	DurationSeconds     int64  `json:"duration_seconds,omitempty"`
	HasPublicTranscript bool   `json:"has_public_transcript"`
	TranscriptURL       string `json:"-"`
	TranscriptType      string `json:"-"`
	AudioURL            string `json:"-"`
}

type Resolver interface {
	Resolve(context.Context, string) (*Episode, error)
}

type Registry struct {
	http *http.Client
}

func NewRegistry(httpClient *http.Client) (*Registry, error) {
	if httpClient == nil {
		return nil, errors.New("content source HTTP client is required")
	}
	return &Registry{http: httpClient}, nil
}

func (r *Registry) Resolve(ctx context.Context, rawURL string) (*Episode, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.User != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, ErrInvalidURL
	}
	host := strings.ToLower(parsed.Hostname())
	switch {
	case host == "podcasts.apple.com" || strings.HasSuffix(host, ".podcasts.apple.com"):
		return r.resolveApple(ctx, parsed)
	case host == "xiaoyuzhoufm.com" || strings.HasSuffix(host, ".xiaoyuzhoufm.com"):
		return r.resolveWebEpisode(ctx, parsed, "xiaoyuzhou")
	default:
		return nil, ErrUnsupportedSource
	}
}

func (r *Registry) resolveApple(ctx context.Context, parsed *url.URL) (*Episode, error) {
	episodeID := strings.TrimSpace(parsed.Query().Get("i"))
	showID := extractAppleShowID(parsed.Path)
	if episodeID == "" {
		return nil, ErrEpisodeRequired
	}
	page, err := r.fetchHTML(ctx, parsed.String())
	if err != nil {
		return nil, err
	}
	episode := episodeFromHTML(page, parsed.String(), "apple", episodeID)
	if showID != "" {
		feedURL, podcastTitle, lookupErr := r.lookupAppleFeed(ctx, showID)
		if lookupErr == nil {
			episode.FeedURL = feedURL
			if episode.PodcastTitle == "" {
				episode.PodcastTitle = podcastTitle
			}
		}
	}
	if episode.FeedURL == "" {
		episode.FeedURL = page.FeedURL
	}
	return r.enrichFromFeed(ctx, episode)
}

func (r *Registry) resolveWebEpisode(ctx context.Context, parsed *url.URL, sourceType string) (*Episode, error) {
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-2] != "episode" || strings.TrimSpace(parts[len(parts)-1]) == "" {
		return nil, ErrEpisodeRequired
	}
	externalID := parts[len(parts)-1]
	page, err := r.fetchHTML(ctx, parsed.String())
	if err != nil {
		return nil, err
	}
	episode := episodeFromHTML(page, parsed.String(), sourceType, externalID)
	episode.FeedURL = page.FeedURL
	return r.enrichFromFeed(ctx, episode)
}

func (r *Registry) enrichFromFeed(ctx context.Context, episode *Episode) (*Episode, error) {
	if episode.FeedURL == "" {
		return episode, nil
	}
	feed, err := r.fetchFeed(ctx, episode.FeedURL)
	if err != nil {
		return episode, nil
	}
	item := matchFeedItem(feed.Items, episode.ExternalID, episode.Title)
	if item == nil {
		return episode, nil
	}
	if item.Title != "" {
		episode.Title = item.Title
	}
	if feed.Title != "" {
		episode.PodcastTitle = feed.Title
	}
	if item.Description != "" {
		episode.Description = stripHTML(item.Description)
	}
	if item.Image.Href != "" {
		episode.CoverURL = item.Image.Href
	} else if episode.CoverURL == "" {
		episode.CoverURL = feed.ImageURL
	}
	if item.Enclosure.URL != "" {
		episode.AudioURL = item.Enclosure.URL
	}
	episode.DurationSeconds = parseDuration(item.Duration)
	for _, candidate := range item.Transcripts {
		if candidate.URL == "" {
			continue
		}
		kind := strings.ToLower(candidate.Type)
		if kind == "" || strings.Contains(kind, "text") || strings.Contains(kind, "vtt") || strings.Contains(kind, "srt") {
			episode.TranscriptURL = candidate.URL
			episode.TranscriptType = candidate.Type
			episode.HasPublicTranscript = true
			break
		}
	}
	return episode, nil
}

type pageMetadata struct {
	Title        string
	Description  string
	ImageURL     string
	AudioURL     string
	CanonicalURL string
	FeedURL      string
	PodcastTitle string
}

func (r *Registry) fetchHTML(ctx context.Context, rawURL string) (pageMetadata, error) {
	response, err := r.get(ctx, rawURL)
	if err != nil {
		return pageMetadata{}, err
	}
	defer response.Body.Close()
	document, err := html.Parse(io.LimitReader(response.Body, maxSourceDocumentBytes+1))
	if err != nil {
		return pageMetadata{}, ErrSourceUnavailable
	}
	metadata := pageMetadata{}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			attributes := attributeMap(node.Attr)
			if node.Data == "meta" {
				key := strings.ToLower(firstNonEmpty(attributes["property"], attributes["name"]))
				value := strings.TrimSpace(attributes["content"])
				switch key {
				case "og:title", "twitter:title":
					if metadata.Title == "" {
						metadata.Title = value
					}
				case "og:description", "description":
					if metadata.Description == "" {
						metadata.Description = value
					}
				case "og:image", "twitter:image":
					if metadata.ImageURL == "" {
						metadata.ImageURL = value
					}
				case "og:audio", "og:audio:url", "og:audio:secure_url", "twitter:player:stream":
					if metadata.AudioURL == "" {
						metadata.AudioURL = value
					}
				case "og:site_name":
					metadata.PodcastTitle = value
				}
			}
			if node.Data == "link" {
				rel, kind, href := strings.ToLower(attributes["rel"]), strings.ToLower(attributes["type"]), strings.TrimSpace(attributes["href"])
				if rel == "canonical" {
					metadata.CanonicalURL = href
				}
				if strings.Contains(rel, "alternate") && (strings.Contains(kind, "rss") || strings.Contains(kind, "atom")) {
					metadata.FeedURL = href
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	base := response.Request.URL
	metadata.CanonicalURL = resolveReference(base, metadata.CanonicalURL)
	metadata.FeedURL = resolveReference(base, metadata.FeedURL)
	metadata.ImageURL = resolveReference(base, metadata.ImageURL)
	metadata.AudioURL = resolveReference(base, metadata.AudioURL)
	return metadata, nil
}

func episodeFromHTML(page pageMetadata, submittedURL, sourceType, externalID string) *Episode {
	canonical := page.CanonicalURL
	if canonical == "" {
		canonical = submittedURL
	}
	return &Episode{SourceType: sourceType, SubmittedURL: submittedURL, CanonicalURL: canonical, ExternalID: externalID,
		Title: page.Title, PodcastTitle: page.PodcastTitle, CoverURL: page.ImageURL, Description: page.Description, AudioURL: page.AudioURL}
}

func (r *Registry) get(ctx context.Context, rawURL string) (*http.Response, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, ErrInvalidURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, ErrInvalidURL
	}
	request.Header.Set("Accept", "text/html, application/rss+xml, application/xml;q=0.9, */*;q=0.1")
	request.Header.Set("User-Agent", "FlowSpace/0.2 (+podcast import)")
	response, err := r.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSourceUnavailable, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, ErrSourceUnavailable
	}
	if response.ContentLength > maxSourceDocumentBytes {
		response.Body.Close()
		return nil, ErrSourceUnavailable
	}
	return response, nil
}

func (r *Registry) lookupAppleFeed(ctx context.Context, showID string) (string, string, error) {
	endpoint := "https://itunes.apple.com/lookup?id=" + url.QueryEscape(showID) + "&entity=podcast"
	response, err := r.get(ctx, endpoint)
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()
	var result struct {
		Results []struct {
			FeedURL        string `json:"feedUrl"`
			CollectionName string `json:"collectionName"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxSourceDocumentBytes)).Decode(&result); err != nil || len(result.Results) == 0 {
		return "", "", ErrSourceUnavailable
	}
	return result.Results[0].FeedURL, result.Results[0].CollectionName, nil
}

type feedDocument struct {
	Title    string     `xml:"channel>title"`
	ImageURL string     `xml:"channel>image>url"`
	Items    []feedItem `xml:"channel>item"`
}

type feedItem struct {
	Title       string `xml:"title"`
	GUID        string `xml:"guid"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Duration    string `xml:"duration"`
	Image       struct {
		Href string `xml:"href,attr"`
	} `xml:"image"`
	Enclosure struct {
		URL  string `xml:"url,attr"`
		Type string `xml:"type,attr"`
	} `xml:"enclosure"`
	Transcripts []struct {
		URL  string `xml:"url,attr"`
		Type string `xml:"type,attr"`
	} `xml:"transcript"`
}

func (r *Registry) fetchFeed(ctx context.Context, feedURL string) (*feedDocument, error) {
	response, err := r.get(ctx, feedURL)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var feed feedDocument
	decoder := xml.NewDecoder(io.LimitReader(response.Body, maxSourceDocumentBytes+1))
	decoder.Strict = true
	if err := decoder.Decode(&feed); err != nil {
		return nil, ErrSourceUnavailable
	}
	base, _ := url.Parse(feedURL)
	feed.ImageURL = resolveReference(base, feed.ImageURL)
	for index := range feed.Items {
		feed.Items[index].Link = resolveReference(base, feed.Items[index].Link)
		feed.Items[index].Image.Href = resolveReference(base, feed.Items[index].Image.Href)
		feed.Items[index].Enclosure.URL = resolveReference(base, feed.Items[index].Enclosure.URL)
		for transcriptIndex := range feed.Items[index].Transcripts {
			feed.Items[index].Transcripts[transcriptIndex].URL = resolveReference(base, feed.Items[index].Transcripts[transcriptIndex].URL)
		}
	}
	return &feed, nil
}

func matchFeedItem(items []feedItem, externalID, title string) *feedItem {
	normalizedTitle := normalizeTitle(title)
	for index := range items {
		item := &items[index]
		if externalID != "" && (strings.Contains(item.GUID, externalID) || strings.Contains(item.Link, externalID)) {
			return item
		}
	}
	for index := range items {
		item := &items[index]
		candidate := normalizeTitle(item.Title)
		if normalizedTitle != "" && candidate != "" && (strings.Contains(normalizedTitle, candidate) || strings.Contains(candidate, normalizedTitle)) {
			return item
		}
	}
	return nil
}

func extractAppleShowID(path string) string {
	match := regexp.MustCompile(`(?:^|/)id(\d+)(?:$|/)`).FindStringSubmatch(path)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func attributeMap(attributes []html.Attribute) map[string]string {
	result := make(map[string]string, len(attributes))
	for _, attribute := range attributes {
		result[strings.ToLower(attribute.Key)] = attribute.Val
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
func resolveReference(base *url.URL, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || base == nil {
		return value
	}
	reference, err := url.Parse(value)
	if err != nil {
		return value
	}
	return base.ResolveReference(reference).String()
}
func normalizeTitle(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(stripHTML(value)), " "))
}

func stripHTML(value string) string {
	document, err := html.Parse(strings.NewReader(value))
	if err != nil {
		return strings.TrimSpace(value)
	}
	parts := make([]string, 0)
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode && strings.TrimSpace(node.Data) != "" {
			parts = append(parts, strings.TrimSpace(node.Data))
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return strings.Join(parts, "\n")
}

func parseDuration(value string) int64 {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) == 0 {
		return 0
	}
	var total int64
	for _, part := range parts {
		number, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return 0
		}
		total = total*60 + number
	}
	return total
}
