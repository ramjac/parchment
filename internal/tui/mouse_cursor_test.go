package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/charmbracelet/colorprofile"

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
	ed.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	ed.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	ed.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	ed.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	// Click on visible line 0, col 2 -> line 2 ("third"), col 2
	ed.Update(tea.MouseClickMsg(tea.Mouse{X: 2, Y: 0, Button: tea.MouseLeft}))
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
	m.Update(mouseEvent{X: 4, Y: 3, Action: mousePress, Button: mouseLeft})
	m.editor.InsertRune('!')
	if m.editor.Value() != "alpha\nbr!avo" {
		t.Fatalf("presentation click positioned cursor at %q", m.editor.Value())
	}
	m.Update(mouseEvent{X: 0, Y: 0, Action: mousePress, Button: mouseLeft})
	m.editor.InsertRune('?')
	if m.editor.Value() != "alpha\nbr!?avo" {
		t.Fatalf("header click moved presentation cursor: %q", m.editor.Value())
	}
}

func TestSpreadsheetClickSelectsCellAndPositionsInput(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "One"}, {Value: "Two"}}})
	m.Update(mouseEvent{X: 2 + m.rowHeaderWidth() + spreadsheetColumnWidth + 3, Y: 4,
		Action: mousePress, Button: mouseLeft})
	if m.column != 1 || m.row != 0 {
		t.Fatalf("clicked cell selected row=%d column=%d", m.row, m.column)
	}
	m.Update(mouseEvent{X: 0, Y: 4, Action: mousePress, Button: mouseLeft})
	if m.column != 1 || m.row != 0 {
		t.Fatalf("row-header click selected row=%d column=%d", m.row, m.column)
	}
	m.Update(sheetKey("enter"))
	m.input.SetValue("Two")
	m.Update(mouseEvent{X: len(spreadsheet.CellName(1, 2)) + 3 + 1, Y: 1,
		Action: mousePress, Button: mouseLeft})
	m.Update(tea.KeyPressMsg{Code: []rune(string("!"))[0], Text: string("!")})
	if m.input.Value() != "T!wo" {
		t.Fatalf("spreadsheet input cursor at %q", m.input.Value())
	}
}

func TestNoteEditorShowsCursorAwayFromTextEnd(t *testing.T) {
	compat.Profile = colorprofile.TrueColor
	t.Cleanup(func() { compat.Profile = colorprofile.Ascii })
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
		model.bodyInput.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	if !strings.Contains(model.View().Content, "\x1b[7m") {
		t.Fatalf("cursor at line %d is not visible in the editor view", model.bodyInput.Line())
	}
}
