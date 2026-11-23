package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

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

func TestTextMatchesUnicodeAndNonOverlapping(t *testing.T) {
	tests := []struct {
		text, query string
		want        []textMatch
	}{
		{"aaaa", "aa", []textMatch{{0, 2, 0, 2}, {2, 4, 2, 4}}},
		{"KKk", "k", []textMatch{{0, 3, 0, 1}, {3, 4, 1, 2}, {4, 5, 2, 3}}},
		{"KKk", "K", []textMatch{{3, 4, 1, 2}}},
		{"界 a\u0301", "a\u0301", []textMatch{{4, 7, 3, 4}}},
		{"a\xffb", "\ufffd", []textMatch{{1, 2, 1, 2}}},
	}
	for _, tt := range tests {
		if got := findTextMatches(tt.text, tt.query); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("findTextMatches(%q, %q) = %+v, want %+v", tt.text, tt.query, got, tt.want)
		}
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

func TestDocumentReaderHighlightsVisibleLines(t *testing.T) {
	screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	screen.reader.SetWidth(14)
	screen.reader.SetHeight(2)
	screen.readerLines = []string{
		"\x1b[31mwide界\x1b[0m alpha",
		"ALPHA and alpha",
		"no match here",
		"last alpha",
	}
	screen.showReaderContent()
	screen.find.visible = true
	for _, query := range []string{"alpha", "ALPHA", " ", "界"} {
		screen.find.input.SetValue(query)
		for _, offset := range []int{0, 1, 2} {
			screen.reader.SetYOffset(offset)
			expected := screen.reader
			expected.SetContent(highlightRenderedText(strings.Join(screen.readerLines, "\n"), query, screen.theme))
			expected.SetYOffset(offset)
			if got, want := screen.readerContentView(), expected.View(); got != want {
				t.Fatalf("query %q offset %d: got %q, want %q", query, offset, got, want)
			}
			if screen.reader.YOffset() != offset {
				t.Fatalf("query %q changed reader position", query)
			}
		}
	}
}

func TestDocumentReaderSearchIgnoresStaleResults(t *testing.T) {
	screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	screen.width = 80
	screen.reading = true
	screen.snapshot.Body = strings.Repeat("x", asyncReaderSearchBytes)
	screen.reader.SetWidth(30)
	screen.reader.SetHeight(1)
	screen.readerLines = []string{"alpha beta", "beta again", "another alpha"}
	screen.showReaderContent()
	screen.find.visible = true
	screen.find.input.SetValue("alpha")
	first := screen.searchReader("alpha")
	if first == nil || !screen.readerSearching || !strings.Contains(screen.readerStatus(), "Searching") {
		t.Fatal("large reader search did not start asynchronously")
	}
	stale := first()
	screen.find.input.SetValue("beta")
	second := screen.searchReader("beta")
	screen.moveReaderMatch(1)
	model := Model{stage: stageDocument, documents: screen}
	_, _ = model.Update(stale)
	if !screen.readerSearching || len(screen.readerMatches) != 0 {
		t.Fatal("stale search result replaced the current search")
	}
	_, _ = model.Update(second())
	if screen.readerSearching || len(screen.readerMatches) != 2 || screen.readerMatch != 1 || screen.reader.YOffset() != 1 {
		t.Fatalf("current search = %+v, match=%d, offset=%d", screen.readerMatches, screen.readerMatch, screen.reader.YOffset())
	}
	screen.find.input.SetValue("alpha")
	cancelled := screen.searchReader("alpha")
	screen.find.close()
	screen.searchReader("")
	screen.update(cancelled())
	if screen.readerSearching || len(screen.readerMatches) != 0 {
		t.Fatal("closed search accepted a cancelled result")
	}
}

func TestRenderedMatchesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := renderedMatchesContext(ctx, []string{strings.Repeat("x", 100000)}, "z", 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled search returned %v", err)
	}
}

func TestRenderedReaderMatchesBoundAndSemantics(t *testing.T) {
	lines := []string{"\x1b[31mAlpha alpha\x1b[0m", "界 alpha 界", "e\u0301 é", "missing"}
	for _, query := range []string{"alpha", "Alpha", "界", "é", "\u0301", "missing"} {
		want := renderedMatches(lines, query, 0)
		got, err := renderedReaderMatchesContext(context.Background(), lines, query, 20)
		if err != nil || len(got) != len(want) {
			t.Fatalf("query %q: got %d matches, want %d: %v", query, len(got), len(want), err)
		}
		for i := range want {
			if got[i].row != want[i].row {
				t.Fatalf("query %q: row %d = %d, want %d", query, i, got[i].row, want[i].row)
			}
		}
	}
	matches, err := renderedReaderMatchesContext(context.Background(), []string{"xxx"}, "x", 3)
	if err != nil || len(matches) != 3 {
		t.Fatalf("matches at limit = %d, err=%v", len(matches), err)
	}
	matches, err = renderedReaderMatchesContext(context.Background(), []string{strings.Repeat("x", 10000)}, "x", 3)
	if !errors.Is(err, errReaderSearchLimit) || matches != nil {
		t.Fatalf("excess matches returned %v, err=%v", matches, err)
	}
}

