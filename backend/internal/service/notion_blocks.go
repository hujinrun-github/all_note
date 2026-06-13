package service

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

type notionBlock struct {
	ID               string          `json:"id,omitempty"`
	Type             string          `json:"type"`
	HasChildren      bool            `json:"has_children,omitempty"`
	Paragraph        notionTextBlock `json:"paragraph,omitempty"`
	Heading1         notionTextBlock `json:"heading_1,omitempty"`
	Heading2         notionTextBlock `json:"heading_2,omitempty"`
	Heading3         notionTextBlock `json:"heading_3,omitempty"`
	BulletedListItem notionTextBlock `json:"bulleted_list_item,omitempty"`
	NumberedListItem notionTextBlock `json:"numbered_list_item,omitempty"`
	ToDo             notionToDoBlock `json:"to_do,omitempty"`
	Quote            notionTextBlock `json:"quote,omitempty"`
	Code             notionCodeBlock `json:"code,omitempty"`
	Divider          map[string]any  `json:"divider,omitempty"`
}

type notionRichText struct {
	PlainText   string            `json:"plain_text"`
	Href        *string           `json:"href"`
	Annotations notionAnnotations `json:"annotations"`
}

type notionAnnotations struct {
	Bold          bool `json:"bold"`
	Italic        bool `json:"italic"`
	Strikethrough bool `json:"strikethrough"`
	Code          bool `json:"code"`
}

type notionTextBlock struct {
	RichText []notionRichText `json:"rich_text"`
}

type notionToDoBlock struct {
	RichText []notionRichText `json:"rich_text"`
	Checked  bool             `json:"checked"`
}

type notionCodeBlock struct {
	RichText []notionRichText `json:"rich_text"`
	Language string           `json:"language"`
}

type notionMarkdownConversion struct {
	Markdown         string
	UnsupportedTypes []string
}

var (
	numberedListPattern = regexp.MustCompile(`^\d+\.\s+(.+)$`)
	todoPattern         = regexp.MustCompile(`^-\s+\[([ xX])\]\s+(.+)$`)
)

func notionBlocksToMarkdown(blocks []notionBlock) notionMarkdownConversion {
	lines := make([]string, 0, len(blocks)*2)
	unsupportedTypes := make([]string, 0)

	for _, block := range blocks {
		switch block.Type {
		case "heading_1":
			lines = append(lines, "# "+notionRichTextToMarkdown(block.Heading1.RichText), "")
		case "heading_2":
			lines = append(lines, "## "+notionRichTextToMarkdown(block.Heading2.RichText), "")
		case "heading_3":
			lines = append(lines, "### "+notionRichTextToMarkdown(block.Heading3.RichText), "")
		case "paragraph":
			lines = append(lines, escapeMarkdownParagraph(notionRichTextToMarkdown(block.Paragraph.RichText)), "")
		case "bulleted_list_item":
			lines = append(lines, "- "+notionRichTextToMarkdown(block.BulletedListItem.RichText))
		case "numbered_list_item":
			lines = append(lines, "1. "+notionRichTextToMarkdown(block.NumberedListItem.RichText))
		case "to_do":
			check := " "
			if block.ToDo.Checked {
				check = "x"
			}
			lines = append(lines, "- ["+check+"] "+notionRichTextToMarkdown(block.ToDo.RichText))
		case "quote":
			lines = append(lines, "> "+notionRichTextToMarkdown(block.Quote.RichText), "")
		case "code":
			code := notionPlainText(block.Code.RichText)
			fence := notionCodeFence(code)
			lines = append(lines, fence+strings.TrimSpace(block.Code.Language), code, fence, "")
		case "divider":
			lines = append(lines, "---", "")
		default:
			unsupportedTypes = append(unsupportedTypes, block.Type)
			lines = append(lines, "[Unsupported Notion block: "+block.Type+"]", "")
		}
	}

	return notionMarkdownConversion{
		Markdown:         strings.Join(lines, "\n"),
		UnsupportedTypes: unsupportedTypes,
	}
}

func markdownToNotionBlocks(markdown string) []notionBlock {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	blocks := make([]notionBlock, 0, len(lines))

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if text, ok := unescapeMarkdownParagraph(trimmed); ok {
			blocks = append(blocks, notionTextMarkdownBlock("paragraph", text))
			continue
		}

		if fence, language, ok := markdownCodeFence(trimmed); ok {
			codeLines := make([]string, 0)
			for i++; i < len(lines); i++ {
				if strings.TrimSpace(lines[i]) == fence {
					break
				}
				codeLines = append(codeLines, lines[i])
			}
			blocks = append(blocks, notionBlock{
				Type: "code",
				Code: notionCodeBlock{
					Language: language,
					RichText: []notionRichText{{PlainText: strings.Join(codeLines, "\n")}},
				},
			})
			continue
		}

		switch {
		case trimmed == "---":
			blocks = append(blocks, notionBlock{Type: "divider", Divider: map[string]any{}})
		case strings.HasPrefix(trimmed, "### "):
			blocks = append(blocks, notionTextMarkdownBlock("heading_3", strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))))
		case strings.HasPrefix(trimmed, "## "):
			blocks = append(blocks, notionTextMarkdownBlock("heading_2", strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))))
		case strings.HasPrefix(trimmed, "# "):
			blocks = append(blocks, notionTextMarkdownBlock("heading_1", strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))))
		case matchesToDo(trimmed):
			blocks = append(blocks, markdownToDoBlock(trimmed))
		case strings.HasPrefix(trimmed, "- "):
			blocks = append(blocks, notionTextMarkdownBlock("bulleted_list_item", strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))))
		case numberedListPattern.MatchString(trimmed):
			text := numberedListPattern.FindStringSubmatch(trimmed)[1]
			blocks = append(blocks, notionTextMarkdownBlock("numbered_list_item", strings.TrimSpace(text)))
		case strings.HasPrefix(trimmed, "> "):
			blocks = append(blocks, notionTextMarkdownBlock("quote", strings.TrimSpace(strings.TrimPrefix(trimmed, "> "))))
		default:
			blocks = append(blocks, notionTextMarkdownBlock("paragraph", trimmed))
		}
	}

	return blocks
}

