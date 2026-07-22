package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
)

type failingTextGenerator struct{}

func (failingTextGenerator) Generate(context.Context, string, string) (string, error) {
	return "", errors.New("AI unavailable")
}

func TestAnnotateJapanesePolicyCanDisableLocalFallback(t *testing.T) {
	if _, _, err := AnnotateJapaneseWithTextGeneratorPolicy(context.Background(), "日本", failingTextGenerator{}, false); err == nil {
		t.Fatal("disabled local fallback silently returned local annotation")
	}
	segments, source, err := AnnotateJapaneseWithTextGeneratorPolicy(context.Background(), "日本", failingTextGenerator{}, true)
	if err != nil || len(segments) == 0 || source != "local" {
		t.Fatalf("explicit local fallback: segments=%v source=%q err=%v", segments, source, err)
	}
}

func TestAnnotateJapaneseWithAIUsesOpenAICompatibleResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"segments\":[{\"text\":\"すぐ\"},{\"text\":\"近\",\"reading\":\"ちか\"},{\"text\":\"く\"}]}"}}]}`))
	}))
	defer server.Close()
	t.Setenv("AI_PROVIDER", "deepseek")
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_BASE_URL", server.URL)
	t.Setenv("AI_MODEL", "test-model")

	segments, source, err := AnnotateJapaneseWithAI(context.Background(), "すぐ近く")
	if err != nil {
		t.Fatalf("AnnotateJapaneseWithAI returned error: %v", err)
	}
	want := []model.FuriganaSegment{{Text: "すぐ"}, {Text: "近", Reading: "ちか"}, {Text: "く"}}
	if !reflect.DeepEqual(segments, want) {
		t.Fatalf("segments = %#v, want %#v", segments, want)
	}
	if source != "ai" {
		t.Fatalf("source = %q, want ai", source)
	}
}

func TestAnnotateJapaneseWithAIFallsBackWhenResponseChangesOriginalText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"segments\":[{\"text\":\"近所\",\"reading\":\"きんじょ\"}]}"}}]}`))
	}))
	defer server.Close()
	t.Setenv("AI_PROVIDER", "deepseek")
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_BASE_URL", server.URL)

	segments, source, err := AnnotateJapaneseWithAI(context.Background(), "近く")
	if err != nil {
		t.Fatalf("AnnotateJapaneseWithAI returned error: %v", err)
	}
	want := []model.FuriganaSegment{{Text: "近", Reading: "ちか"}, {Text: "く"}}
	if !reflect.DeepEqual(segments, want) {
		t.Fatalf("segments = %#v, want %#v", segments, want)
	}
	if source != "local" {
		t.Fatalf("source = %q, want local", source)
	}
}

func TestAnnotateJapanesePlacesReadingsOnlyOverKanji(t *testing.T) {
	got, err := AnnotateJapanese("すぐ近く、日本語を勉強する")
	if err != nil {
		t.Fatalf("AnnotateJapanese returned error: %v", err)
	}

	want := []model.FuriganaSegment{
		{Text: "すぐ"},
		{Text: "近", Reading: "ちか"},
		{Text: "く、"},
		{Text: "日本語", Reading: "にほんご"},
		{Text: "を"},
		{Text: "勉強", Reading: "べんきょう"},
		{Text: "する"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("segments = %#v, want %#v", got, want)
	}
}

func TestAnnotateJapaneseLeavesKanaAndLatinTextUnchanged(t *testing.T) {
	got, err := AnnotateJapanese("ふりがな ABC 123")
	if err != nil {
		t.Fatalf("AnnotateJapanese returned error: %v", err)
	}

	want := []model.FuriganaSegment{{Text: "ふりがな ABC 123"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("segments = %#v, want %#v", got, want)
	}
}

func TestAlignTokenReadingHandlesOkurigana(t *testing.T) {
	got := alignTokenReading("取り扱う", "トリアツカウ")
	want := []model.FuriganaSegment{
		{Text: "取", Reading: "と"},
		{Text: "り"},
		{Text: "扱", Reading: "あつか"},
		{Text: "う"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("segments = %#v, want %#v", got, want)
	}
}
