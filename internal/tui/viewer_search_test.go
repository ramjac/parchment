package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/config"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/spreadsheet"
)

func TestRenderedSearchUsesSmartCaseAndANSIColumns(t *testing.T) {
	line := "\x1b[31mwide界\x1b[0m alpha ALPHA"
	matches := renderedMatches([]string{line}, "alpha", 2)
	if len(matches) != 2 || matches[0].page != 2 || matches[0].startCol != 7 {
		t.Fatalf("case-insensitive rendered matches = %+v", matches)
	}
	if got := findTextMatches("alpha ALPHA", "ALPHA"); len(got) != 1 || got[0].startCol != 6 {
		t.Fatalf("smart-case matches = %+v", got)
	}
	highlighted := highlightLine(line, "alpha", Theme{})
	if strings.Count(highlighted, "\x1b[") < strings.Count(line, "\x1b[") ||
		!strings.Contains(highlighted, "alpha") || !strings.Contains(highlighted, "ALPHA") {
		t.Fatalf("highlighting lost ANSI styling or text: %q", highlighted)
	}
}

func TestNotePreviewSearchFindsAndNavigatesRenderedText(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)
	h.typeText("first phrase\nsecond phrase")
	h.key("f5")
	h.key("/")
	h.typeText("phrase")
	if !h.m.find.active || len(h.m.previewMatches) != 2 || h.m.previewMatch != 0 {
		t.Fatalf("note preview search: active=%t matches=%+v current=%d",
			h.m.find.active, h.m.previewMatches, h.m.previewMatch)
	}
	h.key("enter")
	h.key("n")
	if h.m.previewMatch != 0 || !h.m.find.visible || h.m.find.active {
		t.Fatalf("note preview next did not wrap while leaving search visible: current=%d visible=%t active=%t",
			h.m.previewMatch, h.m.find.visible, h.m.find.active)
	}
}

func TestDocumentReaderAndPrintPreviewSearch(t *testing.T) {
	h := newHarness(t, artifact.DocumentKind)
	snapshot, err := h.docs.Get(context.Background(), h.path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Layout = document.DefaultLayout()
	if _, err := h.docs.Save(context.Background(), snapshot, document.Draft{
		Body: "reader needle", Layout: document.DefaultLayout(),
	}); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.DocumentKind)
	reader := h.m.documents
	if !reader.reading {
		t.Fatal("document did not open in the reader")
	}
	h.key("/")
	h.typeText("needle")
	if len(reader.readerMatches) != 1 || reader.readerMatch != 0 {
		t.Fatalf("reader search = %+v, current=%d", reader.readerMatches, reader.readerMatch)
	}

	h.key("esc")
	h.key("e")
	h.key("f5")
	h.key("/")
	h.typeText("needle")
	if !reader.previewing || len(reader.previewMatches) != 1 || reader.previewMatch != 0 {
		t.Fatalf("print preview search: previewing=%t matches=%+v current=%d",
			reader.previewing, reader.previewMatches, reader.previewMatch)
	}
}

