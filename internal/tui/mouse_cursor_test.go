package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/spreadsheet"
	"example.com/parchment/internal/tui/editor"
)

func TestEditorClickAndScrolledText(t *testing.T) {
	ed := editor.New()
	ed.SetWidth(12)
	ed.SetHeight(3)
	ed.SetValue("first\nsecond\nthird\nfourth\nfifth")
	ed.Focus()
	ed.Update(tea.KeyMsg{Type: tea.KeyDown})
	ed.Update(tea.KeyMsg{Type: tea.KeyDown})
	ed.Update(tea.KeyMsg{Type: tea.KeyDown})
	ed.Update(tea.KeyMsg{Type: tea.KeyDown})

	// Click on visible line 0, col 2 -> line 2 ("third"), col 2
	ed.Update(tea.MouseMsg{X: 2, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	ed.InsertRune('!')
	if !strings.Contains(ed.Value(), "th!ird") {
		t.Fatalf("click inserted on wrong line: %q", ed.Value())
	}
}

func TestPresentationClickPositionsSourceCursor(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)
	m.beginEdit()
	m.editor.SetValue("alpha\nbravo")
	m.editor.Focus()
	m.Update(tea.MouseMsg{X: 4, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m.editor.InsertRune('!')
	if m.editor.Value() != "alpha\nbr!avo" {
		t.Fatalf("presentation click positioned cursor at %q", m.editor.Value())
	}
	m.Update(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m.editor.InsertRune('?')
	if m.editor.Value() != "alpha\nbr!?avo" {
		t.Fatalf("header click moved presentation cursor: %q", m.editor.Value())
	}
}

func TestSpreadsheetClickSelectsCellAndPositionsInput(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "One"}, {Value: "Two"}}})
	m.Update(tea.MouseMsg{X: 2 + m.rowHeaderWidth() + spreadsheetColumnWidth + 3, Y: 4,
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.column != 1 || m.row != 0 {
		t.Fatalf("clicked cell selected row=%d column=%d", m.row, m.column)
	}
	m.Update(tea.MouseMsg{X: 0, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.column != 1 || m.row != 0 {
		t.Fatalf("row-header click selected row=%d column=%d", m.row, m.column)
	}
	m.Update(sheetKey("enter"))
	m.input.SetValue("Two")
	m.Update(tea.MouseMsg{X: len(spreadsheet.CellName(1, 2)) + 3 + 1, Y: 1,
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	if m.input.Value() != "T!wo" {
		t.Fatalf("spreadsheet input cursor at %q", m.input.Value())
	}
}

func TestNoteEditorShowsCursorAwayFromTextEnd(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
	model := NewModel(Config{Path: "note.md", Kind: artifact.NoteKind})
	model.width, model.height = 80, 24
	model.resizeEditors()
	body := note.Note{Body: strings.Repeat("line\n", 60) + "end"}
	if !model.startEdit(body) {
		t.Fatal("note did not open for editing")
	}
	model.stage = stageNote
	model.bodyInput.Focus()
	for i := 0; i < 59; i++ {
		model.bodyInput.CursorUp()
	}
	if !strings.Contains(model.View(), "\x1b[7m") {
		t.Fatalf("cursor at line %d is not visible in the editor view", model.bodyInput.Line())
	}
}
