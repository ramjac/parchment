package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/workspace"
)

// drivePresentation sends a message and feeds presentation results back in.
// While editing, only a pending save command runs; textarea focus and cursor
// blink commands are skipped because executing them sleeps and repeats.
// Results other than presentation messages are dropped for the same reason.
func drivePresentation(t *testing.T, m *presentationModel, message tea.Msg) tea.Msg {
	t.Helper()
	_, cmd := m.Update(message)
	var last tea.Msg
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil || (m.editing && !m.pending) {
			return
		}
		switch result := c().(type) {
		case tea.BatchMsg:
			for _, inner := range result {
				run(inner)
			}
		case nil:
		case tea.QuitMsg:
			last = result
		case presentationLoadedMsg, presentationSavedMsg, presentationHistoryMsg:
			last = result
			_, next := m.Update(result)
			run(next)
		}
	}
	run(cmd)
	return last
}

func newPresentationTestModel(t *testing.T) (*presentationModel, *presentation.Service, string) {
	t.Helper()
	sample, err := os.ReadFile("../../examples/presentation.md")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "deck.md")
	if err := os.WriteFile(path, sample, 0o600); err != nil {
		t.Fatal(err)
	}
	repository, err := workspace.OpenPresentationFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service := presentation.NewService(repository, 10)
	items, err := repository.ListPresentations(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("list standalone presentation = %v, %v", items, err)
	}
	m := newPresentationModel(service, repository, items[0].ID, "deck.md")
	drivePresentation(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	drivePresentation(t, m, m.Init()())
	if !m.loaded || m.pending || m.errMessage != "" {
		t.Fatalf("model not loaded: loaded %t pending %t err %q", m.loaded, m.pending, m.errMessage)
	}
	return m, service, path
}