func TestSpreadsheetSearchIncludesOtherSheetsAndRawFormulas(t *testing.T) {
	m, service, _, path := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "home"}}})
	if _, err := service.AddSheet(context.Background(), path, "Other"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetCellInSheet(context.Background(), path, spreadsheet.SetCellOptions{
		Sheet: "Other", Row: 1, Column: 1, Cell: spreadsheet.Cell{Formula: "=1+2"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetCellInSheet(context.Background(), path, spreadsheet.SetCellOptions{
		Sheet: "Other", Row: 1, Column: 2, Cell: spreadsheet.Cell{Value: "other literal"},
	}); err != nil {
		t.Fatal(err)
	}
	driveSheet(t, m, m.load()())
	driveSheet(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	driveSheet(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1+2")})
	if !m.find.active || len(m.searchMatches) != 1 || m.sheet != 1 || m.row != 0 || m.column != 0 {
		t.Fatalf("formula search: active=%t matches=%+v selection=%d,%d,%d",
			m.find.active, m.searchMatches, m.sheet, m.row, m.column)
	}
	if got := m.values[[2]int{0, 0}]; got != "3" {
		t.Fatalf("evaluated formula value = %q", got)
	}
	for _, query := range []string{"3", "other literal"} {
		driveSheet(t, m, tea.KeyMsg{Type: tea.KeyEsc})
		driveSheet(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
		driveSheet(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(query)})
		if len(m.searchMatches) != 1 || m.sheet != 1 || m.row != 0 ||
			(query == "3" && m.column != 0) || (query != "3" && m.column != 1) {
			t.Fatalf("search %q: matches=%+v selection=%d,%d,%d",
				query, m.searchMatches, m.sheet, m.row, m.column)
		}
	}
}

func TestPresentationSearchFindsSpeakerNotesAndRevealsThem(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)
	var notesQuery string
	for _, slide := range m.deck.Slides {
		if words := strings.Fields(slide.Notes); len(words) > 0 {
			notesQuery = words[len(words)-1]
			break
		}
	}
	if notesQuery == "" {
		t.Skip("example presentation has no speaker notes")
	}
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(notesQuery)})
	noteMatch := -1
	for i, match := range m.searchMatches {
		if match.inNotes {
			noteMatch = i
			break
		}
	}
	if noteMatch < 0 {
		t.Fatalf("search %q did not find speaker notes: %+v", notesQuery, m.searchMatches)
	}
	m.searchMatch = noteMatch
	m.gotoSearchMatch()
	if !m.showNotes || m.page != m.searchMatches[noteMatch].page {
		t.Fatalf("speaker-note match did not reveal its slide: page=%d notes=%t match=%+v",
			m.page, m.showNotes, m.searchMatches[noteMatch])
	}
}

func newSearchHarness(t *testing.T, kind artifact.Kind, mode string) *harness {
	t.Helper()
	h := newHarness(t, "")
	var err error
	switch kind {
	case artifact.NoteKind:
		_, err = h.notes.Create(context.Background(), h.path, "needle needle")
	case artifact.DocumentKind:
		_, err = h.docs.Create(context.Background(), h.path, document.Draft{
			Body: "needle needle", Layout: document.DefaultLayout(),
		})
	case artifact.SpreadsheetKind:
		_, err = h.sheets.Create(context.Background(), h.path, [][]spreadsheet.Cell{{{Value: "needle"}}})
	case artifact.PresentationKind:
		_, err = h.decks.Create(context.Background(), h.path, "# Title\n\n## Slide\n\nneedle needle")
	}
	if err != nil {
		t.Fatal(err)
	}
	h.open(kind)
	if (kind == artifact.DocumentKind || kind == artifact.PresentationKind) && mode != "reader" {
		h.key("e")
	}
	if mode == "preview" {
		h.key("f5")
	}
	return h
}

func TestRetainedFindQueryIsAppliedToSourceEditor(t *testing.T) {
	for _, kind := range []artifact.Kind{artifact.NoteKind, artifact.DocumentKind, artifact.PresentationKind} {
		t.Run(string(kind), func(t *testing.T) {
			mode := "reader"
			if kind == artifact.NoteKind {
				mode = "preview"
			}
			h := newSearchHarness(t, kind, mode)
			h.key("ctrl+f")
			h.typeText("needle")
			h.key("enter")
			if kind == artifact.NoteKind {
				h.key("f5")
			} else {
				h.key("e")
			}
			h.key("ctrl+f")
			var count int
			switch kind {
			case artifact.NoteKind:
				count = h.m.bodyInput.MatchCount()
			case artifact.DocumentKind:
				count = h.m.documents.body.MatchCount()
			case artifact.PresentationKind:
				count = h.m.deck.editor.MatchCount()
			}
			if count != 2 {
				t.Fatalf("retained query was not applied to source: matches=%d", count)
			}
		})
	}
}

func TestFindReceivesClipboardResultInEachTarget(t *testing.T) {
	switch runtime.GOOS {
	case "linux", "freebsd", "netbsd", "openbsd", "dragonfly", "solaris":
	default:
		t.Skip("clipboard command stubs require a Unix clipboard backend")
	}
	bin := t.TempDir()
	for _, name := range []string{"xclip", "xsel", "wl-paste", "termux-clipboard-get", "powershell.exe"} {
		script := "#!/bin/sh\nprintf needle\n"
		if name == "powershell.exe" {
			script = "#!/bin/sh\nprintf 'needle\\r\\n'\n"
		}
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	unsupported := clipboard.Unsupported
	clipboard.Unsupported = false
	t.Cleanup(func() { clipboard.Unsupported = unsupported })
	message := textinput.Paste()
	if fmt.Sprint(message) != "needle" {
		t.Fatalf("stub clipboard result = %#v", message)
	}
	for _, target := range []struct {
		kind artifact.Kind
		mode string
	}{
		{artifact.NoteKind, "editor"}, {artifact.NoteKind, "preview"},
		{artifact.DocumentKind, "editor"}, {artifact.DocumentKind, "reader"}, {artifact.DocumentKind, "preview"},
		{artifact.SpreadsheetKind, "reader"},
		{artifact.PresentationKind, "editor"}, {artifact.PresentationKind, "reader"},
	} {
		t.Run(string(target.kind)+"/"+target.mode, func(t *testing.T) {
			h := newSearchHarness(t, target.kind, target.mode)
			h.key("ctrl+f")
			h.send(message)
			if view := ansi.Strip(h.m.View()); !strings.Contains(view, "Find: needle") ||
				strings.Contains(view, "no matches") {
				t.Fatalf("clipboard result did not update Find and matches:\n%s", view)
			}
		})
	}
}

func TestPresentationSearchInvalidatedAfterSavingRemovedSlide(t *testing.T) {
	m, service, _ := newPresentationTestModel(t)
	item, err := service.Update(context.Background(), m.item, "# Title\n\n## First\n\nneedle\n\n## Last\n\nneedle")
	if err != nil {
		t.Fatal(err)
	}
	m.setItem(item)
	m.find.open(m.width)
	m.find.input.SetValue("needle")
	m.searchSlides("needle")
	m.moveSearchMatch(1)
	m.find.blur()
	m.beginEdit()
	m.editor.SetValue("# Title\n\n## First\n\nneedle")
	drivePresentation(t, m, sheetKey("ctrl+s"))
	if m.editing || m.find.visible || len(m.searchMatches) != 0 {
		t.Fatalf("save retained stale search: editing=%t visible=%t matches=%+v error=%s",
			m.editing, m.find.visible, m.searchMatches, m.errMessage)
	}
	drivePresentation(t, m, sheetKey("n"))
	if m.page >= m.pageCount() {
		t.Fatalf("navigation selected removed page %d", m.page)
	}
	m.searchMatches = []renderedMatch{{page: 99}}
	m.searchMatch = 0
	m.gotoSearchMatch()
	if m.searchMatch != -1 {
		t.Fatal("stale destination was not rejected")
	}
}

func TestSpreadsheetSearchInvalidatedByWorkbookChanges(t *testing.T) {
	m, service, _, path := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "home"}}})
	if _, err := service.AddSheet(context.Background(), path, "Other"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetCellInSheet(context.Background(), path, spreadsheet.SetCellOptions{
		Sheet: "Other", Row: 1, Column: 1, Cell: spreadsheet.Cell{Value: "needle"},
	}); err != nil {
		t.Fatal(err)
	}
	driveSheet(t, m, m.load()())
	search := func() {
		t.Helper()
		driveSheet(t, m, sheetKey("/"))
		driveSheet(t, m, sheetKey("needle"))
		driveSheet(t, m, sheetKey("enter"))
	}
	cleared := func() {
		t.Helper()
		if m.find.visible || len(m.searchMatches) != 0 {
			t.Fatalf("workbook change retained stale search: %+v", m.searchMatches)
		}
		driveSheet(t, m, sheetKey("n"))
	}
	search()
	driveSheet(t, m, sheetKey("d"))
	driveSheet(t, m, sheetKey("s"))
	cleared()
	driveSheet(t, m, sheetKey("u"))
	search()
	driveSheet(t, m, sheetKey("ctrl+r"))
	cleared()
	driveSheet(t, m, sheetKey("u"))
	if !m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
		return spreadsheet.SetCellInBook(book, spreadsheet.SetCellOptions{
			Sheet: "Sheet1", Row: 1, Column: 1, Cell: spreadsheet.Cell{Value: "draft"},
		})
	}) {
		t.Fatal(m.errMessage)
	}
	search()
	driveSheet(t, m, sheetKey("u"))
	cleared()
	search()
	m.setBook(m.book)
	cleared()
	m.searchMatches = []spreadsheetSearchMatch{{sheet: 99}}
	m.searchMatch = 0
	m.gotoSearchMatch()
	if m.searchMatch != -1 {
		t.Fatal("stale sheet destination was not rejected")
	}
}

