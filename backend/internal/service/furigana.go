package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"unicode"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/ikawaha/kagome-dict/ipa"
	"github.com/ikawaha/kagome/v2/tokenizer"
)

var (
	sharedJapaneseTokenizer     *tokenizer.Tokenizer
	sharedJapaneseTokenizerErr  error
	sharedJapaneseTokenizerOnce sync.Once
)

type surfaceRun struct {
	text  string
	kanji bool
}

type furiganaAIDraft struct {
	Segments []model.FuriganaSegment `json:"segments"`
}

func AnnotateJapaneseWithAI(ctx context.Context, text string) ([]model.FuriganaSegment, string, error) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("AI_PROVIDER")))
	apiKey := strings.TrimSpace(os.Getenv("AI_API_KEY"))
	if apiKey != "" && provider != "none" && provider != "mock" {
		segments, err := annotateJapaneseWithOpenAI(ctx, text, apiKey)
		if err == nil {
			return segments, "ai", nil
		}
	}

	segments, err := AnnotateJapanese(text)
	if err != nil {
		return nil, "", err
	}
	return segments, "local", nil
}

func AnnotateJapaneseWithTextGenerator(ctx context.Context, text string, generator TextGenerator) ([]model.FuriganaSegment, string, error) {
	if generator != nil {
		content, err := generator.Generate(ctx,
			"You add Japanese furigana. Return JSON only as {\"segments\":[{\"text\":\"原文\",\"reading\":\"ひらがな\"}]}. Preserve every original character and its order exactly. Split kana, punctuation, numbers and Latin text into segments without reading. Add reading only to kanji text, use contextual hiragana readings, and keep okurigana outside the annotated kanji segment.",
			text,
		)
		if err == nil {
			var draft furiganaAIDraft
			if decodeFuriganaAIDraft(content, &draft) == nil {
				if segments, validateErr := validateFuriganaAISegments(text, draft.Segments); validateErr == nil {
					return segments, "ai", nil
				}
			}
		}
	}
	segments, err := AnnotateJapanese(text)
	if err != nil {
		return nil, "", err
	}
	return segments, "local", nil
}

func AnnotateJapanese(text string) ([]model.FuriganaSegment, error) {
	t, err := japaneseTokenizer()
	if err != nil {
		return nil, err
	}

	segments := make([]model.FuriganaSegment, 0)
	for _, token := range t.Tokenize(text) {
		reading, ok := token.Reading()
		if !ok || !containsKanji(token.Surface) {
			segments = appendPlainSegment(segments, token.Surface)
			continue
		}
		segments = appendMergedSegments(segments, alignTokenReading(token.Surface, reading))
	}
	if len(segments) == 0 && text != "" {
		segments = appendPlainSegment(segments, text)
	}
	return segments, nil
}

