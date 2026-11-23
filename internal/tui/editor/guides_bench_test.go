package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/notetext"
)

func BenchmarkGuideNoteEditor(b *testing.B) {
	paths := []string{"README.md", "docs/User_Guide.md", "docs/Technical_Guide.md"}
	parts := make([]string, len(paths))
	for i, path := range paths {
		content, err := os.ReadFile(filepath.Join("..", "..", "..", path))
		if err != nil {
			b.Fatal(err)
		}
		parts[i] = string(content)
	}
	text := strings.Join(parts, "\n")
	if err := notetext.ValidateText(text); err != nil {
		b.Fatal(err)
	}
	b.Logf("guide note: %d bytes, %d lines", len(text), strings.Count(text, "\n")+1)

	b.Run("Load", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			m := New()
			m.SetValue(text)
		}
	})
	for _, size := range []struct{ width, height int }{{80, 24}, {120, 40}} {
		for _, position := range []struct {
			name string
			line func(int) int
		}{
			{"Start", func(int) int { return 0 }},
			{"Middle", func(lines int) int { return lines / 2 }},
			{"End", func(lines int) int { return lines - 1 }},
		} {
			b.Run(fmt.Sprintf("View/%dx%d/%s", size.width, size.height, position.name), func(b *testing.B) {
				m := New()
				m.Width, m.Height = size.width, size.height
				m.SetValue(text)
				row := 0
				if position.name == "End" {
					row = m.Height - 1
				}
				m.SetCursorAtRow(Point{Line: position.line(len(m.Lines))}, row)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = m.View()
				}
			})
		}
	}
	b.Run("Find", func(b *testing.B) {
		m := New()
		m.SetValue(text)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			m.SetSearch("artifact")
		}
	})
	b.Run("InsertAndUndo", func(b *testing.B) {
		m := New()
		m.SetValue(text)
		m.SetCursorAtRow(Point{Line: len(m.Lines) / 2}, 0)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			m.InsertString("x")
			if !m.Undo() {
				b.Fatal("insert did not create an undo step")
			}
		}
		b.StopTimer()
		if m.Value() != text {
			b.Fatal("insert and undo did not restore the note")
		}
	})
}
