package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotePrintCLI(t *testing.T) {
	c := newCLI(t)
	body := "# Plain note\n\nFirst paragraph.\n\n<!-- parchment:page-break -->\n\nSecond paragraph.\n\n```parchment-secret\nhidden\n```\n"
	if err := os.WriteFile("plain.md", []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	text := c.must("note", "print", "plain.md")
	if !strings.Contains(text, "First paragraph") || !strings.Contains(text, "Second paragraph") {
		t.Fatalf("printed note missing content: %q", text)
	}
	if strings.Contains(text, "\f") || strings.Contains(text, "hidden") {
		t.Fatalf("note treated a document page break as layout or exposed a private block: %q", text)
	}
	pdf := c.must("note", "print", "plain.md", "--format", "pdf")
	if !strings.HasPrefix(pdf, "%PDF-") || !strings.Contains(pdf, "/Count 1") {
		t.Fatal("note PDF is invalid or interprets document-only page breaks")
	}
	c.must("note", "print", "plain.md", "--format", "pdf", "-o", "note.pdf")
	if _, err := c.run("note", "print", "plain.md", "--format", "pdf", "-o", "note.pdf"); err == nil {
		t.Fatal("non-interactive print overwrote a PDF without --force")
	}
	c.must("note", "print", "plain.md", "--format", "pdf", "-o", "note.pdf", "--force")
	if _, err := c.run("note", "print", "plain.md", "--format", "pdf", "-o", "plain.md", "--force"); err == nil {
		t.Fatal("note print overwrote its source")
	}
	if _, err := c.run("note", "print", "plain.md", "-o", "plain.md"); err == nil {
		t.Fatal("text print overwrote its source")
	}
	saved, err := os.ReadFile("plain.md")
	if err != nil || !bytes.Equal(saved, []byte(body)) {
		t.Fatalf("plain Markdown changed: %q, %v", saved, err)
	}
	if _, err := c.run("note", "print", "plain.md", "--format", "unknown"); err == nil {
		t.Fatal("invalid print format accepted")
	}
}

func TestPrintTypedNoteAndRejectDocument(t *testing.T) {
	c := newCLI(t)
	c.must("note", "create", "note.md", "--body", "A typed note")
	text := c.must("note", "print", "note.md")
	if !strings.Contains(text, "A typed note") {
		t.Fatalf("typed note output = %q", text)
	}

	c.must("document", "create", "document.md", "--body", "Not a note")
	if _, err := c.run("note", "print", filepath.Join(c.dir, "document.md")); err == nil {
		t.Fatal("note print accepted a document")
	}
}

func TestNotePrintPaginatesLongMarkdown(t *testing.T) {
	c := newCLI(t)
	body := strings.Repeat("A paragraph with enough text to fill a printed page.\n\n", 120)
	if err := os.WriteFile("long.md", []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	text := c.must("note", "print", "long.md")
	if !strings.Contains(text, "\f") {
		t.Fatal("long note did not produce multiple text pages")
	}
	pdf := c.must("note", "print", "long.md", "--format", "pdf")
	if !strings.HasPrefix(pdf, "%PDF-") || strings.Contains(pdf, "/Count 1\n") {
		t.Fatal("long note did not produce a multipage PDF")
	}
}
