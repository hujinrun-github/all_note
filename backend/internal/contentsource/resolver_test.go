package contentsource

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestResolveXiaoyuzhouEpisodeFindsPublicRSSFeedTranscript(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, contentType := "", "text/html"
		switch request.URL.Path {
		case "/episode/episode-1":
			body = `<html><head><meta property="og:title" content="AI 产品经理的下一站"><meta property="og:image" content="/cover.jpg"><link rel="alternate" type="application/rss+xml" href="/feed.xml"></head></html>`
		case "/feed.xml":
			contentType = "application/rss+xml"
			body = `<rss xmlns:podcast="https://podcastindex.org/namespace/1.0"><channel><title>产品沉思录</title><item><title>AI 产品经理的下一站</title><guid>episode-1</guid><itunes:duration xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd">01:02:03</itunes:duration><enclosure url="/audio.mp3" type="audio/mpeg"/><podcast:transcript url="/transcript.vtt" type="text/vtt"/></item></channel></rss>`
		default:
			t.Fatalf("unexpected request path %s", request.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{contentType}}, Request: request, ContentLength: int64(len(body))}, nil
	})}
	registry, err := NewRegistry(client)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse("https://www.xiaoyuzhoufm.com/episode/episode-1")
	episode, err := registry.resolveWebEpisode(context.Background(), parsed, "xiaoyuzhou")
	if err != nil {
		t.Fatal(err)
	}
	if !episode.HasPublicTranscript || episode.TranscriptURL != "https://www.xiaoyuzhoufm.com/transcript.vtt" {
		t.Fatalf("transcript = %#v", episode)
	}
	if episode.PodcastTitle != "产品沉思录" || episode.DurationSeconds != 3723 {
		t.Fatalf("episode metadata = %#v", episode)
	}
}

func TestResolveRequiresSingleEpisodeURL(t *testing.T) {
	registry, _ := NewRegistry(&http.Client{})
	if _, err := registry.Resolve(context.Background(), "https://www.xiaoyuzhoufm.com/podcast/abc"); err != ErrEpisodeRequired {
		t.Fatalf("error = %v", err)
	}
}

func TestRegistryRejectsCredentialedSubresourceURLs(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("credentialed request reached transport: %s", request.URL)
		return nil, nil
	})}
	registry, _ := NewRegistry(client)
	if _, err := registry.get(context.Background(), "https://user:password@media.example/private.mp3"); !errors.Is(err, ErrInvalidURL) {
		t.Fatalf("get() error = %v", err)
	}
}

func TestResolveXiaoyuzhouEpisodeUsesPublicOpenGraphAudioWithoutRSS(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `<html><head><meta property="og:title" content="公开单集"><meta property="og:audio" content="https://media.example/public.m4a"></head></html>`
		return &http.Response{
			StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Request: request,
			ContentLength: int64(len(body)), Header: http.Header{"Content-Type": []string{"text/html"}},
		}, nil
	})}
	registry, err := NewRegistry(client)
	if err != nil {
		t.Fatal(err)
	}
	episode, err := registry.Resolve(context.Background(), "https://www.xiaoyuzhoufm.com/episode/public-1")
	if err != nil {
		t.Fatal(err)
	}
	if episode.AudioURL != "https://media.example/public.m4a" || episode.HasPublicTranscript {
		t.Fatalf("episode = %#v", episode)
	}
}
