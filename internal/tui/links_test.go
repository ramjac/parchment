package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
)

func TestExtractAndRewriteLinks(t *testing.T) {
	input := []string{
		"Here is a [link to intro](#intro) and [another](#section-2).",
		"```",
		"[code link](#not-a-link)",
		"```",
		"External [Google](https://google.com) link stays.",
	}

	rewritten, links := extractAndRewriteLinks(input)
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d: %+v", len(links), links)
	}

	if links[0].label != "link to intro" || links[0].target != "intro" {
		t.Errorf("link 0 mismatch: %+v", links[0])
	}
	if links[1].label != "another" || links[1].target != "section-2" {
		t.Errorf("link 1 mismatch: %+v", links[1])
	}

	if !strings.Contains(rewritten[0], "Here is a link to intro and another.") {
		t.Errorf("rewritten line 0 wrong: %q", rewritten[0])
	}
	if !strings.Contains(rewritten[2], "[code link](#not-a-link)") {
		t.Errorf("fenced code block link was rewritten: %q", rewritten[2])
	}
	if !strings.Contains(rewritten[4], "[Google](https://google.com)") {
		t.Errorf("external link was modified: %q", rewritten[4])
	}
}

func TestDocumentReaderClickableInternalLinks(t *testing.T) {
	h := newHarness(t, "")
	brk := "\n\n" + document.PageBreakMarkup + "\n\n"
	body := "# Page One\n\nClick [Jump to Section Two](#section-two) for details.\n\nAlso [Unresolved](#non-existent) link." +
		brk +
		"# Section Two\n\nWelcome to section two."

	if _, err := h.docs.Create(context.Background(), h.path, document.Draft{Body: body}); err != nil {
		t.Fatal(err)
	}

	h.open(artifact.DocumentKind)
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})

	s := h.m.documents
	if !s.reading {
		t.Fatal("expected document to open in reader")
	}
	if s.readerPage != 0 {
		t.Fatalf("expected initial readerPage 0, got %d", s.readerPage)
	}

	view := h.m.View()
	clean := ansi.Strip(view)

	// Glamour must not print raw (#section-two) URL
	if strings.Contains(clean, "(#section-two)") {
		t.Fatalf("raw target URL leaked into reader view:\n%s", clean)
	}
	if !strings.Contains(clean, "Jump to Section Two") {
		t.Fatalf("link label missing from reader view:\n%s", clean)
	}

	// Verify hit box was registered
	if len(s.readerLinks) == 0 {
		t.Fatal("no readerLinks registered")
	}

	var targetBox *linkHitBox
	for _, box := range s.readerLinks {
		if box.page == 0 && box.target == "section-two" {
			targetBox = &box
			break
		}
	}
	if targetBox == nil {
		t.Fatalf("linkHitBox for section-two not found: %+v", s.readerLinks)
	}

	// Calculate terminal click position
	outlineWidth, _ := s.readerPaneWidths()
	clickX := (outlineWidth + 2) + targetBox.startCol + 1
	clickY := targetBox.row + 4 - s.reader.YOffset

	// Click on link
	h.send(tea.MouseMsg{
		X:      clickX,
		Y:      clickY,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})

	if s.readerPage != 1 {
		t.Fatalf("expected readerPage to jump to 1, got %d", s.readerPage)
	}
}

func TestDocumentPreviewClickableInternalLinks(t *testing.T) {
	h := newHarness(t, "")
	brk := "\n\n" + document.PageBreakMarkup + "\n\n"
	body := "# Page One\n\nSee [Go to Second Page](#second-page) for more." +
		brk +
		"# Second Page\n\nHere is page two."

	if _, err := h.docs.Create(context.Background(), h.path, document.Draft{Body: body}); err != nil {
		t.Fatal(err)
	}

	h.open(artifact.DocumentKind)
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})

	// Switch from reader to editor, then press F5 for preview
	h.key("e")
	s := h.m.documents
	if s.reading {
		t.Fatal("expected editor mode")
	}

	h.key("f5")
	if !s.previewing {
		t.Fatal("expected preview mode")
	}
	if s.editPage != 0 {
		t.Fatalf("expected editPage 0, got %d", s.editPage)
	}

	view := h.m.View()
	clean := ansi.Strip(view)
	if strings.Contains(clean, "(#second-page)") {
		t.Fatalf("raw target URL leaked into preview view:\n%s", clean)
	}

	var targetBox *linkHitBox
	for _, box := range s.editLinks {
		if box.page == 0 && box.target == "second-page" {
			targetBox = &box
			break
		}
	}
	if targetBox == nil {
		t.Fatalf("linkHitBox for second-page not found: %+v", s.editLinks)
	}

	clickX := targetBox.startCol + 1
	clickY := targetBox.row + 2 - s.editPageLine

	h.send(tea.MouseMsg{
		X:      clickX,
		Y:      clickY,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})

	if s.editPage != 1 {
		t.Fatalf("expected preview to jump to page 1, got %d", s.editPage)
	}
}

func TestUnresolvedAndExternalLinksDoNotNavigate(t *testing.T) {
	h := newHarness(t, "")
	body := "# Intro\n\n[Unresolved](#does-not-exist)\n\n[External](https://example.com)"
	if _, err := h.docs.Create(context.Background(), h.path, document.Draft{Body: body}); err != nil {
		t.Fatal(err)
	}

	h.open(artifact.DocumentKind)
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})

	s := h.m.documents
	initialPage := s.readerPage

	// Click unresolved link
	var brokenBox *linkHitBox
	for _, box := range s.readerLinks {
		if box.target == "does-not-exist" {
			brokenBox = &box
			break
		}
	}
	if brokenBox == nil {
		t.Fatal("expected brokenBox to be registered")
	}

	outlineWidth, _ := s.readerPaneWidths()
	clickX := (outlineWidth + 2) + brokenBox.startCol + 1
	clickY := brokenBox.row + 4 - s.reader.YOffset

	h.send(tea.MouseMsg{
		X:      clickX,
		Y:      clickY,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})

	if s.readerPage != initialPage {
		t.Fatalf("unresolved link caused navigation to page %d", s.readerPage)
	}
}
