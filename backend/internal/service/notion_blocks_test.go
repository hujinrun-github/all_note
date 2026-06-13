package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNotionBlocksToMarkdownCoversSupportedBlocks(t *testing.T) {
	raw := []byte(`[
		{"id":"h1","type":"heading_1","heading_1":{"rich_text":[{"plain_text":"Plan","annotations":{},"href":null}]},"has_children":false},
		{"id":"p1","type":"paragraph","paragraph":{"rich_text":[{"plain_text":"Read ","annotations":{},"href":null},{"plain_text":"docs","annotations":{"bold":true},"href":"https://example.com"}]},"has_children":false},
		{"id":"b1","type":"bulleted_list_item","bulleted_list_item":{"rich_text":[{"plain_text":"bullet","annotations":{},"href":null}]},"has_children":false},
		{"id":"n1","type":"numbered_list_item","numbered_list_item":{"rich_text":[{"plain_text":"numbered","annotations":{},"href":null}]},"has_children":false},
		{"id":"t1","type":"to_do","to_do":{"checked":true,"rich_text":[{"plain_text":"done","annotations":{},"href":null}]},"has_children":false},
		{"id":"q1","type":"quote","quote":{"rich_text":[{"plain_text":"quote","annotations":{},"href":null}]},"has_children":false},
		{"id":"c1","type":"code","code":{"language":"go","rich_text":[{"plain_text":"fmt.Println(\"hi\")","annotations":{},"href":null}]},"has_children":false},
		{"id":"d1","type":"divider","divider":{},"has_children":false}
	]`)
	var blocks []notionBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatalf("decode blocks: %v", err)
	}

	converted := notionBlocksToMarkdown(blocks)

	want := strings.Join([]string{
		"# Plan",
		"",
		"Read [**docs**](https://example.com)",
		"",
		"- bullet",
		"1. numbered",
		"- [x] done",
		"> quote",
		"",
		"```go",
		"fmt.Println(\"hi\")",
		"```",
		"",
		"---",
		"",
	}, "\n")
	if converted.Markdown != want {
		t.Fatalf("markdown mismatch\nwant:\n%q\ngot:\n%q", want, converted.Markdown)
	}
	if len(converted.UnsupportedTypes) != 0 {
		t.Fatalf("unexpected unsupported types: %#v", converted.UnsupportedTypes)
	}
}

func TestNotionBlocksToMarkdownMarksUnsupportedBlocks(t *testing.T) {
	blocks := []notionBlock{{ID: "table-1", Type: "table"}}

	converted := notionBlocksToMarkdown(blocks)

	if !strings.Contains(converted.Markdown, "[Unsupported Notion block: table]") {
		t.Fatalf("unsupported marker missing from markdown: %q", converted.Markdown)
	}
	if len(converted.UnsupportedTypes) != 1 || converted.UnsupportedTypes[0] != "table" {
		t.Fatalf("unsupported types = %#v", converted.UnsupportedTypes)
	}
}

func TestNotionParagraphMarkdownBlockMarkersRoundTripAsParagraphs(t *testing.T) {
	texts := []string{
		"# Title",
		"## Subtitle",
		"### Section",
		"---",
		"- item",
		"1. item",
		"> quote",
		"- [ ] task",
		"- [x] done",
		"```go",
	}
	blocks := make([]notionBlock, 0, len(texts))
	for _, text := range texts {
		blocks = append(blocks, notionBlock{
			Type:      "paragraph",
			Paragraph: notionTextBlock{RichText: []notionRichText{{PlainText: text}}},
		})
	}

	converted := notionBlocksToMarkdown(blocks)

	wantMarkdown := strings.Join([]string{
		`\# Title`,
		"",
		`\## Subtitle`,
		"",
		`\### Section`,
		"",
		`\---`,
		"",
		`\- item`,
		"",
		`1\. item`,
		"",
		`\> quote`,
		"",
		`\- [ ] task`,
		"",
		`\- [x] done`,
		"",
		"\\```go",
		"",
	}, "\n")
	if converted.Markdown != wantMarkdown {
		t.Fatalf("escaped markdown mismatch\nwant:\n%q\ngot:\n%q", wantMarkdown, converted.Markdown)
	}

	roundTripped := markdownToNotionBlocks(converted.Markdown)
	if len(roundTripped) != len(texts) {
		t.Fatalf("round-tripped block count = %d, want %d: %#v", len(roundTripped), len(texts), roundTripped)
	}
	for i, block := range roundTripped {
		if block.Type != "paragraph" {
			t.Fatalf("block %d type = %q, want paragraph", i, block.Type)
		}
		if got := block.Paragraph.RichText[0].PlainText; got != texts[i] {
			t.Fatalf("block %d text = %q, want %q", i, got, texts[i])
		}
	}
}

