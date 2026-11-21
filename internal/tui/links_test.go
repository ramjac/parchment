package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/config"
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
	previousProfile := compat.Profile
	compat.Profile = colorprofile.TrueColor
	t.Cleanup(func() { compat.Profile = previousProfile })

	h := newHarness(t, "")
	brk := "\n\n" + document.PageBreakMarkup + "\n\n"
	body := "# Page One\n\nClick [Jump to Section Two](#section-two) for details.\n\nAlso [Unresolved](#non-existent) link.\n\n" + strings.Repeat("Filler.\n\n", 20) +
		brk +
		"# Section Two\n\nWelcome to section two.\n\n" + strings.Repeat("More.\n\n", 20)

	if _, err := h.docs.Create(context.Background(), h.path, document.Draft{Body: body}); err != nil {
		t.Fatal(err)
	}

	h.openWithTheme(artifact.DocumentKind, false, config.Theme{Mode: "dark"})
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})

	s := h.m.documents
	if !s.reading {
		t.Fatal("expected document to open in reader")
	}
	if s.reader.YOffset() != 0 {
		t.Fatalf("expected initial offset 0, got %d", s.reader.YOffset())
	}

	view := h.m.View().Content
	clean := ansi.Strip(view)

	// Glamour must not print raw (#section-two) URL
	if strings.Contains(clean, "(#section-two)") {
		t.Fatalf("raw target URL leaked into reader view:\n%s", clean)
	}
	if !strings.Contains(clean, "Jump to Section Two") {
		t.Fatalf("link label missing from reader view:\n%s", clean)
	}
	if !strings.Contains(view, s.theme.Link.Render("Jump to Section Two")) {
		t.Fatalf("internal link does not use the shared link color:\n%q", view)
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
	clickY := targetBox.row + 3 - s.reader.YOffset()

	// Click on link
	h.send(mouseEvent{
		X:      clickX,
		Y:      clickY,
		Action: mousePress,
		Button: mouseLeft,
	})

	if s.reader.YOffset() != s.readerRows[1] {
		t.Fatalf("expected jump to section row %d, got %d", s.readerRows[1], s.reader.YOffset())
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

	view := h.m.View().Content
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

	h.send(mouseEvent{
		X:      clickX,
		Y:      clickY,
		Action: mousePress,
		Button: mouseLeft,
	})

	if s.editPage != 1 {
		t.Fatalf("expected preview to jump to page 1, got %d", s.editPage)
	}
}

func TestReaderScrolledLinkAndResize(t *testing.T) {
	h := newHarness(t, "")
	body := "# Start\n\n" + strings.Repeat("Long text that wraps when the reader becomes narrow.\n\n", 20) +
		"界 prefix [Jump](#target)\n\n" + strings.Repeat("More content.\n\n", 15) +
		"# Target\n\n" + strings.Repeat("After target.\n\n", 20)
	if _, err := h.docs.Create(context.Background(), h.path, document.Draft{Body: body}); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.DocumentKind)
	for _, width := range []int{100, 60} {
		h.send(tea.WindowSizeMsg{Width: width, Height: 20})
		s := h.m.documents
		if len(s.readerLinks) != 1 {
			t.Fatalf("width %d: link boxes = %+v", width, s.readerLinks)
		}
		box := s.readerLinks[0]
		s.reader.SetYOffset(box.row)
		view := strings.Split(ansi.Strip(h.m.View().Content), "\n")
		x, y := -1, -1
		for i, line := range view {
			if idx := strings.Index(line, "Jump"); idx >= 0 {
				x, y = runewidth.StringWidth(line[:idx]), i
				break
			}
		}
		if x < 0 {
			t.Fatalf("width %d: link not visible", width)
		}
		h.send(mouseEvent{X: x, Y: y, Action: mousePress, Button: mouseLeft})
		if s.currentSection() != 1 {
			t.Fatalf("width %d: click at %d,%d did not navigate: offset=%d boxes=%+v", width, x, y, s.reader.YOffset(), s.readerLinks)
		}
		s.reader.GotoTop()
		h.send(mouseEvent{Action: mousePress, Button: mouseWheelDown})
		if s.reader.YOffset() == 0 {
			t.Fatalf("width %d: wheel did not scroll", width)
		}
		h.send(mouseEvent{Action: mousePress, Button: mouseWheelUp})
		if s.reader.YOffset() != 0 {
			t.Fatalf("width %d: wheel did not scroll up", width)
		}
		h.key("pgdown")
		if s.reader.YOffset() == 0 {
			t.Fatalf("width %d: Page Down did not scroll", width)
		}
		h.key("pgup")
		if s.reader.YOffset() != 0 {
			t.Fatalf("width %d: Page Up did not scroll", width)
		}
		h.key("space")
		if s.reader.YOffset() == 0 {
			t.Fatalf("width %d: Space did not scroll", width)
		}
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
	initialOffset := s.reader.YOffset()

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
	clickY := brokenBox.row + 3 - s.reader.YOffset()

	h.send(mouseEvent{
		X:      clickX,
		Y:      clickY,
		Action: mousePress,
		Button: mouseLeft,
	})

	if s.reader.YOffset() != initialOffset {
		t.Fatalf("unresolved link caused navigation to row %d", s.reader.YOffset())
	}
}
