package transcript

import (
	"errors"
	"regexp"
	"strings"
)

var ErrEmpty = errors.New("transcript is empty")

var (
	timestampLine = regexp.MustCompile(`^\s*(?:\d{1,2}:)?\d{2}:\d{2}[.,]\d{3}\s+-->\s+(?:\d{1,2}:)?\d{2}:\d{2}[.,]\d{3}`)
	cueNumber     = regexp.MustCompile(`^\d+$`)
	markup        = regexp.MustCompile(`<[^>]+>`)
)

func Normalize(input string) (string, error) {
	input = strings.TrimPrefix(input, "\ufeff")
	lines := strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n")
	paragraphs := make([]string, 0)
	current := make([]string, 0)
	flush := func() {
		if len(current) == 0 {
			return
		}
		text := strings.TrimSpace(strings.Join(current, " "))
		if text != "" && (len(paragraphs) == 0 || paragraphs[len(paragraphs)-1] != text) {
			paragraphs = append(paragraphs, text)
		}
		current = current[:0]
	}
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			flush()
			continue
		}
		upper := strings.ToUpper(line)
		if upper == "WEBVTT" || strings.HasPrefix(upper, "NOTE ") || timestampLine.MatchString(line) || cueNumber.MatchString(line) {
			continue
		}
		line = strings.TrimSpace(markup.ReplaceAllString(line, ""))
		if line != "" {
			current = append(current, line)
		}
	}
	flush()
	text := strings.TrimSpace(strings.Join(paragraphs, "\n\n"))
	if text == "" {
		return "", ErrEmpty
	}
	return text, nil
}
