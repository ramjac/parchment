package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/filerepo"
)

func TestPresentationPrintCLI(t *testing.T) {
	c := newCLI(t)
	source := "# Talk\n\n## First\n\nSlide content\n\n: Speaker reminder\n\n## Second\n\nClosing\n"
	if err := os.WriteFile("source.md", []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	c.must("presentation", "create", "talk.md", "--body-file", "source.md")
	original, err := os.ReadFile("talk.md")
	if err != nil {
		t.Fatal(err)
	}
	text := c.must("presentation", "print", "talk.md")
	pages := strings.Split(text, "\f")
	if len(pages) != 3 || !strings.Contains(pages[1], "Slide content") ||
		!strings.Contains(pages[1], "Speaker reminder") || !strings.Contains(pages[2], "Closing") {
		t.Fatalf("unexpected text pages: %q", text)
	}
	pdf := c.must("presentation", "print", "talk.md", "--format", "pdf")
	if !strings.HasPrefix(pdf, "%PDF-") || !strings.Contains(pdf, "/Count 3") {
		t.Fatal("presentation PDF is invalid or does not contain three pages")
	}
	c.must("presentation", "print", "talk.md", "--format", "pdf", "-o", "slides.pdf")
	repo, err := filerepo.New(filepath.Join(c.home, ".parchment"), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if path, err := repo.LastPDFPath(t.Context()); err != nil || path != filepath.Join(c.dir, "slides.pdf") {
		t.Fatalf("remembered path = %q, %v", path, err)
	}
	if _, err := c.run("presentation", "print", "talk.md", "--format", "pdf", "-o", "slides.pdf"); err == nil {
		t.Fatal("overwrote a PDF without confirmation or --force")
	}
	c.must("presentation", "print", "talk.md", "--format", "pdf", "-o", "slides.pdf", "--force")
	if _, err := c.run("presentation", "print", "talk.md", "--format", "pdf", "-o", "talk.md", "--force"); err == nil {
		t.Fatal("overwrote source presentation with PDF")
	}
	if _, err := c.run("presentation", "print", "talk.md", "-o", "talk.md"); err == nil {
		t.Fatal("overwrote source presentation with text")
	}
	if saved, err := os.ReadFile("talk.md"); err != nil || !bytes.Equal(saved, original) {
		t.Fatalf("source changed: %v", err)
	}
}

func TestPrintExamplePresentationPDF(t *testing.T) {
	example, err := filepath.Abs("../../examples/presentation.md")
	if err != nil {
		t.Fatal(err)
	}
	c := newCLI(t)
	if pdf := c.must("presentation", "print", example, "--format", "pdf"); !strings.HasPrefix(pdf, "%PDF-") {
		t.Fatal("example presentation did not produce a PDF")
	}
	if pdf := c.must("presentation", "print", example, "--format", "pdf", "--slides-per-page", "2"); !strings.HasPrefix(pdf, "%PDF-") {
		t.Fatal("example presentation did not produce a two-up PDF")
	}
	if text := c.must("presentation", "print", example, "--slides-per-page", "2"); !strings.Contains(text, "Speaker notes") {
		t.Fatal("example presentation two-up text is missing notes")
	}
}

func TestPresentationPrintTwoUpCLI(t *testing.T) {
	c := newCLI(t)
	source := "# Talk\n\n## First\n\nSlide content\n\n: Reminder\n\n## Second\n\nClosing\n"
	if err := os.WriteFile("source.md", []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	c.must("presentation", "create", "talk.md", "--body-file", "source.md")
	text := c.must("presentation", "print", "talk.md", "--slides-per-page", "2")
	if len(strings.Split(text, "\f")) != 2 || !strings.Contains(text, "Reminder") || !strings.Contains(text, "+---") {
		t.Fatalf("unexpected two-up text: %q", text)
	}
	pdf := c.must("presentation", "print", "talk.md", "--format", "pdf", "--slides-per-page", "2")
	if !strings.HasPrefix(pdf, "%PDF-") || !strings.Contains(pdf, "/Count 2") {
		t.Fatal("two-up PDF is invalid or does not contain two pages")
	}
	if _, err := c.run("presentation", "print", "talk.md", "--slides-per-page", "3"); err == nil {
		t.Fatal("invalid slide count accepted")
	}
}
