package printing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/presentation"
)

func TestPrintPresentationPagesAndNotes(t *testing.T) {
	item := presentation.Presentation{Source: "# Deck\n\nBy Presenter\n\n## First\n\nSlide content.\n\n: First note.\n: Second note.\n\n## Second\n\nAnother slide.\n"}
	printed, err := PrintPresentation(item)
	if err != nil {
		t.Fatal(err)
	}
	pages := strings.Split(printed, "\f")
	if len(pages) != 3 {
		t.Fatalf("got %d pages, want title page and two slides", len(pages))
	}
	if !strings.Contains(pages[0], "DECK") || !strings.Contains(pages[0], "By Presenter") {
		t.Fatalf("title page = %q", pages[0])
	}
	if !strings.Contains(pages[1], "Slide content") || !strings.Contains(pages[1], "First note") || !strings.Contains(pages[1], "Second note") {
		t.Fatalf("first slide = %q", pages[1])
	}
	if strings.Contains(pages[0]+pages[2], "First note") {
		t.Fatal("speaker notes appeared on the wrong page")
	}
	layout := document.DefaultLayout()
	layout.Orientation, layout.PageNumbers = document.Landscape, document.NumbersNone
	geometry, err := layout.Geometry()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(pages[1], "\n")
	noteLine := -1
	for i, line := range lines {
		if strings.Contains(line, "Second note") {
			noteLine = i
		}
	}
	if noteLine < geometry.TopRows+geometry.BodyRows()-3 {
		t.Fatalf("notes line %d is not at the page bottom", noteLine)
	}
}

func TestPrintPresentationRejectsOversizedSlide(t *testing.T) {
	item := presentation.Presentation{Source: "# Deck\n\n## Long\n\n" + strings.Repeat("Long paragraph.\n\n", 200)}
	if _, err := PrintPresentation(item); err == nil || !strings.Contains(err.Error(), "fit on one text page") {
		t.Fatalf("oversized slide error = %v", err)
	}
}

func TestPresentationSlidesExpandCodeRelativeToSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "snippet.go"), []byte("package sample\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	item := presentation.Presentation{Artifact: artifact.Artifact{Path: filepath.Join(dir, "talk.md")}, Source: "# Deck\n\n## Code\n\n.code snippet.go\n"}
	slides, err := PresentationSlides(item)
	if err != nil || len(slides) != 2 || !strings.Contains(slides[1].Body, "package sample") {
		t.Fatalf("expanded slides = %+v, %v", slides, err)
	}
	item.Source = "# Deck\n\n## Code\n\n.code missing.go\n"
	if _, err := PresentationSlides(item); err == nil {
		t.Fatal("missing code snippet accepted")
	}
}

func TestPrintPresentationTwoUp(t *testing.T) {
	item := presentation.Presentation{Source: "# Deck\n\n## First\n\nContent\n\n: First reminder\n\n## Second\n\nClosing\n\n: Second reminder\n"}
	printed, err := PrintPresentationTwoUp(item)
	if err != nil {
		t.Fatal(err)
	}
	pages := strings.Split(printed, "\f")
	if len(pages) != 2 || !strings.Contains(pages[0], "DECK") || !strings.Contains(pages[0], "First reminder") ||
		!strings.Contains(pages[1], "Second reminder") {
		t.Fatalf("unexpected pages: %q", printed)
	}
	if strings.Count(pages[0], "+---") != 4 || strings.Count(pages[1], "+---") != 2 {
		t.Fatalf("wrong box count: %q", printed)
	}
	if strings.Index(pages[0], "First reminder") < strings.LastIndex(pages[0], "+---") {
		t.Fatal("first slide notes are not below its box")
	}
	item.Source = "# Deck\n\n## Slide\n\n" + strings.Repeat("Long paragraph.\n\n", 100)
	if _, err := PrintPresentationTwoUp(item); err == nil {
		t.Fatal("oversized slide was accepted")
	}
}
