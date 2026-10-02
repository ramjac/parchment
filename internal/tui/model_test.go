package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
	model = updated.(Model)
	updated, _ = model.Update(notesLoadedMsg{})
	model = updated.(Model)
	if view := model.View(); !strings.Contains(view, "too small") || !strings.Contains(view, "press q") {
		t.Fatalf("small terminal view = %q", view)
	}
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	model = updated.(Model)
	if model.mode != browsing || !strings.Contains(model.View(), "Close help") {
		t.Fatalf("help overlay did not retain focus: mode=%d view=%q", model.mode, model.View())
	}
}
