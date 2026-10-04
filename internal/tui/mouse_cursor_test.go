package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"example.com/parchment/internal/note"
	"example.com/parchment/internal/spreadsheet"
)

func TestPlaceTextareaCursorInScrolledAndWrappedText(t *testing.T) {
	editor := textarea.New()
	editor.Prompt = ""
	editor.ShowLineNumbers = false
	editor.SetWidth(12)
	editor.SetHeight(3)
	editor.SetValue("first\nsecond\nthird\nfourth\nfifth")
	editor.Focus()
	editor.View()
	for range 4 {
		editor, _ = editor.Update(tea.KeyMsg{Type: tea.KeyDown})
		editor.View()
	}
	if !placeTextareaCursor(&editor, 2, 0) {
		copy := editor
		copy.Cursor.Blink = false
		t.Fatalf("click in scrolled textarea was ignored: %q", copy.View())
	}
	editor.InsertString("!")
	if !strings.Contains(editor.Value(), "th!ird") {
		t.Fatalf("click inserted on wrong line: %q", editor.Value())
	}
	editor.SetValue("abcdefghijklmnopqrstuv")
	editor.SetWidth(8)
	editor.SetHeight(4)
	editor.Focus()
	if !placeTextareaCursor(&editor, 3, 1) {
		t.Fatal("click in wrapped line was ignored")
	}
	editor.InsertString("!")
	if editor.Value() != "abcdefghijk!lmnopqrstuv" {
		t.Fatalf("wrapped click inserted at %q", editor.Value())
	}
}

func TestNoteClickPositionsBodyCursor(t *testing.T) {
	repository := openTestWorkspace(t)
	model := NewModel(note.NewService(repository, 10), repository, "note.md", "note.md", WithSingleMarkdownFile())
	model.width, model.height = 80, 24
	model.bodyInput.SetWidth(76)
	model.bodyInput.SetHeight(14)
	model.bodyInput.SetValue("alpha\nbravo")
	model.bodyInput.Focus()
	model.mode, model.pending = editing, false
	model.Update(tea.MouseMsg{X: 2, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	model.bodyInput.InsertString("!")
	if model.bodyInput.Value() != "alpha\nbr!avo" {
		t.Fatalf("note click positioned cursor at %q", model.bodyInput.Value())
	}
	model.Update(tea.MouseMsg{X: 1, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	model.bodyInput.InsertString("?")
	if model.bodyInput.Value() != "alpha\nbr!?avo" {
		t.Fatalf("header click moved note cursor: %q", model.bodyInput.Value())
	}
}

func TestDocumentClickPositionsBodyCursor(t *testing.T) {
	m, _ := newDocumentsModel(t)
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	s := m.documents
	s.body.SetValue("alpha\nbravo")
	y := toolbarTop + s.toolbarRows() + 1
	drive(t, m, tea.MouseMsg{X: 2, Y: y + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	s.body.InsertString("!")
	if s.body.Value() != "alpha\nbr!avo" {
		t.Fatalf("document click positioned cursor at %q", s.body.Value())
	}
	drive(t, m, tea.MouseMsg{X: 1, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	s.body.InsertString("?")
	if s.body.Value() != "alpha\nbr!?avo" {
		t.Fatalf("header click moved document cursor: %q", s.body.Value())
	}
}

func TestPresentationClickPositionsSourceCursor(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)
	m.beginEdit()
	m.editor.SetValue("alpha\nbravo")
	m.editor.Focus()
	m.Update(tea.MouseMsg{X: 5, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m.editor.InsertString("!")
	if m.editor.Value() != "alpha\nbr!avo" {
		t.Fatalf("presentation click positioned cursor at %q", m.editor.Value())
	}
	m.Update(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m.editor.InsertString("?")
	if m.editor.Value() != "alpha\nbr!?avo" {
		t.Fatalf("header click moved presentation cursor: %q", m.editor.Value())
	}
}

func TestSpreadsheetClickSelectsCellAndPositionsInput(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "One"}, {Value: "Two"}}})
	m.Update(tea.MouseMsg{X: m.rowHeaderWidth() + spreadsheetColumnWidth + 3, Y: 3,
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.column != 1 || m.row != 0 {
		t.Fatalf("clicked cell selected row=%d column=%d", m.row, m.column)
	}
	m.Update(tea.MouseMsg{X: 0, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
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
	repository := openTestWorkspace(t)
	model := NewModel(note.NewService(repository, 10), repository, "note.md", "note.md", WithSingleMarkdownFile())
	model.width, model.height = 80, 24
	model.resizeEditors()
	body := note.Note{Body: strings.Repeat("line\n", 60) + "end"}
	body.ID = "x"
	if !model.startEdit(body) {
		t.Fatal("note did not open for editing")
	}
	for i := 0; i < 59; i++ {
		model.bodyInput.CursorUp()
	}
	if !strings.Contains(model.View(), "\x1b[7m") {
		t.Fatalf("cursor at line %d is not visible in the editor view", model.bodyInput.Line())
	}
}

func TestPlaceTextareaCursorWithGrowingLineNumbers(t *testing.T) {
	editor := textarea.New()
	editor.Prompt = ""
	editor.ShowLineNumbers = true
	editor.MaxHeight = 0
	editor.SetWidth(30)
	editor.SetHeight(14)
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = "abcdef"
	}
	editor.SetValue(strings.Join(lines, "\n"))
	editor.Focus()
	editor.View()
	// Line 12 has a two-digit number, so its text starts one column later.
	if !placeTextareaCursor(&editor, 6, 11) {
		t.Fatal("click on a two-digit line was ignored")
	}
	editor.InsertString("!")
	if got := strings.Split(editor.Value(), "\n")[11]; got != "ab!cdef" {
		t.Fatalf("click on line 12 inserted at %q", got)
	}
}