func annotateJapaneseWithOpenAI(ctx context.Context, text, apiKey string) ([]model.FuriganaSegment, error) {
	modelName := strings.TrimSpace(os.Getenv("AI_MODEL"))
	if modelName == "" {
		modelName = defaultAIModel
	}
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("AI_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = defaultAIBaseURL
	}

	body := map[string]interface{}{
		"model": modelName,
		"messages": []map[string]string{
			{
				"role": "system",
				"content": "You add Japanese furigana. Return JSON only as {\"segments\":[{\"text\":\"原文\",\"reading\":\"ひらがな\"}]}. " +
					"Preserve every original character and its order exactly. Split kana, punctuation, numbers and Latin text into segments without reading. " +
					"Add reading only to kanji text, use contextual hiragana readings, and keep okurigana outside the annotated kanji segment.",
			},
			{
				"role":    "user",
				"content": text,
			},
		},
		"temperature": 0,
		"response_format": map[string]string{
			"type": "json_object",
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: defaultAIRequestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("AI request failed: %s %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}

	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if len(decoded.Choices) == 0 {
		return nil, errors.New("AI response did not include a choice")
	}

	var draft furiganaAIDraft
	if err := decodeFuriganaAIDraft(decoded.Choices[0].Message.Content, &draft); err != nil {
		return nil, err
	}
	return validateFuriganaAISegments(text, draft.Segments)
}

func decodeFuriganaAIDraft(content string, draft *furiganaAIDraft) error {
	content = strings.TrimSpace(content)
	if err := json.Unmarshal([]byte(content), draft); err == nil {
		return nil
	}
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return errors.New("AI response was not valid JSON")
	}
	return json.Unmarshal([]byte(content[start:end+1]), draft)
}

func validateFuriganaAISegments(original string, segments []model.FuriganaSegment) ([]model.FuriganaSegment, error) {
	if len(segments) == 0 {
		return nil, errors.New("AI response did not include furigana segments")
	}

	validated := make([]model.FuriganaSegment, 0, len(segments))
	var reconstructed strings.Builder
	for _, segment := range segments {
		if segment.Text == "" {
			return nil, errors.New("AI response included an empty text segment")
		}
		reconstructed.WriteString(segment.Text)
		reading := normalizeKana(strings.TrimSpace(segment.Reading))
		if reading == "" {
			validated = appendPlainSegment(validated, segment.Text)
			continue
		}
		if !containsKanji(segment.Text) || !isHiraganaReading(reading) {
			return nil, errors.New("AI response included an invalid reading segment")
		}
		validated = appendMergedSegments(validated, alignTokenReading(segment.Text, reading))
	}
	if reconstructed.String() != original {
		return nil, errors.New("AI response changed the original text")
	}
	return validated, nil
}

func isHiraganaReading(reading string) bool {
	for _, r := range reading {
		if unicode.In(r, unicode.Hiragana) || r == 'ー' || r == '・' {
			continue
		}
		return false
	}
	return reading != ""
}

func japaneseTokenizer() (*tokenizer.Tokenizer, error) {
	sharedJapaneseTokenizerOnce.Do(func() {
		sharedJapaneseTokenizer, sharedJapaneseTokenizerErr = tokenizer.New(ipa.Dict(), tokenizer.OmitBosEos())
	})
	return sharedJapaneseTokenizer, sharedJapaneseTokenizerErr
}

func alignTokenReading(surface, reading string) []model.FuriganaSegment {
	runs := splitSurfaceRuns(surface)
	if len(runs) == 0 || !containsKanji(surface) {
		return []model.FuriganaSegment{{Text: surface}}
	}

	remaining := normalizeKana(reading)
	segments := make([]model.FuriganaSegment, 0, len(runs))
	for i, run := range runs {
		if run.kanji {
			readingEnd := len([]rune(remaining))
			for j := i + 1; j < len(runs); j++ {
				if runs[j].kanji {
					continue
				}
				nextKana := normalizeKana(runs[j].text)
				if index := strings.Index(remaining, nextKana); index >= 0 {
					readingEnd = len([]rune(remaining[:index]))
				}
				break
			}

			remainingRunes := []rune(remaining)
			if readingEnd <= 0 || readingEnd > len(remainingRunes) {
				return []model.FuriganaSegment{{Text: surface, Reading: normalizeKana(reading)}}
			}
			segments = append(segments, model.FuriganaSegment{
				Text:    run.text,
				Reading: string(remainingRunes[:readingEnd]),
			})
			remaining = string(remainingRunes[readingEnd:])
			continue
		}

		normalizedText := normalizeKana(run.text)
		if !strings.HasPrefix(remaining, normalizedText) {
			return []model.FuriganaSegment{{Text: surface, Reading: normalizeKana(reading)}}
		}
		segments = appendPlainSegment(segments, run.text)
		remaining = strings.TrimPrefix(remaining, normalizedText)
	}

	if remaining != "" {
		return []model.FuriganaSegment{{Text: surface, Reading: normalizeKana(reading)}}
	}
	return segments
}

func splitSurfaceRuns(surface string) []surfaceRun {
	runs := make([]surfaceRun, 0)
	var current strings.Builder
	var currentKanji bool
	for _, r := range surface {
		kanji := isKanji(r)
		if current.Len() > 0 && kanji != currentKanji {
			runs = append(runs, surfaceRun{text: current.String(), kanji: currentKanji})
			current.Reset()
		}
		currentKanji = kanji
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		runs = append(runs, surfaceRun{text: current.String(), kanji: currentKanji})
	}
	return runs
}

func appendMergedSegments(dst, src []model.FuriganaSegment) []model.FuriganaSegment {
	for _, segment := range src {
		if segment.Reading == "" {
			dst = appendPlainSegment(dst, segment.Text)
			continue
		}
		dst = append(dst, segment)
	}
	return dst
}

func appendPlainSegment(segments []model.FuriganaSegment, text string) []model.FuriganaSegment {
	if text == "" {
		return segments
	}
	if len(segments) > 0 && segments[len(segments)-1].Reading == "" {
		segments[len(segments)-1].Text += text
		return segments
	}
	return append(segments, model.FuriganaSegment{Text: text})
}

func containsKanji(text string) bool {
	for _, r := range text {
		if isKanji(r) {
			return true
		}
	}
	return false
}

func isKanji(r rune) bool {
	return unicode.In(r, unicode.Han) || r == '々' || r == '〆' || r == 'ヶ'
}

func normalizeKana(text string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'ァ' && r <= 'ヶ' {
			return r - 0x60
		}
		return r
	}, text)
}
