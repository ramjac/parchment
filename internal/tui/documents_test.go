package tui

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
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

func TestAltFormattingFromTitleFocusesBody(t *testing.T) {
	m, _ := newDocumentsModel(t)
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	typeText(t, m, "Title")
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true})
	typeText(t, m, "x")
	s := m.documents
	if !s.body.Focused() || s.titleInput.Focused() {
		t.Fatal("formatting from the title did not focus the body")
	}
	if s.body.Value() != "**x**" || s.titleInput.Value() != "Title" {
		t.Fatalf("body = %q, title = %q", s.body.Value(), s.titleInput.Value())
	}
}

func TestSectionPromptRequiresWholeValue(t *testing.T) {
	m, _ := newDocumentsModel(t)
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	s := m.documents
	for _, value := range []string{"2junk", "2.5", "2cc", "5", "0"} {
		s.finishPrompt(promptSection, value)
		if s.body.Value() != "" || s.errMessage == "" {
			t.Fatalf("section value %q accepted: body %q", value, s.body.Value())
		}
	}
	s.finishPrompt(promptSection, "2c")
	if !strings.Contains(s.body.Value(), document.SectionBreakMarkup(2, true)) {
		t.Fatalf("valid section value rejected: %q", s.body.Value())
	}
}

func TestSmallTerminalQDoesNotQuitEditor(t *testing.T) {
	m, _ := newDocumentsModel(t)
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	typeText(t, m, "Draft")
	_, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	cmd, _ := m.documents.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("q quit the editor with unsaved changes")
		}
	}
	if m.documents.mode != documentEditing {
		t.Fatal("editor closed")
	}
}

func TestCancelledImageLoadIsDiscarded(t *testing.T) {
	m, _ := newDocumentsModel(t)
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := t.TempDir() + "/pixel.png"
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg := readImageCommand(context.Background(), path)().(imageLoadedMsg); msg.err != nil {
		t.Fatalf("valid image failed to load: %v", msg.err)
	}
	msg := readImageCommand(ctx, path)()
	m.documents.pending = true
	m.documents.update(msg)
	if len(m.documents.images) != 0 || m.documents.body.Value() != "" || m.documents.pending {
		t.Fatal("cancelled image load changed the document")
	}
	if !strings.Contains(m.documents.status, "cancelled") {
		t.Fatalf("status = %q", m.documents.status)
	}
}

func TestEditorRefusesDocumentItCannotPreserve(t *testing.T) {
	m, docs := newDocumentsModel(t)
	long := strings.Repeat("t", 250)
	created, err := docs.Create(context.Background(), document.Draft{Title: long, Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	s := m.documents
	if s.mode != documentBrowsing || !strings.Contains(s.errMessage, "exceeds editor limits") {
		t.Fatalf("mode = %d, error = %q", s.mode, s.errMessage)
	}
	stored, err := docs.Get(context.Background(), created.ID)
	if err != nil || stored.Title != long {
		t.Fatalf("stored title changed: %q, %v", stored.Title, err)
	}
}
