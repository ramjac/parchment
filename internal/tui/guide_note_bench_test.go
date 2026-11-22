package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/filerepo"
	"example.com/parchment/internal/note"
)

func guideNoteModel(tb testing.TB, width, height int) (*Model, string, string) {
	tb.Helper()
	var parts []string
	for _, path := range []string{"README.md", "docs/User_Guide.md", "docs/Technical_Guide.md"} {
		data, err := os.ReadFile(filepath.Join("..", "..", path))
		if err != nil {
			tb.Fatal(err)
		}
		parts = append(parts, string(data))
	}
	body := strings.Join(parts, "\n")
	path := filepath.Join(tb.TempDir(), "guides.md")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		tb.Fatal(err)
	}
	repo, err := filerepo.New(tb.TempDir(), filerepo.Options{})
	if err != nil {
		tb.Fatal(err)
	}
	model := NewModel(Config{Path: path, Kind: artifact.NoteKind, Notes: note.NewService(repo, 10), UndoLimit: 10})
	_, _ = model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	message := model.Init()()
	_, _ = model.Update(message)
	if model.stage != stageNote || model.pending || model.errMessage != "" {
		tb.Fatalf("guide note did not open: %s", model.errMessage)
	}
	return &model, path, body
}

func saveGuideNote(tb testing.TB, m *Model) {
	tb.Helper()
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		tb.Fatal("save did not return a command")
	}
	_, _ = m.Update(cmd())
	if m.pending || m.errMessage != "" {
		tb.Fatalf("save failed: %s", m.errMessage)
	}
}

func TestGuideNoteWorkflow(t *testing.T) {
	m, path, body := guideNoteModel(t, 80, 24)
	_, _ = m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	expected := m.bodyInput.Value()
	saveGuideNote(t, m)
	saved, err := os.ReadFile(path)
	if err != nil || string(saved) != expected || expected == body {
		t.Fatalf("plain note edit did not round-trip: %v", err)
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyF5})
	if !m.previewing || m.View().Content == "" {
		t.Fatal("guide note preview did not open")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyF5})
	if m.previewing || m.bodyInput.Value() != expected {
		t.Fatal("preview changed note content")
	}
}

func BenchmarkGuideNoteWorkflow(b *testing.B) {
	b.Run("Open80x24", func(b *testing.B) {
		base, path, _ := guideNoteModel(b, 80, 24)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			m := NewModel(Config{Path: path, Kind: artifact.NoteKind, Notes: base.service, UndoLimit: 10})
			_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			_, _ = m.Update(m.Init()())
			_ = m.View().Content
			if m.stage != stageNote || m.errMessage != "" {
				b.Fatalf("guide note did not open: %s", m.errMessage)
			}
		}
	})
	for _, size := range []struct{ width, height int }{{80, 24}, {120, 40}} {
		b.Run(fmt.Sprintf("Preview/%dx%d", size.width, size.height), func(b *testing.B) {
			m, _, _ := guideNoteModel(b, size.width, size.height)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyF5})
				_ = m.View().Content
				_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
				_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyF5})
			}
			b.StopTimer()
			if m.previewing {
				b.Fatal("note did not return to editor")
			}
		})
	}
	b.Run("EditSave", func(b *testing.B) {
		m, path, _ := guideNoteModel(b, 80, 24)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if i%2 == 0 {
				_, _ = m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
			} else {
				_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
			}
			saveGuideNote(b, m)
		}
		b.StopTimer()
		saved, err := os.ReadFile(path)
		if err != nil || string(saved) != m.bodyInput.Value() {
			b.Fatalf("saved note did not match editor: %v", err)
		}
	})
}
