package artifactfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/parchment/internal/artifact"
)

func testArtifact() artifact.Artifact {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	return artifact.Artifact{
		ID: "n12345", Kind: artifact.NoteKind, Title: "Example",
		CreatedAt: now, ModifiedAt: now, FormatVersion: artifact.FormatVersion,
	}
}

func TestEncodeDecodePreservesMarkdownAndStructuredBlocks(t *testing.T) {
	body := "# Example\n\nText.\n\n```go\n```parchment-not-a-payload\nignored\n```\n"
	data, err := Encode(testArtifact(), body, map[string]any{
		"parchment-note": map[string]string{"format": "example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "```parchment-meta\n") {
		t.Fatalf("metadata block is not first: %s", data)
	}
	if strings.Index(string(data), bodyBoundary) > strings.Index(string(data), "```parchment-note") {
		t.Fatalf("structured block does not follow the body:\n%s", data)
	}
	file, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if file.Body != body {
		t.Fatalf("body = %q, want %q", file.Body, body)
	}
	if file.Artifact.Title != "Example" {
		t.Fatalf("artifact metadata = %+v", file.Artifact)
	}
	var payload map[string]string
	if err := json.Unmarshal(file.Blocks["parchment-note"], &payload); err != nil {
		t.Fatal(err)
	}
	if payload["format"] != "example" {
		t.Fatalf("payload = %v", payload)
	}
}

func TestReadMetadataDistinguishesMissingFromInvalidEnvelope(t *testing.T) {
	if _, err := ReadMetadata([]byte("# Plain Markdown\n")); !errors.Is(err, ErrMetadataMissing) {
		t.Fatalf("missing metadata error = %v", err)
	}
	if _, err := ReadMetadata([]byte("```parchment-meta\nnot JSON\n```\n\n<!-- parchment-body -->\nbody\n")); err == nil ||
		errors.Is(err, ErrMetadataMissing) {
		t.Fatalf("invalid metadata error = %v", err)
	}
	if _, err := ReadMetadata([]byte("```parchment-note\n{}\n```\n")); !errors.Is(err, ErrMetadataMissing) {
		t.Fatalf("non-metadata leading fence error = %v", err)
	}
	if _, err := ReadMetadata([]byte("```parchment-meta extra\n{}\n```\n")); err == nil ||
		errors.Is(err, ErrMetadataMissing) {
		t.Fatalf("malformed metadata opening error = %v", err)
	}
	data, err := Encode(testArtifact(), "body", nil)
	if err != nil {
		t.Fatal(err)
	}
	padded := append([]byte("\n\n"), data...)
	metadata, err := ReadMetadata(padded)
	if err != nil || metadata.Kind != testArtifact().Kind ||
		!metadata.CreatedAt.Equal(testArtifact().CreatedAt) {
		t.Fatalf("metadata after leading blank lines = %+v, %v", metadata, err)
	}
	file, err := Decode(padded)
	if err != nil || file.Body != "body" {
		t.Fatalf("decoded artifact after leading blank lines = %+v, %v", file, err)
	}
}

func TestReadMetadataRequiresCurrentFormatMarker(t *testing.T) {
	metadata, err := json.Marshal(testArtifact())
	if err != nil {
		t.Fatal(err)
	}
	source := "```parchment-meta\n" + string(metadata) + "\n```\n"
	for _, read := range []func([]byte) (artifact.Artifact, error){
		ReadMetadata,
		func(data []byte) (artifact.Artifact, error) {
			return ReadMetadataFrom(bytes.NewReader(data))
		},
	} {
		if _, err := read([]byte(source)); err == nil || errors.Is(err, ErrMetadataMissing) {
			t.Fatalf("metadata without current format marker error = %v", err)
		}
	}
}

func TestEncodeValidationFailure(t *testing.T) {
	invalidArtifact := artifact.Artifact{}
	_, err := Encode(invalidArtifact, "body", nil)
	if err == nil {
		t.Fatal("expected error when encoding invalid artifact, got nil")
	}
	if !strings.Contains(err.Error(), "validate artifact metadata:") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestEncodeMaxFileSizeValidation(t *testing.T) {
	largeBody := strings.Repeat("a", MaxFileSize)
	_, err := Encode(testArtifact(), largeBody, nil)
	if err == nil {
		t.Fatal("expected error when output exceeds MaxFileSize, got nil")
	}
	if !strings.Contains(err.Error(), "artifact file is larger than 64 MiB") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestEncodeBlockValidation(t *testing.T) {
	art := testArtifact()

	t.Run("invalid block name", func(t *testing.T) {
		invalidNames := []string{"invalid-name", "parchment-meta", ""}
		for _, name := range invalidNames {
			_, err := Encode(art, "body", map[string]any{name: map[string]string{}})
			if err == nil {
				t.Fatalf("expected error for block name %q, got nil", name)
			}
			if !strings.Contains(err.Error(), "invalid Parchment block name") {
				t.Fatalf("unexpected error for block name %q: %v", name, err)
			}
		}
	})

	t.Run("invalid json raw message", func(t *testing.T) {
		_, err := Encode(art, "body", map[string]any{
			"parchment-data": json.RawMessage(`{invalid json`),
		})
		if err == nil {
			t.Fatal("expected error for invalid json raw message, got nil")
		}
		if !strings.Contains(err.Error(), "invalid JSON") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("unmarshalable block struct", func(t *testing.T) {
		_, err := Encode(art, "body", map[string]any{
			"parchment-data": make(chan int),
		})
		if err == nil {
			t.Fatal("expected error for unmarshalable block payload, got nil")
		}
		if !strings.Contains(err.Error(), "encode parchment-data block:") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})
}

func TestTitleFromBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"empty", "", "Untitled"},
		{"whitespace only", "   \n\t\n  \r\n", "Untitled"},
		{"simple text", "Hello World", "Hello World"},
		{"heading", "# My Title", "My Title"},
		{"heading with spaces", "   #   My Title   \nSecond line", "My Title"},
		{"leading blank lines", "\n\n  \n# Header\nBody", "Header"},
		{"crlf line endings", "\r\n\r\n# Title CRLF\r\nBody", "Title CRLF"},
		{"plain first non-empty line", "First line without hash\n# Second line", "First line without hash"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := titleFromBody(tt.body)
			if got != tt.want {
				t.Errorf("titleFromBody(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func BenchmarkTitleFromBody(b *testing.B) {
	smallBody := "# Small Document\nThis is a small body text.\nSecond line.\n"
	largeBody := strings.Repeat("This is a random line in a large markdown file.\n", 10000) + "# Title at the End\n"

	b.Run("SmallBody", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = titleFromBody(smallBody)
		}
	})

	b.Run("LargeBody", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = titleFromBody(largeBody)
		}
	})
}

func TestReadMetadataRejectsPreviousArtifactFormat(t *testing.T) {
	data, err := Encode(testArtifact(), "body", nil)
	if err != nil {
		t.Fatal(err)
	}
	previous := strings.Replace(
		strings.Replace(string(data), "parchment-single-file-v2", "parchment-single-file-v1", 1),
		`"format_version": 2`, `"format_version": 1`, 1,
	)
	if _, err := ReadMetadata([]byte(previous)); err == nil {
		t.Fatal("accepted the previous artifact format")
	}
}

func TestReadMetadataRejectsRuntimeFields(t *testing.T) {
	data, err := Encode(testArtifact(), "body", nil)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(string(data), `"kind": "note",`,
		`"kind": "note",`+"\n  "+`"id": "n12345",`, 1)
	if _, err := ReadMetadata([]byte(source)); err == nil ||
		!strings.Contains(err.Error(), `unexpected Parchment metadata field "id"`) {
		t.Fatalf("runtime metadata field error = %v", err)
	}
}

func TestReadMetadataRejectsCorruptEmbeddedMetadata(t *testing.T) {
	for _, continuation := range []string{
		"<!-- parchment-body -->\nbody\n",
		"```parchment-document\n{}\n```\n\n<!-- parchment-body -->\nbody\n",
	} {
		source := "```parchment-meta\n{}\n```\n\n" + continuation
		for _, read := range []func([]byte) (artifact.Artifact, error){
			ReadMetadata,
			func(data []byte) (artifact.Artifact, error) {
				return ReadMetadataFrom(bytes.NewReader(data))
			},
		} {
			if _, err := read([]byte(source)); err == nil || errors.Is(err, ErrMetadataMissing) {
				t.Fatalf("corrupt embedded metadata error = %v", err)
			}
		}
	}
}

func TestEncodeDecodePreservesBodyStartingWithReservedFence(t *testing.T) {
	for _, body := range []string{
		"```parchment-footnote\nnot JSON\n```\n",
		"```parchment-note\n{\"text\":\"visible body\"}\n```\n",
	} {
		data, err := Encode(testArtifact(), body, nil)
		if err != nil {
			t.Fatal(err)
		}
		file, err := Decode(data)
		if err != nil {
			t.Fatalf("Decode body %q: %v", body, err)
		}
		if file.Body != body {
			t.Errorf("body = %q, want %q", file.Body, body)
		}
		if len(file.Blocks) != 0 {
			t.Errorf("body fence was parsed as envelope blocks: %v", file.Blocks)
		}
	}
}

func TestDecodeTreatsReservedFencesAsBodyWithoutTrailingBoundary(t *testing.T) {
	body := "```parchment-note\n{\"text\":\"visible body\"}\n```\n"
	data, err := Encode(testArtifact(), body, nil)
	if err != nil {
		t.Fatal(err)
	}
	file, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if file.Body != body || len(file.Blocks) != 0 {
		t.Fatalf("body = %q, blocks = %v", file.Body, file.Blocks)
	}
}

func TestDecodePreservesBodyContainingTrailingBoundaryAndReservedFence(t *testing.T) {
	body := "# Example\n\n" + trailingBlocksBoundary +
		"\n\n```parchment-note\n{\"text\":\"body content\"}\n```\n\nMore body.\n"
	data, err := Encode(testArtifact(), body, map[string]any{
		"parchment-note": map[string]string{"text": "payload"},
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if file.Body != body || len(file.Blocks) != 1 {
		t.Fatalf("body = %q, blocks = %v", file.Body, file.Blocks)
	}
	var payload map[string]string
	if err := json.Unmarshal(file.Blocks["parchment-note"], &payload); err != nil {
		t.Fatal(err)
	}
	if payload["text"] != "payload" {
		t.Fatalf("payload = %v", payload)
	}
}

func TestDecodeRequiresBodyBoundary(t *testing.T) {
	body := "# Example\n\nMarkdown body.\n"
	data, err := Encode(testArtifact(), body, nil)
	if err != nil {
		t.Fatal(err)
	}
	withoutBoundary := strings.Replace(string(data), bodyBoundary+"\n", "", 1)
	if _, err := Decode([]byte(withoutBoundary)); err == nil {
		t.Fatal("decoded artifact without body separator")
	}
}

func TestDecodeRejectsPayloadBlocksBeforeBody(t *testing.T) {
	data, err := Encode(testArtifact(), "body", map[string]any{
		"parchment-note": map[string]string{"kind": "note"},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	payloadStart := strings.Index(source, "```parchment-note")
	boundaryStart := strings.Index(source, bodyBoundary)
	metadataEnd := strings.Index(source, "```\n\n")
	if payloadStart < 0 || boundaryStart < 0 || metadataEnd < 0 {
		t.Fatalf("encoded block boundaries not found:\n%s", source)
	}
	payloadEnd := strings.Index(source[payloadStart:], "```\n") + payloadStart + 4
	preBody := source[:metadataEnd+4] + "\n" + source[payloadStart:payloadEnd] +
		"\n" + source[boundaryStart:]
	if _, err := Decode([]byte(preBody)); err == nil {
		t.Fatal("accepted a payload block before the body")
	}
}

func TestDecodeRejectsMalformedTrailingBlockOpening(t *testing.T) {
	data, err := Encode(testArtifact(), "body", map[string]any{
		"parchment-note": map[string]string{"kind": "note"},
	})
	if err != nil {
		t.Fatal(err)
	}
	malformed := strings.Replace(string(data), "```parchment-note\n", "```parchment-note extra\n", 1)
	if _, err := Decode([]byte(malformed)); err == nil {
		t.Fatal("accepted malformed trailing block opening")
	}
}

func TestEncodePreservesRawBlockFormatting(t *testing.T) {
	raw := json.RawMessage(`{"compact":true,"nested":{"count":2}}`)
	data, err := Encode(testArtifact(), "", map[string]any{"parchment-note": raw})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, raw) {
		t.Fatalf("raw JSON block was reformatted:\n%s", data)
	}
	file, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(file.Blocks["parchment-note"], raw) {
		t.Fatalf("decoded block = %s, want %s", file.Blocks["parchment-note"], raw)
	}
}

func TestDecodeSkipsMultipleSeparatingBlankLinesAndPreservesBodyWhitespace(t *testing.T) {
	encoded, err := Encode(testArtifact(), "Markdown body\n", map[string]any{
		"parchment-note": map[string]string{"kind": "note"},
	})
	if err != nil {
		t.Fatal(err)
	}
	multiple := strings.Replace(string(encoded), "\n\n```parchment-note", "\n\n\n\n```parchment-note", 1)
	multiple = strings.Replace(multiple, "\n\n"+bodyBoundary, "\n\n\n\n"+bodyBoundary, 1)
	file, err := Decode([]byte(multiple))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := file.Blocks["parchment-note"]; !ok || file.Body != "Markdown body\n" {
		t.Fatalf("decoded envelope = blocks %v, body %q", file.Blocks, file.Body)
	}

}

func TestReadMetadataFromStopsAfterMetadataBlock(t *testing.T) {
	data, err := Encode(testArtifact(), strings.Repeat("body ", 200_000), nil)
	if err != nil {
		t.Fatal(err)
	}
	reader := bytes.NewReader(append([]byte("\n\n"), data...))
	got, err := ReadMetadataFrom(reader)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != testArtifact().Kind || !got.CreatedAt.Equal(testArtifact().CreatedAt) {
		t.Fatalf("metadata = %+v", got)
	}
	if reader.Len() == 0 {
		t.Fatal("metadata reader consumed the artifact body")
	}
}

func TestReadMetadataFromStopsAtNonMetadataFirstLine(t *testing.T) {
	reader := bytes.NewReader([]byte(strings.Repeat("body", 250_000)))
	_, err := ReadMetadataFrom(reader)
	if !errors.Is(err, ErrMetadataMissing) {
		t.Fatalf("non-envelope metadata error = %v", err)
	}
	if reader.Len() == 0 {
		t.Fatal("metadata reader consumed a long Markdown line")
	}
}

func TestReadMetadataFromPreservesOversizedMetadataLines(t *testing.T) {
	metadata := testArtifact()
	data, err := Encode(metadata, "body", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadMetadataFrom(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != metadata.Kind || !got.CreatedAt.Equal(metadata.CreatedAt) {
		t.Fatalf("metadata was not preserved: %+v", got)
	}
}

func TestReadMetadataFromPreservesOversizedMetadataOpeningLine(t *testing.T) {
	data, err := Encode(testArtifact(), "body", nil)
	if err != nil {
		t.Fatal(err)
	}
	opener := "```parchment-meta" + strings.Repeat(" ", 5_000) + "\n"
	data = []byte(strings.Replace(string(data), "```parchment-meta\n", opener, 1))
	got, err := ReadMetadataFrom(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != testArtifact().Kind {
		t.Fatalf("metadata kind = %q", got.Kind)
	}
}

func TestReadMetadataParsesMinifiedCurrentMetadata(t *testing.T) {
	data, err := Encode(testArtifact(), "body", nil)
	if err != nil {
		t.Fatal(err)
	}
	minified := strings.Replace(
		string(data),
		`"parchment_format": "parchment-single-file-v2"`,
		`"parchment_format":"parchment-single-file-v2"`,
		1,
	)
	if minified == string(data) {
		t.Fatal("format marker was not found")
	}
	item, err := ReadMetadata([]byte(minified))
	if err != nil || item.Kind != testArtifact().Kind {
		t.Fatalf("read minified current metadata = %+v, %v", item, err)
	}
}

func TestEnvelopeHasExactlyTheSharedDiskFields(t *testing.T) {
	data, err := Encode(testArtifact(), "# Example\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	metadata, _, err := readBlock(string(data), 0, opening{marker: "```", name: metadataBlock})
	if err != nil {
		t.Fatal(err)
	}
	assertEnvelopeKeys(t, metadata)
}

func TestExamplesDecodeWithExactEnvelopeFields(t *testing.T) {
	for _, name := range []string{"note.md", "document.md", "budget.md", "presentation.md"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("../../examples", name))
			if err != nil {
				t.Fatal(err)
			}
			file, err := Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			if file.Artifact.Kind == "" {
				t.Fatal("artifact kind is missing")
			}
			metadata, _, err := readBlock(string(data), 0, opening{marker: "```", name: metadataBlock})
			if err != nil {
				t.Fatal(err)
			}
			assertEnvelopeKeys(t, metadata)
		})
	}
}

func assertEnvelopeKeys(t *testing.T, metadata []byte) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &fields); err != nil {
		t.Fatal(err)
	}
	want := []string{"parchment_format", "kind", "format_version", "created_at", "modified_at"}
	if len(fields) != len(want) {
		t.Fatalf("metadata fields = %v, want exactly %v", fields, want)
	}
	for _, key := range want {
		if _, ok := fields[key]; !ok {
			t.Errorf("metadata is missing %q", key)
		}
	}
}

func TestStripPrivateBlocks(t *testing.T) {
	tests := []struct {
		name     string
		markdown string
		want     string
	}{
		{
			name:     "empty markdown",
			markdown: "",
			want:     "",
		},
		{
			name:     "plain markdown without fences",
			markdown: "# Title\n\nThis is plain text.\n",
			want:     "# Title\n\nThis is plain text.\n",
		},
		{
			name:     "simple parchment private block with backticks",
			markdown: "before\n```parchment-secret\nhidden content\n```\nafter\n",
			want:     "before\nafter\n",
		},
		{
			name:     "simple parchment private block with tildes",
			markdown: "before\n~~~parchment-secret\nhidden content\n~~~\nafter\n",
			want:     "before\nafter\n",
		},
		{
			name:     "ordinary code fence preserved",
			markdown: "before\n```go\nfmt.Println(\"hello\")\n```\nafter\n",
			want:     "before\n```go\nfmt.Println(\"hello\")\n```\nafter\n",
		},
		{
			name:     "longer opening fence requires matching closing fence",
			markdown: "````parchment-secret\n```\nstill hidden\n````\nafter\n",
			want:     "after\n",
		},
		{
			name:     "shorter closing fence does not close longer private fence",
			markdown: "````parchment-secret\ncontent\n```\nstill inside\n````\nafter\n",
			want:     "after\n",
		},
		{
			name:     "closing fence with trailing spaces",
			markdown: "```parchment-secret\nhidden\n```   \nafter\n",
			want:     "after\n",
		},
		{
			name:     "private block with info string arguments",
			markdown: "```parchment-data json extra\n{\"key\": \"val\"}\n```\nafter\n",
			want:     "after\n",
		},
		{
			name:     "CRLF line endings in private block",
			markdown: "before\r\n```parchment-secret\r\nhidden\r\n```\r\nafter\r\n",
			want:     "before\r\nafter\r\n",
		},
		{
			name:     "indented private block (1 to 3 spaces)",
			markdown: "  ```parchment-secret\n  hidden\n  ```\nafter\n",
			want:     "after\n",
		},
		{
			name:     "indented code block (4 spaces) is not a fence",
			markdown: "    ```parchment-secret\n    visible indented code\n",
			want:     "    ```parchment-secret\n    visible indented code\n",
		},
		{
			name:     "quoted private block in blockquote",
			markdown: "> ```parchment-secret\n> hidden\n> ```\nafter\n",
			want:     "after\n",
		},
		{
			name:     "nested blockquote private block",
			markdown: "> > ```parchment-secret\n> > hidden\n> > ```\nafter\n",
			want:     "after\n",
		},
		{
			name:     "quoted ordinary fence containing private fence syntax",
			markdown: "> ```go\n> ```parchment-example\n> visible\n> ```\n> ```\n",
			want:     "> ```go\n> ```parchment-example\n> visible\n> ```\n> ```\n",
		},
		{
			name:     "unordered list private block",
			markdown: "- ```parchment-secret\n  hidden list item\n  ```\nafter\n",
			want:     "after\n",
		},
		{
			name:     "ordered list private block",
			markdown: "1. ```parchment-secret\n   hidden item\n   ```\nafter\n",
			want:     "after\n",
		},
		{
			name:     "deep list continuation private block",
			markdown: "10. Item\n\n    ```parchment-deep-list\n    deeply-hidden\n    ```\nafter\n",
			want:     "10. Item\n\nafter\n",
		},
		{
			name:     "ordinary code fence with inner parchment-like text",
			markdown: "```go\n// parchment-secret\ncode line\n```\n",
			want:     "```go\n// parchment-secret\ncode line\n```\n",
		},
		{
			name:     "unterminated private block at EOF",
			markdown: "before\n```parchment-secret\nhidden at eof",
			want:     "before\n",
		},
		{
			name:     "tilde private fence not closed by backticks",
			markdown: "~~~parchment-secret\n```\nstill hidden\n~~~\nafter\n",
			want:     "after\n",
		},
		{
			name:     "backtick fence with invalid backtick in info string is ignored as fence",
			markdown: "```parchment`invalid\ncontent\n```\n",
			want:     "```parchment`invalid\ncontent\n```\n",
		},
		{
			name:     "multiple consecutive private blocks",
			markdown: "```parchment-one\none\n```\n\n```parchment-two\ntwo\n```\nafter\n",
			want:     "\nafter\n",
		},
		{
			name:     "private block exiting blockquote container prematurely",
			markdown: "> ```parchment-quote\n> quoted-hidden\n\noutside-quote\n",
			want:     "\noutside-quote\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripPrivateBlocks(tt.markdown)
			if got != tt.want {
				t.Errorf("StripPrivateBlocks(%q) =\n%q\nwant:\n%q", tt.markdown, got, tt.want)
			}
		})
	}
}

func TestStripPrivateBlocksPreservesOrdinaryCodeFences(t *testing.T) {
	markdown := "before\n\n```parchment-secret\nhidden\n```\n\n```go\n```parchment-example\nvisible\n```\n```\nafter\n"
	got := StripPrivateBlocks(markdown)
	if strings.Contains(got, "parchment-secret") || strings.Contains(got, "hidden") {
		t.Fatalf("Parchment block was rendered: %q", got)
	}
	if !strings.Contains(got, "```go\n```parchment-example\nvisible\n```\n```") {
		t.Fatalf("ordinary code fence content was removed: %q", got)
	}
}

func TestStripPrivateBlocksRecognizesQuotedFences(t *testing.T) {
	markdown := "> ```parchment-secret\n> hidden\n> ```\n\n" +
		"> > ~~~parchment-deep\n> > hidden deep\n> > ~~~\n\n" +
		"> ```go\n> ```parchment-example\n> visible\n> ```\n> ```\n"
	got := StripPrivateBlocks(markdown)
	if strings.Contains(got, "parchment-secret") || strings.Contains(got, "hidden") {
		t.Fatalf("quoted Parchment block was rendered: %q", got)
	}
	if !strings.Contains(got, "> ```go\n> ```parchment-example\n> visible\n> ```\n> ```") {
		t.Fatalf("ordinary quoted code fence content was removed: %q", got)
	}
}

func TestStripPrivateBlocksTracksFenceContainers(t *testing.T) {
	markdown := "~~~parchment-secret\nsecret-before\n> ~~~\nsecret-after\n~~~\n\n" +
		"~~~go\n> ~~~\n```parchment-example\nvisible\n~~~\n\n" +
		"- ```parchment-list-secret\n  list-hidden\n  ```\n" +
		"- ```go\n  ```parchment-list-example\n  list-visible\n  ```\n"
	got := StripPrivateBlocks(markdown)
	for _, hidden := range []string{"parchment-secret", "secret-before", "secret-after",
		"parchment-list-secret", "list-hidden"} {
		if strings.Contains(got, hidden) {
			t.Fatalf("reserved block content %q was rendered: %q", hidden, got)
		}
	}
	for _, visible := range []string{"~~~go\n> ~~~\n```parchment-example\nvisible\n~~~",
		"- ```go\n  ```parchment-list-example\n  list-visible\n  ```"} {
		if !strings.Contains(got, visible) {
			t.Fatalf("ordinary fenced content was removed: %q", got)
		}
	}
}

func TestStripPrivateBlocksResetsAtContainerExit(t *testing.T) {
	markdown := "> ```parchment-quote\n> quoted-hidden\n\noutside-quote\n\n" +
		"- ```parchment-list\n  listed-hidden\n\noutside-list\n\n" +
		"    ```parchment-not-a-fence\n    visible-indented-code\n"
	got := StripPrivateBlocks(markdown)
	for _, hidden := range []string{"parchment-quote", "quoted-hidden", "parchment-list", "listed-hidden"} {
		if strings.Contains(got, hidden) {
			t.Fatalf("unterminated container fence leaked %q: %q", hidden, got)
		}
	}
	for _, visible := range []string{"outside-quote", "outside-list", "```parchment-not-a-fence", "visible-indented-code"} {
		if !strings.Contains(got, visible) {
			t.Fatalf("content outside a fence was lost (%q): %q", visible, got)
		}
	}
}

func TestStripPrivateBlocksRecognizesFencesOnListContinuations(t *testing.T) {
	markdown := "10. Item\n\n    ```parchment-deep-list\n    deeply-hidden\n    ```\n\n" +
		"    ```go\n    ```parchment-list-example\n    list-visible\n    ```\n"
	got := StripPrivateBlocks(markdown)
	if strings.Contains(got, "parchment-deep-list") || strings.Contains(got, "deeply-hidden") {
		t.Fatalf("deep list-contained reserved fence was rendered: %q", got)
	}
	if !strings.Contains(got, "    ```go\n    ```parchment-list-example\n    list-visible\n    ```") {
		t.Fatalf("ordinary list-contained code fence was removed: %q", got)
	}
}

func TestStripPrivateBlocksRecognizesNestedListFences(t *testing.T) {
	markdown := "- Parent\n    - Child\n      ```parchment-nested-secret\n      nested-hidden\n      ```\n" +
		"    - Ordinary\n      ```go\n      ```parchment-nested-example\n      nested-visible\n      ```\n"
	got := StripPrivateBlocks(markdown)
	for _, hidden := range []string{"parchment-nested-secret", "nested-hidden"} {
		if strings.Contains(got, hidden) {
			t.Fatalf("nested reserved block leaked %q: %q", hidden, got)
		}
	}
	for _, visible := range []string{"- Parent", "    - Ordinary", "```go", "parchment-nested-example", "nested-visible"} {
		if !strings.Contains(got, visible) {
			t.Fatalf("ordinary nested code block lost %q: %q", visible, got)
		}
	}
}

func TestStripPrivateBlocksPreservesListFenceStateAcrossBlankLines(t *testing.T) {
	markdown := "- ````go\n  code before\n\n  ```parchment-example\n  visible example\n  ```\n  ````\n"
	got := StripPrivateBlocks(markdown)
	for _, want := range []string{"````go", "```parchment-example", "visible example", "````"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ordinary list code fence lost %q: %q", want, got)
		}
	}
}

func TestStripPrivateBlocksResetsAtListContinuationExit(t *testing.T) {
	markdown := "- Item\n\n  ```parchment-secret\n  hidden continuation\nOutside the list\n"
	got := StripPrivateBlocks(markdown)
	if strings.Contains(got, "parchment-secret") || strings.Contains(got, "hidden continuation") {
		t.Fatalf("list-continuation reserved fence was rendered: %q", got)
	}
	if !strings.Contains(got, "Outside the list") {
		t.Fatalf("content after the list was hidden: %q", got)
	}
}

func TestStripPrivateBlocksRecognizesNestedListBlockquoteFences(t *testing.T) {
	markdown := "- > ```parchment-secret\n  > nested-hidden\n  > ```\n\n" +
		"- > ```go\n  > ```parchment-example\n  > nested-visible\n  > ```\n"
	got := StripPrivateBlocks(markdown)
	for _, hidden := range []string{"parchment-secret", "nested-hidden"} {
		if strings.Contains(got, hidden) {
			t.Fatalf("nested reserved block leaked %q: %q", hidden, got)
		}
	}
	for _, visible := range []string{"- > ```go", "> ```parchment-example", "> nested-visible", "> ```"} {
		if !strings.Contains(got, visible) {
			t.Fatalf("ordinary nested code block lost %q: %q", visible, got)
		}
	}
}

func TestDecodeRequiresLeadingMetadataAndValidPrivateBlock(t *testing.T) {
	for _, source := range []string{
		"# Missing metadata\n",
		"```parchment-meta\n{}\n```\n\n```parchment-data\nnot JSON\n```\n",
	} {
		if _, err := Decode([]byte(source)); err == nil {
			t.Fatalf("accepted invalid artifact file: %q", source)
		}
	}
}