func storedPresentation(t *testing.T, path string) presentation.Presentation {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	item, err := presentation.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func TestPresentationViewNavigatesSlidesAndNotes(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)
	view := m.View()
	for _, want := range []string{"Product Update", "deck.md", "Parchment team", "Title page"} {
		if !strings.Contains(view, want) {
			t.Fatalf("title page missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "parchment-meta") || strings.Contains(view, "parchment-body") {
		t.Fatalf("view leaked envelope:\n%s", view)
	}
	drivePresentation(t, m, sheetKey("right"))
	view = m.View()
	if !strings.Contains(view, "What shipped") || !strings.Contains(view, "Slide 1 of") {
		t.Fatalf("first slide not shown:\n%s", view)
	}
	withNotes := -1
	for index, slide := range m.deck.Slides {
		if slide.Notes != "" {
			withNotes = index
			break
		}
	}
	if withNotes < 0 {
		t.Fatal("example presentation has no speaker notes")
	}
	for m.page < withNotes+1 {
		drivePresentation(t, m, sheetKey("n"))
	}
	if strings.Contains(m.View(), m.deck.Slides[withNotes].Notes) {
		t.Fatal("speaker notes shown before toggling")
	}
	drivePresentation(t, m, sheetKey("s"))
	if view := m.View(); !strings.Contains(view, "Speaker notes") ||
		!strings.Contains(view, m.deck.Slides[withNotes].Notes) {
		t.Fatalf("notes not shown:\n%s", view)
	}
	drivePresentation(t, m, sheetKey("end"))
	if m.page != len(m.deck.Slides) {
		t.Fatalf("end page = %d", m.page)
	}
	drivePresentation(t, m, sheetKey("right"))
	if m.page != len(m.deck.Slides) {
		t.Fatalf("navigation passed last slide: %d", m.page)
	}
	drivePresentation(t, m, sheetKey("home"))
	drivePresentation(t, m, sheetKey("left"))
	if m.page != 0 {
		t.Fatalf("navigation passed title page: %d", m.page)
	}
	if _, ok := drivePresentation(t, m, sheetKey("q")).(tea.QuitMsg); !ok {
		t.Fatal("q did not quit")
	}
}

func TestPresentationEditSavesSourceAndUndoes(t *testing.T) {
	m, _, path := newPresentationTestModel(t)
	original := m.item.Source
	drivePresentation(t, m, sheetKey("right"))
	drivePresentation(t, m, sheetKey("e"))
	if !m.editing {
		t.Fatal("e did not start editing")
	}
	if line := m.editor.Line(); line != slideSourceLine(original, 1) || line == 0 {
		t.Fatalf("cursor line = %d", line)
	}
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	// Typing q while editing inserts text rather than quitting.
	if msg := drivePresentation(t, m, sheetKey(" q")); msg != nil {
		if _, quit := msg.(tea.QuitMsg); quit {
			t.Fatal("q quit while editing")
		}
	}
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.editing || m.pending || m.errMessage != "" || m.status != "Saved" {
		t.Fatalf("save state: editing %t pending %t status %q err %q", m.editing, m.pending, m.status, m.errMessage)
	}
	saved := storedPresentation(t, path)
	if !strings.Contains(saved.Source, "## What shipped q\n") || m.page != 1 ||
		!strings.Contains(m.View(), "What shipped q") {
		t.Fatalf("saved source = %q\nview:\n%s", saved.Source, m.View())
	}
	drivePresentation(t, m, sheetKey("u"))
	if m.errMessage != "" || !strings.HasPrefix(m.status, "Undid") {
		t.Fatalf("undo status %q err %q", m.status, m.errMessage)
	}
	if storedPresentation(t, path).Source != original || m.item.Source != original {
		t.Fatal("undo did not restore original source")
	}
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if storedPresentation(t, path).Source != saved.Source || m.item.Source != saved.Source {
		t.Fatal("redo did not reapply edit")
	}
}

func TestPresentationSaveReturnsToSlideBeingEdited(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)
	original := m.item.Source
	drivePresentation(t, m, sheetKey("e"))
	if m.page != 0 {
		t.Fatal("test must begin on the title page")
	}
	m.moveEditorToLine(slideSourceLine(original, 3))
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	drivePresentation(t, m, sheetKey("!"))
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.editing || m.page != 3 {
		t.Fatalf("after save: editing %t, page %d, want page 3", m.editing, m.page)
	}
	drivePresentation(t, m, sheetKey("e"))
	m.moveEditorToLine(slideSourceLine(original, 1))
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.editing || m.page != 1 {
		t.Fatalf("after cancel: editing %t, page %d, want page 1", m.editing, m.page)
	}
}

func TestPresentationEditRejectsInvalidSourceAndConfirmsDiscard(t *testing.T) {
	m, _, path := newPresentationTestModel(t)
	original := m.item.Source
	drivePresentation(t, m, sheetKey("e"))
	m.editor.SetValue("not a presentation")
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !m.editing || m.pending || !strings.Contains(m.errMessage, "Invalid presentation") {
		t.Fatalf("invalid save state: editing %t pending %t err %q", m.editing, m.pending, m.errMessage)
	}
	if storedPresentation(t, path).Source != original {
		t.Fatal("invalid source was saved")
	}
	drivePresentation(t, m, sheetKey("esc"))
	if !m.editing || !m.discardWarning {
		t.Fatal("dirty esc did not warn")
	}
	drivePresentation(t, m, sheetKey("esc"))
	if m.editing || m.item.Source != original {
		t.Fatal("second esc did not discard draft")
	}
	drivePresentation(t, m, sheetKey("e"))
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.editing || m.status != "No changes" {
		t.Fatalf("unchanged save state: editing %t status %q", m.editing, m.status)
	}
}

func TestPresentationSaveErrorKeepsDraft(t *testing.T) {
	m, _, path := newPresentationTestModel(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	trailer := strings.Index(content, "<!-- parchment-blocks -->")
	if trailer < 0 {
		t.Fatal("presentation trailing-block boundary is missing")
	}
	content = content[:trailer] + "External\n\n" + content[trailer:]
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	drivePresentation(t, m, sheetKey("e"))
	draft := m.item.Source + "\nLocal edit\n"
	m.editor.SetValue(draft)
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !m.editing || m.pending || !strings.Contains(m.errMessage, "changed outside Parchment") {
		t.Fatalf("save error state: editing %t pending %t err %q", m.editing, m.pending, m.errMessage)
	}
	if m.editor.Value() != draft {
		t.Fatal("save failure lost the draft")
	}
	if strings.Contains(storedPresentation(t, path).Source, "Local edit") {
		t.Fatal("external edit was overwritten")
	}
}

func TestPresentationLoadErrorsAndStaleResults(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)
	m.id = "p5"
	drivePresentation(t, m, sheetKey("r"))
	if m.errMessage != "Load failed: presentation not found" {
		t.Fatalf("missing load error = %q", m.errMessage)
	}
	cmd := m.load()
	drivePresentation(t, m, sheetKey("esc"))
	if m.pending || m.status != "Cancelled" {
		t.Fatalf("cancel state: pending %t status %q", m.pending, m.status)
	}
	before := m.item
	drivePresentation(t, m, cmd())
	if !presentation.Equal(before, m.item) || m.status != "Cancelled" {
		t.Fatal("stale load result was applied")
	}
	drivePresentation(t, m, presentationSavedMsg{seq: m.seq, err: context.Canceled})
	if m.errMessage != "" {
		t.Fatalf("cancelled save error shown: %q", m.errMessage)
	}
	m.pending = true
	drivePresentation(t, m, presentationHistoryMsg{seq: m.seq, err: errors.New("boom")})
	if m.errMessage != "History failed: boom" {
		t.Fatalf("history error = %q", m.errMessage)
	}
}