func TestViewerResizePreservesSearchResultAndScroll(t *testing.T) {
	t.Run("note preview", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		h.typeText(strings.Repeat("needle\n\n", 40))
		h.key("f5")
		h.key("/")
		h.typeText("needle")
		h.key("enter")
		h.m.previewMatch = len(h.m.previewMatches) - 1
		h.m.movePreviewMatch(0)
		current, offset := h.m.previewMatch, h.m.preview.YOffset
		for _, size := range []tea.WindowSizeMsg{{Width: 100, Height: 25}, {Width: 90, Height: 25}} {
			h.send(size)
			if h.m.previewMatch != current || h.m.preview.YOffset != offset {
				t.Fatalf("resize reset preview: match=%d/%d offset=%d/%d",
					h.m.previewMatch, current, h.m.preview.YOffset, offset)
			}
		}
	})
	t.Run("document reader", func(t *testing.T) {
		h := newHarness(t, "")
		if _, err := h.docs.Create(context.Background(), h.path, document.Draft{
			Body: "needle\n\n" + document.PageBreakMarkup + "\n\nneedle", Layout: document.DefaultLayout(),
		}); err != nil {
			t.Fatal(err)
		}
		h.open(artifact.DocumentKind)
		h.key("/")
		h.typeText("needle")
		h.key("enter")
		s := h.m.documents
		current, page, offset := s.readerMatch, s.readerPage, s.reader.YOffset
		for _, size := range []tea.WindowSizeMsg{{Width: 100, Height: 25}, {Width: 90, Height: 25}} {
			h.send(size)
			if s.readerMatch != current || s.readerPage != page || s.reader.YOffset != offset {
				t.Fatalf("resize reset reader: match=%d/%d page=%d/%d offset=%d/%d",
					s.readerMatch, current, s.readerPage, page, s.reader.YOffset, offset)
			}
		}
	})
	t.Run("presentation", func(t *testing.T) {
		m, _, _ := newPresentationTestModel(t)
		item := m.item
		item.Source = "# Title\n\n## First\n\nneedle\n\n## Last\n\n" + strings.Repeat("filler\n\n", 30) + "needle"
		m.setItem(item)
		drivePresentation(t, m, sheetKey("/"))
		drivePresentation(t, m, sheetKey("needle"))
		if view := m.View(); !strings.Contains(view, "1/2") || strings.Contains(view, "no matches") {
			t.Fatalf("live viewer search has the wrong count:\n%s", view)
		}
		drivePresentation(t, m, sheetKey("enter"))
		current, page, offset := m.searchMatch, m.page, m.scroll
		for _, size := range []tea.WindowSizeMsg{{Width: 100, Height: 25}, {Width: 90, Height: 25}} {
			drivePresentation(t, m, size)
			if m.searchMatch != current || m.page != page || m.scroll != offset {
				t.Fatalf("resize reset slides: match=%d/%d page=%d/%d offset=%d/%d",
					m.searchMatch, current, m.page, page, m.scroll, offset)
			}
		}
	})
}

