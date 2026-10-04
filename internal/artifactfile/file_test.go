package artifactfile

import (
	"encoding/json"
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
	if file.Body != body {
		t.Fatalf("body = %q, want %q", file.Body, body)
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
