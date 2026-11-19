package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"example.com/parchment/internal/artifact"
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