func TestSpreadsheetEscapeDismissesBlurredFindBeforeClosing(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "needle"}}})
	driveSheet(t, m, sheetKey("/"))
	driveSheet(t, m, sheetKey("needle"))
	driveSheet(t, m, sheetKey("enter"))
	driveSheet(t, m, sheetKey("d"))
	driveSheet(t, m, sheetKey("esc"))
	if m.pendingShortcut != "" || !m.find.visible {
		t.Fatal("Escape did not cancel the pending shortcut before Find")
	}
	if result := driveSheet(t, m, sheetKey("esc")); result != nil || m.find.visible || m.closeWarning {
		t.Fatalf("Escape closed the workbook instead of Find: result=%#v visible=%t warning=%t",
			result, m.find.visible, m.closeWarning)
	}
}

func TestSpreadsheetHighlightedFormulaUsesDisplayWidth(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Formula: "=123456789+123456789"}}})
	m.theme = resolveTheme(config.Theme{Mode: "dark"})
	m.width = 18
	plain := m.renderFormulaBar()
	m.find.visible = true
	m.find.input.SetValue("123")
	highlighted := m.renderFormulaBar()
	if ansi.Strip(highlighted) != plain || ansi.StringWidth(highlighted) != m.width {
		t.Fatalf("highlighting changed truncation: plain=%q highlighted=%q", plain, highlighted)
	}
}
