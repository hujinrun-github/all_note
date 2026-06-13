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

func TestNotionMarkdownHashIsStable(t *testing.T) {
	left := notionMarkdownHash("Paragraph\n\n")
	right := notionMarkdownHash("Paragraph\n\n")
	if left == "" || left != right {
		t.Fatalf("hash not stable: %q %q", left, right)
	}
}
