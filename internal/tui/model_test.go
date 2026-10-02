package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/workspace"
)

func TestResponsiveMinimumAndHelpModal(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(note.NewService(ws, 10), ws, "test", ws.Root())
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	model = *updated.(*Model)
	updated, _ = model.Update(notesLoadedMsg{})
	model = *updated.(*Model)
	if view := model.View(); !strings.Contains(view, "too small") || !strings.Contains(view, "press q") {
		t.Fatalf("small terminal view = %q", view)
	}
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	model = *updated.(*Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	model = *updated.(*Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	model = *updated.(*Model)
	if model.mode != browsing || !strings.Contains(model.View(), "Close help") {
		t.Fatalf("help overlay did not retain focus: mode=%d view=%q", model.mode, model.View())
	}
}

func TestPendingCtrlCCancelsOperationAndQuits(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(note.NewService(ws, 10), ws, "test", ws.Root())
	opCtx, cancel := context.WithCancel(context.Background())
	model.pending = true
	model.cancelOperation = cancel

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if command == nil {
		t.Fatal("Ctrl+C while pending did not request quit")
	}
	if err := opCtx.Err(); err == nil {
		t.Fatal("Ctrl+C did not cancel the pending operation")
	}
	if _, ok := updated.(*Model); !ok {
		t.Fatalf("updated model has type %T", updated)
	}
}

func TestPendingSaveCancellationKeepsEditorOpen(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(note.NewService(ws, 10), ws, "test", ws.Root())
	opCtx, cancel := context.WithCancel(context.Background())
	model.mode = editing
	model.pending = true
	model.cancelOperation = cancel

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = *updated.(*Model)
	if command != nil || model.mode != editing || !model.pending {
		t.Fatalf("cancelled save state: command=%v mode=%d pending=%t", command, model.mode, model.pending)
	}
	if err := opCtx.Err(); err == nil {
		t.Fatal("Ctrl+C did not cancel the pending save")
	}
	if !strings.Contains(model.status, "Cancelling save") {
		t.Fatalf("status = %q", model.status)
	}
	updated, _ = model.Update(noteSavedMsg{err: context.Canceled})
	model = *updated.(*Model)
	if model.pending || model.mode != editing || !strings.Contains(model.status, "unsaved changes remain") {
		t.Fatalf("completed cancelled save state: pending=%t mode=%d status=%q", model.pending, model.mode, model.status)
	}
}

func TestNarrowListKeepsSelectedNoteVisible(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(note.NewService(ws, 10), ws, "test", ws.Root())
	model.width, model.height, model.selected = 50, 12, 15
	for i := range 30 {
		model.notes = append(model.notes, note.Note{Artifact: artifact.Artifact{Title: fmt.Sprintf("Note %02d", i)}})
	}
	model.resizePreview()

	view := model.View()
	if !strings.Contains(view, "Note 15") {
		t.Fatalf("selected note is not visible in narrow list:\n%s", view)
	}
	if strings.Contains(view, "Note 00") {
		t.Fatalf("narrow list rendered notes outside its visible window:\n%s", view)
	}
}

func TestPreviewCanScrollInWideLayout(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(note.NewService(ws, 10), ws, "test", ws.Root())
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 16})
	model = *updated.(*Model)
	body := make([]string, 30)
	for i := range body {
		body[i] = fmt.Sprintf("Line %02d", i+1)
	}
	updated, _ = model.Update(notesLoadedMsg{notes: []note.Note{{
		Artifact: artifact.Artifact{Title: "Long note"},
		Body:     strings.Join(body, "\n"),
	}}})
	model = *updated.(*Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = *updated.(*Model)
	if view := model.View(); !strings.Contains(view, "Line 01") || strings.Contains(view, "Line 30") {
		t.Fatalf("initial preview viewport = %q", view)
	}
	for range 20 {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
		model = *updated.(*Model)
	}
	if view := model.View(); !strings.Contains(view, "Line 20") {
		t.Fatalf("scrolled preview does not show later content: %q", view)
	}
}

func TestReloadPreservesActiveSearch(t *testing.T) {
	ctx := context.Background()
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := note.NewService(ws, 10)
	if _, err := service.Create(ctx, "Matching note", "needle"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, "Other note", "unrelated"); err != nil {
		t.Fatal(err)
	}
	model := NewModel(service, ws, "test", ws.Root())
	model.searchActive = true
	model.searchQuery = "needle"
	model.errMessage = "previous failure"

	result := model.loadNotes()().(searchCompletedMsg)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if len(result.notes) != 1 || result.notes[0].Title != "Matching note" {
		t.Fatalf("reload results = %+v, want active query preserved", result.notes)
	}
	updated, _ := model.Update(searchCompletedMsg{notes: result.notes})
	model = *updated.(*Model)
	if model.errMessage != "" {
		t.Fatalf("successful reload retained old error: %q", model.errMessage)
	}
}
