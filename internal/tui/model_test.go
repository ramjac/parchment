package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/workspace"
)

func TestResponsiveMinimumAndHelpModal(t *testing.T) {
	ws := openTestWorkspace(t)
	model := NewModel(note.NewService(ws, 10), ws, "test", ws.Root())
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	model = *updated.(*Model)
	if view := model.View(); !strings.Contains(view, "too small") || !strings.Contains(view, "press q") {
		t.Fatalf("small terminal view = %q", view)
	}
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	model = *updated.(*Model)
	if command == nil {
		t.Fatal("q in minimum-size mode did not request quit")
	}
	updated, _ = model.Update(notesLoadedMsg{})
	model = *updated.(*Model)
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

func openTestWorkspace(t *testing.T) *workspace.Workspace {
	t.Helper()
	root := t.TempDir()
	if err := workspace.Init(root); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestPendingCtrlCCancelsOperationAndQuits(t *testing.T) {
	ws := openTestWorkspace(t)
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

func TestMinimumSizeQuitCancelsPendingOperation(t *testing.T) {
	ws := openTestWorkspace(t)
	model := NewModel(note.NewService(ws, 10), ws, "test", ws.Root())
	opCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model.pending = true
	model.cancelOperation = cancel
	model.width, model.height = 30, 8

	_, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if command == nil {
		t.Fatal("q in minimum-size mode did not request quit")
	}
	if !errors.Is(opCtx.Err(), context.Canceled) {
		t.Fatalf("pending operation context error = %v, want context canceled", opCtx.Err())
	}
}

func TestPendingSaveCancellationKeepsEditorOpen(t *testing.T) {
	ws := openTestWorkspace(t)
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
	ws := openTestWorkspace(t)
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

func TestViewSanitizesTerminalControlSequences(t *testing.T) {
	ws := openTestWorkspace(t)
	model := NewModel(note.NewService(ws, 10), ws, "workspace\x1b[2Jname", "/tmp/workspace\x1b]52;c;payload\a")
	model.width, model.height = 100, 20
	model.notes = []note.Note{{
		Artifact: artifact.Artifact{
			Title: "Title\x1b[2J",
			Tags:  []string{"tag\x07"},
		},
		Body: "Body\x1b]52;c;payload\a\nnext line",
	}}
	model.resizePreview()

	view := model.View()
	for _, unsafe := range []string{
		"\x1b[2J",
		"\x1b]52;",
		"\x07",
	} {
		if strings.Contains(view, unsafe) {
			t.Errorf("view contains terminal control sequence %q: %q", unsafe, view)
		}
	}
	if !strings.Contains(view, "�[2J") || !strings.Contains(view, "�]52;") {
		t.Fatalf("view did not visibly sanitize workspace and note strings: %q", view)
	}

	model.mode = editing
	model.titleInput.SetValue("Edited\x1b[2J")
	model.bodyInput.SetValue("Edited body\x1b]52;c;payload\a")
	view = model.View()
	if strings.Contains(view, "\x1b[2J") || strings.Contains(view, "\x1b]52;") {
		t.Fatalf("editor view contains pasted terminal controls: %q", view)
	}
}

func TestEditRefusesNotesThatEditorWouldNormalize(t *testing.T) {
	cases := []struct {
		name  string
		title string
		body  string
	}{
		{name: "long title", title: strings.Repeat("x", 201), body: "body"},
		{name: "tab in body", title: "Title", body: "before\tafter"},
		{name: "control in body", title: "Title", body: "before\x1b[2Jafter"},
		{name: "oversized body", title: "Title", body: strings.Repeat("x", 1_000_001)},
		{name: "too many body lines", title: "Title", body: strings.Repeat("x\n", 10_000) + "last"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := openTestWorkspace(t)
			model := NewModel(note.NewService(ws, 10), ws, "test", ws.Root())
			model.width, model.height = 100, 20
			model.pending = false
			model.notes = []note.Note{{
				Artifact: artifact.Artifact{ID: "id", Title: tc.title},
				Body:     tc.body,
			}}
			updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
			model = *updated.(*Model)
			if model.mode != browsing {
				t.Fatalf("lossy note opened for editing; mode=%d", model.mode)
			}
			if model.errMessage == "" {
				t.Fatal("refused edit did not explain why")
			}
			if model.titleInput.Value() == tc.title && model.bodyInput.Value() == tc.body {
				t.Fatal("test note was representable and should have entered the editor")
			}
		})
	}
}

func TestEditSaveRejectsConcurrentExternalChange(t *testing.T) {
	ctx := context.Background()
	ws := openTestWorkspace(t)
	service := note.NewService(ws, 10)
	created, err := service.Create(ctx, "Original title", "Original body")
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(service, ws, "test", ws.Root())
	model.pending = false
	model.notes = []note.Note{created}
	model.width, model.height = 100, 20
	if !model.startEdit(created) {
		t.Fatal("could not start editing note")
	}
	model.bodyInput.SetValue("Edited body")

	external := created
	external.Title = "Changed outside TUI"
	external.ModifiedAt = external.ModifiedAt.Add(time.Second)
	if err := ws.Save(ctx, external); err != nil {
		t.Fatal(err)
	}

	result := model.saveNote()().(noteSavedMsg)
	if result.err == nil {
		t.Fatal("save succeeded despite external modification")
	}
	if !strings.Contains(result.err.Error(), "changed since this operation") {
		t.Fatalf("save error = %v, want snapshot conflict", result.err)
	}
	updated, _ := model.Update(result)
	model = *updated.(*Model)
	if model.mode != editing || model.errMessage == "" {
		t.Fatalf("conflicted save did not keep the editor open and show an error: mode=%v error=%q", model.mode, model.errMessage)
	}
	current, err := ws.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Title != external.Title || current.Body != created.Body {
		t.Fatalf("stale editor overwrote external update: %+v", current)
	}
}

func TestPreviewCanScrollInWideLayout(t *testing.T) {
	ws := openTestWorkspace(t)
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
	ws := openTestWorkspace(t)
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
