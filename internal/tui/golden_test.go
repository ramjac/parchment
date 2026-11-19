package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/config"
	"example.com/parchment/internal/document"
	tuitheme "example.com/parchment/internal/tui/theme"
)

func TestTUIRenderGoldens(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
	})

	darkTheme := config.Theme{Mode: "dark"}

	t.Run("note-editor", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		saved, err := h.notes.Get(context.Background(), h.path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.notes.UpdateExpected(context.Background(), saved,
			"# A Short Note\n\nText with **emphasis** and `code`.\n"); err != nil {
			t.Fatal(err)
		}
		h.openWithTheme(artifact.NoteKind, false, darkTheme)
		prepareGoldenView(h, 80, 24)
		assertTUIViewGolden(t, "note-editor", h.m.View(), 80, 24)
	})

	t.Run("note-preview", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		saved, err := h.notes.Get(context.Background(), h.path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.notes.UpdateExpected(context.Background(), saved,
			"# A Short Note\n\nText with **emphasis** and `code`.\n\n[Reference](https://example.com)\n\n![Diagram](diagram.png)\n\n- A useful item\n"); err != nil {
			t.Fatal(err)
		}
		h.openWithTheme(artifact.NoteKind, false, darkTheme)
		h.key("f5")
		prepareGoldenView(h, 80, 24)
		assertTUIViewGolden(t, "note-preview", h.m.View(), 80, 24)
	})

	t.Run("document-reader", func(t *testing.T) {
		h := newHarness(t, "")
		body := "# Getting Started\n\nA paragraph with **emphasis**, a [link](https://example.com), and `code`.\n\n> A quoted thought.\n\n## Next Steps\n\n- One\n- Two\n\n```go\nfmt.Println(\"ordinary code\")\n```\n\n```parchment-example\nhidden payload\n```\n"
		if _, err := h.docs.Create(context.Background(), h.path, document.Draft{Body: body}); err != nil {
			t.Fatal(err)
		}
		h.openWithTheme(artifact.DocumentKind, false, darkTheme)
		assertRenderedDocumentMarkdown(t, h.m.documents.readerPages)
		prepareGoldenView(h, 120, 40)
		assertTUIViewGolden(t, "document-reader", h.m.View(), 120, 40)
	})

	t.Run("spreadsheet-grid", func(t *testing.T) {
		h := newHarness(t, artifact.SpreadsheetKind)
		prepareGoldenView(h, 80, 24)
		assertTUIViewGolden(t, "spreadsheet-grid", h.m.View(), 80, 24)
	})

	t.Run("presentation", func(t *testing.T) {
		h := newHarness(t, artifact.PresentationKind)
		item, err := h.decks.Get(context.Background(), h.path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.decks.Update(context.Background(), item,
			"# A Short Deck\n\n## First Slide\n\nA paragraph with **emphasis** and `code`.\n"); err != nil {
			t.Fatal(err)
		}
		h.openWithTheme(artifact.PresentationKind, false, darkTheme)
		h.m.deck.goToPage(1)
		prepareGoldenView(h, 120, 40)
		assertTUIViewGolden(t, "presentation", h.m.View(), 120, 40)
	})

	t.Run("small-terminal", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		prepareGoldenView(h, 80, 9)
		view := h.m.View()
		if !strings.Contains(view, "terminal too small") {
			t.Fatalf("small terminal did not show the size warning:\n%s", view)
		}
		assertTUIViewGolden(t, "small-terminal", view, 0, 0)
	})

	t.Run("note-discard-confirmation", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		h.typeText("unsaved content")
		h.key("esc")
		prepareGoldenView(h, 80, 24)
		assertTUIViewGolden(t, "note-discard-confirmation", h.m.View(), 0, 0)
	})

	t.Run("open-error", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		h.m.stage = stageFailed
		prepareGoldenView(h, 80, 24)
		assertTUIViewGolden(t, "open-error", h.m.View(), 0, 0)
	})
}

func TestMarkdownContentColors(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.ANSI256, termenv.ANSI} {
		lipgloss.SetColorProfile(profile)
		for _, mode := range []string{"light", "dark"} {
			styles := tuitheme.Resolve(config.Theme{Mode: mode})
			for _, tt := range []struct {
				name, source, text string
				color              *string
			}{
				{"heading", "# Title", "Title", styles.Markdown.H1.Color},
				{"link", "[Reference](https://example.com)", "Reference", styles.Markdown.LinkText.Color},
				{"image", "![Diagram](diagram.png)", "Diagram", styles.Markdown.ImageText.Color},
				{"inline code", "`value`", "value", styles.Markdown.Code.Color},
				{"bullet", "- Item", "Item", styles.Markdown.Item.Color},
				{"number", "1. Item", "Item", styles.Markdown.Enumeration.Color},
			} {
				if tt.color == nil {
					t.Fatalf("%s has no semantic color", tt.name)
				}
				rendered := renderMarkdown(tt.source, 60, styles)
				sample := lipgloss.NewStyle().Foreground(lipgloss.Color(*tt.color)).Render("sample")
				colorSequence, _, _ := strings.Cut(sample, "sample")
				colorParams := strings.TrimSuffix(strings.TrimPrefix(colorSequence, "\x1b["), "m")
				if colorParams == "" || !strings.Contains(rendered, colorParams) ||
					!strings.Contains(ansi.Strip(rendered), tt.text) {
					t.Errorf("%s, mode %s, profile %v: expected color %s and text %q in %q",
						tt.name, mode, profile, colorParams, tt.text, rendered)
				}
			}
		}
	}
}