func TestDocumentReaderReportsSearchLimit(t *testing.T) {
	screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	screen.width, screen.reading, screen.readerSearching = 80, true, true
	screen.find.visible = true
	screen.find.input.SetValue("x")
	screen.readerSearchFinished(documentReaderSearchMsg{
		session: screen.readerSearchSession, query: "x", err: errReaderSearchLimit,
	})
	if !strings.Contains(screen.readerStatus(), "refine the search") {
		t.Fatal("reader did not report the match limit")
	}
	screen.searchReader("")
	if screen.readerSearchError != "" {
		t.Fatal("closing search retained the match-limit error")
	}
}

func TestDocumentReaderSearchCancelWhileRunning(t *testing.T) {
	screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	screen.reading = true
	screen.snapshot.Body = strings.Repeat("x", 3<<20)
	screen.readerLines = []string{screen.snapshot.Body}
	screen.find.visible = true
	screen.find.input.SetValue("absent")
	cmd := screen.searchReader("absent")
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	screen.cancelReaderSearch()
	select {
	case result := <-done:
		msg := result.(documentReaderSearchMsg)
		if !errors.Is(msg.err, context.Canceled) {
			t.Fatalf("cancelled search returned %v", msg.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled search did not stop")
	}
}

func TestDocumentReaderResizePreservesPendingSearchNavigation(t *testing.T) {
	screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	screen.resize(80, 24)
	screen.startReading(document.Document{Body: "# Heading\n\nbeta beta", Layout: document.DefaultLayout()})
	screen.find.visible = true
	screen.find.input.SetValue("beta")
	screen.readerSearching = true
	screen.readerSearchSteps = 1
	screen.readerMatches = nil
	screen.readerMatch = -1
	screen.resize(100, 30)
	if screen.readerSearching || len(screen.readerMatches) != 2 || screen.readerMatch != 1 {
		t.Fatalf("resize lost pending search navigation: matches=%+v current=%d", screen.readerMatches, screen.readerMatch)
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
	driveSheet(t, m, tea.KeyPressMsg{Code: []rune(string("/"))[0], Text: string("/")})
	driveSheet(t, m, tea.KeyPressMsg{Code: []rune(string("1+2"))[0], Text: string("1+2")})
	if !m.find.active || len(m.searchMatches) != 1 || m.sheet != 1 || m.row != 0 || m.column != 0 {
		t.Fatalf("formula search: active=%t matches=%+v selection=%d,%d,%d",
			m.find.active, m.searchMatches, m.sheet, m.row, m.column)
	}
	if got := m.values[[2]int{0, 0}]; got != "3" {
		t.Fatalf("evaluated formula value = %q", got)
	}
	for _, query := range []string{"3", "other literal"} {
		driveSheet(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
		driveSheet(t, m, tea.KeyPressMsg{Code: []rune(string("/"))[0], Text: string("/")})
		driveSheet(t, m, tea.KeyPressMsg{Code: []rune(string(query))[0], Text: string(query)})
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
	drivePresentation(t, m, tea.KeyPressMsg{Code: []rune(string("/"))[0], Text: string("/")})
	drivePresentation(t, m, tea.KeyPressMsg{Code: []rune(string(notesQuery))[0], Text: string(notesQuery)})
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
			if view := ansi.Strip(h.m.View().Content); !strings.Contains(view, "Find: needle") ||
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
		current, offset := h.m.previewMatch, h.m.preview.YOffset()
		for _, size := range []tea.WindowSizeMsg{{Width: 100, Height: 25}, {Width: 90, Height: 25}} {
			h.send(size)
			if h.m.previewMatch != current || h.m.preview.YOffset() != offset {
				t.Fatalf("resize reset preview: match=%d/%d offset=%d/%d",
					h.m.previewMatch, current, h.m.preview.YOffset(), offset)
			}
		}
	})
	t.Run("document reader", func(t *testing.T) {
		h := newHarness(t, "")
		if _, err := h.docs.Create(context.Background(), h.path, document.Draft{
			Body: strings.Repeat("A long line of words that wraps differently when the document reader changes width.\n\n", 25) +
				"needle\n\n" + document.PageBreakMarkup + "\n\n" + strings.Repeat("Filler.\n\n", 20) + "needle\n\n" +
				strings.Repeat("End.\n\n", 20), Layout: document.DefaultLayout(),
		}); err != nil {
			t.Fatal(err)
		}
		h.open(artifact.DocumentKind)
		h.key("/")
		h.typeText("needle")
		h.key("enter")
		s := h.m.documents
		current := s.readerMatch
		for _, size := range []tea.WindowSizeMsg{{Width: 100, Height: 25}, {Width: 60, Height: 25}, {Width: 90, Height: 25}} {
			h.send(size)
			row := s.readerMatches[s.readerMatch].row
			if s.readerMatch != current || row < s.reader.YOffset() || row >= s.reader.YOffset()+s.reader.Height() {
				t.Fatalf("resize lost reader match %d/%d at row %d, viewport %d:%d",
					s.readerMatch, current, row, s.reader.YOffset(), s.reader.YOffset()+s.reader.Height())
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
		if view := m.View().Content; !strings.Contains(view, "1/2") || strings.Contains(view, "no matches") {
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
	previousProfile := terminalColorProfile
	terminalColorProfile = colorprofile.TrueColor
	t.Cleanup(func() { terminalColorProfile = previousProfile })
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