func TestNotionMultilineParagraphEscapesLeadingMarkerLine(t *testing.T) {
	blocks := []notionBlock{{
		Type: "paragraph",
		Paragraph: notionTextBlock{RichText: []notionRichText{{
			PlainText: "---\nstill paragraph",
		}}},
	}}

	converted := notionBlocksToMarkdown(blocks)

	wantMarkdown := strings.Join([]string{
		`\---`,
		"still paragraph",
		"",
	}, "\n")
	if converted.Markdown != wantMarkdown {
		t.Fatalf("escaped markdown mismatch\nwant:\n%q\ngot:\n%q", wantMarkdown, converted.Markdown)
	}

	roundTripped := markdownToNotionBlocks(converted.Markdown)
	wantTexts := []string{"---", "still paragraph"}
	if len(roundTripped) != len(wantTexts) {
		t.Fatalf("round-tripped block count = %d, want %d: %#v", len(roundTripped), len(wantTexts), roundTripped)
	}
	for i, block := range roundTripped {
		if block.Type != "paragraph" {
			t.Fatalf("block %d type = %q, want paragraph", i, block.Type)
		}
		if got := block.Paragraph.RichText[0].PlainText; got != wantTexts[i] {
			t.Fatalf("block %d text = %q, want %q", i, got, wantTexts[i])
		}
	}
}

func TestNotionMultilineParagraphEscapesLaterMarkerLines(t *testing.T) {
	blocks := []notionBlock{{
		Type: "paragraph",
		Paragraph: notionTextBlock{RichText: []notionRichText{{
			PlainText: strings.Join([]string{
				"intro",
				"- item",
				"> quote",
				"1. number",
				"- [x] task",
				"```go",
			}, "\n"),
		}}},
	}}

	converted := notionBlocksToMarkdown(blocks)

	wantMarkdown := strings.Join([]string{
		"intro",
		`\- item`,
		`\> quote`,
		`1\. number`,
		`\- [x] task`,
		"\\```go",
		"",
	}, "\n")
	if converted.Markdown != wantMarkdown {
		t.Fatalf("escaped markdown mismatch\nwant:\n%q\ngot:\n%q", wantMarkdown, converted.Markdown)
	}

	roundTripped := markdownToNotionBlocks(converted.Markdown)
	wantTexts := []string{"intro", "- item", "> quote", "1. number", "- [x] task", "```go"}
	if len(roundTripped) != len(wantTexts) {
		t.Fatalf("round-tripped block count = %d, want %d: %#v", len(roundTripped), len(wantTexts), roundTripped)
	}
	for i, block := range roundTripped {
		if block.Type != "paragraph" {
			t.Fatalf("block %d type = %q, want paragraph", i, block.Type)
		}
		if got := block.Paragraph.RichText[0].PlainText; got != wantTexts[i] {
			t.Fatalf("block %d text = %q, want %q", i, got, wantTexts[i])
		}
	}
}

func TestNotionMultilineParagraphEscapesIndentedMarkerLines(t *testing.T) {
	blocks := []notionBlock{{
		Type: "paragraph",
		Paragraph: notionTextBlock{RichText: []notionRichText{{
			PlainText: strings.Join([]string{
				"intro",
				"  ---",
				"  - item",
				"  # Title",
				"  1. item",
				"  ```go",
			}, "\n"),
		}}},
	}}

	converted := notionBlocksToMarkdown(blocks)

	wantMarkdown := strings.Join([]string{
		"intro",
		`  \---`,
		`  \- item`,
		`  \# Title`,
		`  1\. item`,
		"  \\```go",
		"",
	}, "\n")
	if converted.Markdown != wantMarkdown {
		t.Fatalf("escaped markdown mismatch\nwant:\n%q\ngot:\n%q", wantMarkdown, converted.Markdown)
	}

	roundTripped := markdownToNotionBlocks(converted.Markdown)
	wantTexts := []string{"intro", "  ---", "  - item", "  # Title", "  1. item", "  ```go"}
	if len(roundTripped) != len(wantTexts) {
		t.Fatalf("round-tripped block count = %d, want %d: %#v", len(roundTripped), len(wantTexts), roundTripped)
	}
	for i, block := range roundTripped {
		if block.Type != "paragraph" {
			t.Fatalf("block %d type = %q, want paragraph", i, block.Type)
		}
		if got := block.Paragraph.RichText[0].PlainText; got != wantTexts[i] {
			t.Fatalf("block %d text = %q, want %q", i, got, wantTexts[i])
		}
	}
}

