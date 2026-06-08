package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

type obsidianParsedMarkdown struct {
	ID       string
	Title    string
	Body     string
	FolderID string
	TagsJSON string
	Hash     string
}

type obsidianFrontmatter struct {
	ID      string   `yaml:"id"`
	Source  string   `yaml:"source"`
	Folder  string   `yaml:"folder"`
	Tags    []string `yaml:"tags"`
	Created string   `yaml:"created"`
	Updated string   `yaml:"updated"`
}

func parseObsidianMarkdown(raw []byte, fileName string) (*obsidianParsedMarkdown, error) {
	body := string(raw)
	var frontmatter obsidianFrontmatter
	if rawFrontmatter, markdownBody, ok := splitObsidianFrontmatter(body); ok {
		if strings.TrimSpace(rawFrontmatter) != "" {
			if err := yaml.Unmarshal([]byte(rawFrontmatter), &frontmatter); err != nil {
				return nil, fmt.Errorf("parse obsidian frontmatter: %w", err)
			}
		}
		body = strings.TrimLeft(markdownBody, "\r\n")
	}

	title := titleFromMarkdownBody(body)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	}

	body = stripDuplicateHeading(body, title)
	body = ensureTrailingNewline(body)

	folderID := strings.TrimSpace(frontmatter.Folder)
	if folderID == "" {
		folderID = "__uncategorized"
	}
	tagsJSON, err := json.Marshal(cleanTags(frontmatter.Tags))
	if err != nil {
		return nil, fmt.Errorf("encode obsidian tags: %w", err)
	}
	sum := sha256.Sum256(raw)

	return &obsidianParsedMarkdown{
		ID:       strings.TrimSpace(frontmatter.ID),
		Title:    title,
		Body:     body,
		FolderID: folderID,
		TagsJSON: string(tagsJSON),
		Hash:     hex.EncodeToString(sum[:]),
	}, nil
}

func splitObsidianFrontmatter(markdown string) (string, string, bool) {
	for _, delimiters := range []struct {
		opening string
		closing string
	}{
		{opening: "---\n", closing: "\n---\n"},
		{opening: "---\r\n", closing: "\r\n---\r\n"},
	} {
		if !strings.HasPrefix(markdown, delimiters.opening) {
			continue
		}
		rest := markdown[len(delimiters.opening):]
		closingAt := strings.Index(rest, delimiters.closing)
		if closingAt == -1 {
			return "", markdown, false
		}
		bodyAt := closingAt + len(delimiters.closing)
		return rest[:closingAt], rest[bodyAt:], true
	}
	return "", markdown, false
}

func titleFromMarkdownBody(body string) string {
	for _, line := range strings.SplitAfter(body, "\n") {
		if title, ok := parseH1Line(line); ok {
			return title
		}
	}
	return ""
}

func stripDuplicateHeading(body, title string) string {
	title = strings.TrimSpace(title)
	if title == "" || body == "" {
		return body
	}

	lines := strings.SplitAfter(body, "\n")
	headingAt := 0
	for headingAt < len(lines) && strings.TrimSpace(lines[headingAt]) == "" {
		headingAt++
	}
	if headingAt >= len(lines) {
		return ""
	}
	headingTitle, ok := parseH1Line(lines[headingAt])
	if !ok || headingTitle != title {
		return body
	}

	bodyAt := headingAt + 1
	for bodyAt < len(lines) && strings.TrimSpace(lines[bodyAt]) == "" {
		bodyAt++
	}
	return strings.Join(lines[bodyAt:], "")
}

func ensureTrailingNewline(value string) string {
	if value == "" || strings.HasSuffix(value, "\n") {
		return value
	}
	return value + "\n"
}

func cleanTags(tags []string) []string {
	cleaned := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			cleaned = append(cleaned, tag)
		}
	}
	return cleaned
}

func parseH1Line(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "# ") {
		return "", false
	}
	title := strings.TrimSpace(strings.TrimPrefix(line, "# "))
	return title, title != ""
}