func assertRenderedDocumentMarkdown(t *testing.T, pages []document.Page) {
	t.Helper()
	var rendered strings.Builder
	for _, page := range pages {
		for _, line := range page.Lines {
			rendered.WriteString(ansi.Strip(line))
			rendered.WriteByte('\n')
		}
	}
	text := rendered.String()
	for _, want := range []string{"◆ Getting Started", "emphasis", "A quoted thought.", "▌ Next Steps", "One", "fmt.Println(\"ordinary code\")"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered document is missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"## Next Steps", "**emphasis**", "> A quoted thought.", "```", "hidden payload"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("rendered document contains source markup %q:\n%s", unwanted, text)
		}
	}
}

func TestMarkdownHeadingsRemainDistinctWithoutColor(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
	})

	rendered := renderMarkdown("# Title\n\n## Section\n\nBody text.", 40, tuitheme.Resolve(config.Theme{Mode: "dark"}))
	plain := ansi.Strip(rendered)
	if strings.Contains(rendered, "\x1b[38;") || strings.Contains(rendered, "\x1b[48;") ||
		!strings.Contains(plain, "◆ Title") ||
		!strings.Contains(plain, "▌ Section") {
		t.Fatalf("monochrome Markdown rendering lost hierarchy or emitted color: rendered=%q plain=%q", rendered, plain)
	}
}

func prepareGoldenView(h *harness, width, height int) {
	h.t.Helper()
	h.m.path = "fixtures/example.md"
	h.m.status, h.m.errMessage = "", ""
	if h.m.documents != nil {
		h.m.documents.path = h.m.path
		h.m.documents.status, h.m.documents.errMessage = "", ""
	}
	if h.m.sheet != nil {
		h.m.sheet.path = h.m.path
		h.m.sheet.status, h.m.sheet.errMessage, h.m.sheet.loadedStatus = "", "", ""
	}
	if h.m.deck != nil {
		h.m.deck.path = h.m.path
		h.m.deck.status, h.m.deck.errMessage, h.m.deck.loadedStatus = "", "", ""
	}
	h.send(tea.WindowSizeMsg{Width: width, Height: height})
}

func assertTUIViewGolden(t *testing.T, name, got string, width, height int) {
	t.Helper()
	gotWidth := tuiScreenWidth(got)
	if width > 0 && gotWidth != width {
		t.Fatalf("rendered width = %d, want %d", gotWidth, width)
	}
	gotHeight := strings.Count(ansi.Strip(got), "\n") + 1
	if height > 0 && gotHeight != height {
		t.Fatalf("rendered height = %d, want %d", gotHeight, height)
	}
	if width > 0 {
		plain := ansi.Strip(got)
		if !strings.Contains(plain, "┌") || !strings.Contains(plain, "└") {
			t.Fatal("bordered view is missing fixed top or bottom chrome")
		}
		if !strings.Contains(got, "\x1b[") {
			t.Fatal("forced truecolor rendering produced no ANSI styles")
		}
	}

	path := filepath.Join("testdata", name+".golden")
	if os.Getenv("UPDATE_TUI_GOLDENS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (regenerate explicitly with UPDATE_TUI_GOLDENS=1)", path, err)
	}
	if !bytes.Equal(want, []byte(got)) {
		t.Fatalf("render differs from %s: %s (regenerate explicitly with UPDATE_TUI_GOLDENS=1)",
			path, firstGoldenDifference(want, []byte(got)))
	}
}

func firstGoldenDifference(want, got []byte) string {
	wantLines, gotLines := strings.Split(string(want), "\n"), strings.Split(string(got), "\n")
	for i := 0; i < max(len(wantLines), len(gotLines)); i++ {
		var wantLine, gotLine string
		if i < len(wantLines) {
			wantLine = wantLines[i]
		}
		if i < len(gotLines) {
			gotLine = gotLines[i]
		}
		if wantLine == gotLine {
			continue
		}
		wantText, gotText := ansi.Strip(wantLine), ansi.Strip(gotLine)
		if wantText == gotText {
			return fmt.Sprintf("ANSI styles differ on row %d (want %d bytes, got %d)", i+1, len(wantLine), len(gotLine))
		}
		return fmt.Sprintf("row %d differs: want %q; got %q", i+1, wantText, gotText)
	}
	return "contents differ"
}

func tuiScreenWidth(view string) int {
	width := 0
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		width = max(width, runewidth.StringWidth(line))
	}
	return width
}
