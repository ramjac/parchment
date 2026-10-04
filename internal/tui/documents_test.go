package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
)

// drive sends a message and feeds the results of returned commands back into
// the model, as the Bubble Tea runtime would.
func drive(t *testing.T, m *Model, message tea.Msg) {
	t.Helper()
	_, cmd := m.Update(message)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch result := c().(type) {
		case tea.BatchMsg:
			for _, inner := range result {
				run(inner)
			}
		case nil:
		default:
			_, next := m.Update(result)
			run(next)
		}
	}
	run(cmd)
}

func typeText(t *testing.T, m *Model, text string) {
	t.Helper()
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
}

func newDocumentsModel(t *testing.T) (*Model, *document.Service) {
	t.Helper()
	ws := openTestWorkspace(t)
	docs := document.NewService(ws, 10)
	model := NewModel(note.NewService(ws, 10), ws, "test", ws.Root(), WithDocuments(docs))
	m := &model
	drive(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	drive(t, m, notesLoadedMsg{})
	return m, docs
}

func TestDocumentEditorHasToolbarAndSavesLayout(t *testing.T) {
	m, docs := newDocumentsModel(t)
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if !m.documentsActive {
		t.Fatal("Tab did not open the documents screen")
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	view := m.View()
	for _, label := range []string{"Save", "Preview", "Bold", "H1", "Page break", "Section", "Cols: 1", "Margins: Normal", "Header", "Footer", "Image"} {
		if label == "Bold" {
			label = "B"
		}
		if !strings.Contains(view, label) {
			t.Fatalf("editor toolbar is missing %q:\n%s", label, view)
		}
	}
	typeText(t, m, "Report")
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	typeText(t, m, "Intro")
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true})
	typeText(t, m, "bold")
	m.documents.act("pagebreak")
	m.documents.act("columns")
	if _, err := docs.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	list, err := docs.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("documents = %v, err = %v", list, err)
	}
	got := list[0]
	if got.Title != "Report" || got.Layout.Columns != 2 {
		t.Fatalf("saved document = %q columns %d", got.Title, got.Layout.Columns)
	}
	if !strings.Contains(got.Body, "Intro**bold") || !strings.Contains(got.Body, document.PageBreakMarkup) {
		t.Fatalf("saved body = %q", got.Body)
	}
	if m.documents.mode != documentBrowsing {
		t.Fatal("editor did not close after saving")
	}
}

func TestToolbarMouseClickAndUnsavedEscape(t *testing.T) {
	m, _ := newDocumentsModel(t)
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	s := m.documents
	placed, _ := s.buttonLayout(s.width)
	buttons := s.toolbarButtons()
	for _, p := range placed {
		if buttons[p.index].action == "columns" {
			drive(t, m, tea.MouseMsg{X: p.x + 1, Y: toolbarTop + p.row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		}
	}
	if s.layout.Columns != 2 {
		t.Fatalf("clicking the columns button left %d columns", s.layout.Columns)
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if s.mode != documentEditing {
		t.Fatal("Esc discarded unsaved layout changes without confirmation")
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if s.mode != documentBrowsing {
		t.Fatal("second Esc did not close the editor")
	}
}

func TestDocumentsTabReturnsToNotes(t *testing.T) {
	m, _ := newDocumentsModel(t)
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.documentsActive {
		t.Fatal("Tab did not return to notes")
	}
}
