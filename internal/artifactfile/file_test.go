package artifactfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"example.com/parchment/internal/artifact"
)

func testArtifact() artifact.Artifact {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	return artifact.Artifact{
		ID: "0123456789abcdef0123456789abcdef", Kind: artifact.NoteKind, Title: "Example",
		CreatedAt: now, ModifiedAt: now, FormatVersion: artifact.FormatVersion,
		Location: ".parchment/artifacts/0123456789abcdef0123456789abcdef/content.md",
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
	if _, err := ReadMetadata([]byte("```parchment-meta\nnot JSON\n```\n")); err == nil ||
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
	if err != nil || metadata.ID != testArtifact().ID {
		t.Fatalf("metadata after leading blank lines = %+v, %v", metadata, err)
	}
	file, err := Decode(padded)
	if err != nil || file.Body != "body" {
		t.Fatalf("decoded artifact after leading blank lines = %+v, %v", file, err)
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

func TestDecodeAcceptsLegacyEnvelopeWithoutBodyBoundary(t *testing.T) {
	body := "# Legacy artifact\n\nMarkdown body.\n"
	data, err := Encode(testArtifact(), body, nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.Replace(string(data), bodyBoundary+"\n", "", 1)
	file, err := Decode([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if file.Body != "\n"+body {
		t.Fatalf("body = %q, want %q", file.Body, "\n"+body)
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

	var legacy bytes.Buffer
	metadata, err := json.Marshal(testArtifact())
	if err != nil {
		t.Fatal(err)
	}
	writeBlock(&legacy, metadataBlock, metadata)
	legacy.WriteString("\n\n  Legacy body\n")
	file, err = Decode(legacy.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if file.Body != "\n\n  Legacy body\n" {
		t.Fatalf("legacy body whitespace = %q", file.Body)
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
	if got.ID != testArtifact().ID {
		t.Fatalf("metadata ID = %q", got.ID)
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
	metadata.Title = strings.Repeat("x", 10_000)
	data, err := Encode(metadata, "body", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadMetadataFrom(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != metadata.Title {
		t.Fatal("metadata line was not preserved")
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
	if got.ID != testArtifact().ID {
		t.Fatalf("metadata ID = %q", got.ID)
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