func TestNotionMultilineParagraphEscapesTrailingSpaceMarkerLines(t *testing.T) {
	blocks := []notionBlock{{
		Type: "paragraph",
		Paragraph: notionTextBlock{RichText: []notionRichText{{
			PlainText: strings.Join([]string{
				"--- ",
				"- item ",
				"# Title ",
				"1. item ",
				"```go ",
			}, "\n"),
		}}},
	}}

	converted := notionBlocksToMarkdown(blocks)

	wantMarkdown := strings.Join([]string{
		`\--- `,
		`\- item `,
		`\# Title `,
		`1\. item `,
		"\\```go ",
		"",
	}, "\n")
	if converted.Markdown != wantMarkdown {
		t.Fatalf("escaped markdown mismatch\nwant:\n%q\ngot:\n%q", wantMarkdown, converted.Markdown)
	}

	roundTripped := markdownToNotionBlocks(converted.Markdown)
	wantTexts := []string{"--- ", "- item ", "# Title ", "1. item ", "```go "}
	if len(roundTripped) != len(wantTexts) {
		t.Fatalf("round-tripped block count = %d, want %d: %#v", len(roundTripped), len(wantTexts), roundTripped)
	}
	for i, block := range roundTripped {
		if block.Type != "paragraph" {
			t.Fatalf("block %d type = %q, want paragraph", i, block.Type)
		}
		if got := block.Paragraph.RichText[0].PlainText; got != wantTexts[i] {
			t.Fatalf("block %d text = %q, want %q", i, got, wantTexts[i])
		}
	}
}

func TestMarkdownToNotionBlocksCoversSupportedMarkdown(t *testing.T) {
	markdown := strings.Join([]string{
		"# Title",
		"",
		"Paragraph",
		"",
		"- bullet",
		"1. first",
		"- [ ] open",
		"- [x] done",
		"> quote",
		"",
		"```go",
		"fmt.Println(\"hi\")",
		"```",
		"",
		"---",
	}, "\n")

	blocks := markdownToNotionBlocks(markdown)

	types := make([]string, 0, len(blocks))
	for _, block := range blocks {
		types = append(types, block.Type)
	}
	want := []string{"heading_1", "paragraph", "bulleted_list_item", "numbered_list_item", "to_do", "to_do", "quote", "code", "divider"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("types = %#v, want %#v", types, want)
	}
}

func TestNotionMarkdownHashNormalizesEquivalentNewlines(t *testing.T) {
	want := notionMarkdownHash("Paragraph\n")
	variants := []string{
		"Paragraph",
		"Paragraph\n\n",
		"Paragraph\r\n",
		"Paragraph\r\n\r\n",
	}

	for _, variant := range variants {
		if got := notionMarkdownHash(variant); got != want {
			t.Fatalf("hash for %q = %q, want %q", variant, got, want)
		}
	}
}

func TestNotionCodeBlockUsesFenceThatDoesNotCollideWithContent(t *testing.T) {
	code := strings.Join([]string{
		"fmt.Println(\"before\")",
		"```",
		"fmt.Println(\"after\")",
	}, "\n")
	blocks := []notionBlock{{
		Type: "code",
		Code: notionCodeBlock{
			Language: "go",
			RichText: []notionRichText{{
				PlainText: code,
			}},
		},
	}}

	converted := notionBlocksToMarkdown(blocks)

	wantMarkdown := strings.Join([]string{
		"````go",
		"fmt.Println(\"before\")",
		"```",
		"fmt.Println(\"after\")",
		"````",
		"",
	}, "\n")
	if converted.Markdown != wantMarkdown {
		t.Fatalf("code markdown mismatch\nwant:\n%q\ngot:\n%q", wantMarkdown, converted.Markdown)
	}

	roundTripped := markdownToNotionBlocks(converted.Markdown)
	if len(roundTripped) != 1 {
		t.Fatalf("round-tripped block count = %d, want 1: %#v", len(roundTripped), roundTripped)
	}
	if roundTripped[0].Type != "code" {
		t.Fatalf("round-tripped type = %q, want code", roundTripped[0].Type)
	}
	if got := roundTripped[0].Code.RichText[0].PlainText; got != code {
		t.Fatalf("round-tripped code = %q, want %q", got, code)
	}
}

func TestNotionMarkdownHashIsStable(t *testing.T) {
	left := notionMarkdownHash("Paragraph\n\n")
	right := notionMarkdownHash("Paragraph\n\n")
	if left == "" || left != right {
		t.Fatalf("hash not stable: %q %q", left, right)
	}
}