func notionMarkdownHash(markdown string) string {
	sum := sha256.Sum256([]byte(canonicalNotionMarkdown(markdown)))
	return hex.EncodeToString(sum[:])
}

func canonicalNotionMarkdown(markdown string) string {
	markdown = strings.ReplaceAll(markdown, "\r\n", "\n")
	markdown = strings.ReplaceAll(markdown, "\r", "\n")
	markdown = strings.TrimRight(markdown, "\n")
	if markdown == "" {
		return ""
	}
	return markdown + "\n"
}

func notionRichTextToMarkdown(text []notionRichText) string {
	var b strings.Builder
	for _, part := range text {
		value := part.PlainText
		if part.Annotations.Code {
			value = "`" + value + "`"
		}
		if part.Annotations.Bold {
			value = "**" + value + "**"
		}
		if part.Annotations.Italic {
			value = "*" + value + "*"
		}
		if part.Annotations.Strikethrough {
			value = "~~" + value + "~~"
		}
		if part.Href != nil && strings.TrimSpace(*part.Href) != "" {
			value = "[" + value + "](" + strings.TrimSpace(*part.Href) + ")"
		}
		b.WriteString(value)
	}
	return b.String()
}

func notionPlainText(text []notionRichText) string {
	var b strings.Builder
	for _, part := range text {
		b.WriteString(part.PlainText)
	}
	return b.String()
}

func escapeMarkdownParagraph(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = escapeMarkdownParagraphLine(lines[i])
	}
	return strings.Join(lines, "\n")
}

func escapeMarkdownParagraphLine(text string) string {
	if strings.HasPrefix(text, "# ") || strings.HasPrefix(text, "## ") || strings.HasPrefix(text, "### ") {
		return `\` + text
	}
	if text == "---" {
		return `\` + text
	}
	if strings.HasPrefix(text, "- ") {
		return `\` + text
	}
	if numberedListPattern.MatchString(text) {
		return strings.Replace(text, ".", `\.`, 1)
	}
	if strings.HasPrefix(text, "> ") {
		return `\` + text
	}
	if fence, _, ok := markdownCodeFence(text); ok && strings.HasPrefix(text, fence) {
		return `\` + text
	}
	return text
}

func unescapeMarkdownParagraph(line string) (string, bool) {
	if strings.HasPrefix(line, `\#`) ||
		strings.HasPrefix(line, `\---`) ||
		strings.HasPrefix(line, `\- `) ||
		strings.HasPrefix(line, `\> `) ||
		strings.HasPrefix(line, "\\```") {
		return line[1:], true
	}

	for i := 0; i < len(line); i++ {
		if line[i] < '0' || line[i] > '9' {
			if i > 0 && strings.HasPrefix(line[i:], `\. `) {
				return line[:i] + "." + line[i+2:], true
			}
			return "", false
		}
	}
	return "", false
}

func notionCodeFence(code string) string {
	longestRun := 0
	currentRun := 0
	for _, char := range code {
		if char == '`' {
			currentRun++
			if currentRun > longestRun {
				longestRun = currentRun
			}
			continue
		}
		currentRun = 0
	}
	if longestRun < 3 {
		longestRun = 2
	}
	return strings.Repeat("`", longestRun+1)
}

func markdownCodeFence(line string) (string, string, bool) {
	if !strings.HasPrefix(line, "```") {
		return "", "", false
	}
	count := 0
	for count < len(line) && line[count] == '`' {
		count++
	}
	if count < 3 {
		return "", "", false
	}
	return line[:count], strings.TrimSpace(line[count:]), true
}

func notionTextMarkdownBlock(blockType, text string) notionBlock {
	richText := []notionRichText{{PlainText: text}}
	block := notionBlock{Type: blockType}
	switch blockType {
	case "heading_1":
		block.Heading1 = notionTextBlock{RichText: richText}
	case "heading_2":
		block.Heading2 = notionTextBlock{RichText: richText}
	case "heading_3":
		block.Heading3 = notionTextBlock{RichText: richText}
	case "bulleted_list_item":
		block.BulletedListItem = notionTextBlock{RichText: richText}
	case "numbered_list_item":
		block.NumberedListItem = notionTextBlock{RichText: richText}
	case "quote":
		block.Quote = notionTextBlock{RichText: richText}
	default:
		block.Type = "paragraph"
		block.Paragraph = notionTextBlock{RichText: richText}
	}
	return block
}

func matchesToDo(line string) bool {
	return todoPattern.MatchString(line)
}

func markdownToDoBlock(line string) notionBlock {
	matches := todoPattern.FindStringSubmatch(line)
	checked := matches[1] == "x" || matches[1] == "X"
	return notionBlock{
		Type: "to_do",
		ToDo: notionToDoBlock{
			Checked:  checked,
			RichText: []notionRichText{{PlainText: strings.TrimSpace(matches[2])}},
		},
	}
}
