package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"example.com/parchment/internal/notetext"
)

func TestPreservedBoundariesFollowEditsAndUndo(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cursor Point
		key    tea.KeyPressMsg
		want   string
	}{
		{"split", Point{0, 1}, tea.KeyPressMsg{Code: tea.KeyEnter}, "a\r\nb\r\ncd\ref\n"},
		{"backspace join", Point{1, 0}, tea.KeyPressMsg{Code: tea.KeyBackspace}, "abcd\ref\n"},
		{"delete join", Point{1, 2}, tea.KeyPressMsg{Code: tea.KeyDelete}, "ab\r\ncdef\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := notetext.Split("ab\r\ncd\ref\n")
			m := New()
			m.Focus()
			if err := m.SetDocument(original); err != nil {
				t.Fatal(err)
			}
			m.Cursor = tc.cursor
			m.Update(tc.key)
			got, err := m.Document().Source()
			if err != nil || got != tc.want {
				t.Fatalf("source = %q, %v; want %q", got, err, tc.want)
			}
			edited := m.Document()
			if !m.Undo() || !m.Document().Equal(original) || !m.Redo() || !m.Document().Equal(edited) {
				t.Fatal("undo/redo did not restore boundaries")
			}
		})
	}
}

func TestDeletingTextResolvesAdjacentCRLFBoundaries(t *testing.T) {
	original := notetext.Split("a\rb\nc")
	m := New()
	if err := m.SetDocument(original); err != nil {
		t.Fatal(err)
	}
	m.Cursor = Point{Line: 1, Col: 1}
	m.Selection = Selection{Anchor: Point{Line: 1, Col: 0}, Active: true}
	m.DeleteSelection()

	edited := m.Document()
	source, err := edited.Source()
	if err != nil || source != "a\r\r\nc" {
		t.Fatalf("edited source = %q, %v; want both newlines preserved", source, err)
	}
	if !m.Undo() || !m.Document().Equal(original) {
		t.Fatal("undo did not restore original line endings")
	}
	if !m.Redo() || !m.Document().Equal(edited) {
		t.Fatal("redo did not restore adjusted line endings")
	}

	recovered := New()
	if err := recovered.SetDocument(notetext.Document{
		Text: "a\n\nc", Endings: []notetext.Ending{notetext.CR, notetext.LF},
	}); err != nil {
		t.Fatal(err)
	}
	source, err = recovered.Document().Source()
	if err != nil || source != "a\r\r\nc" {
		t.Fatalf("recovered source = %q, %v; want both saved newlines", source, err)
	}
}

func TestNearestEndingAndPasteReplacement(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		cursor Point
		anchor *Point
		paste  string
		want   string
	}{
		{"next", "long\r\nabc\rx", Point{1, 2}, nil, "1\n2", "long\r\nab1\r2c\rx"},
		{"previous tie", "a\r\nabc\rx", Point{1, 1}, nil, "1\r\n2\r3", "a\r\na1\r\n2\r\n3bc\rx"},
		{"empty fallback", "", Point{}, nil, "a\r\nb\rc", "a\nb\nc"},
		{"replace all", "a\r\nb\rc", Point{2, 1}, &Point{0, 0}, "x\r\ny", "x\ny"},
		{"replace boundary", "a\r\nb\rc\n", Point{1, 1}, &Point{0, 1}, "z", "az\rc\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New()
			if err := m.SetDocument(notetext.Split(tc.source)); err != nil {
				t.Fatal(err)
			}
			m.Cursor = tc.cursor
			if tc.anchor != nil {
				m.Selection = Selection{Anchor: *tc.anchor, Active: true}
			}
			m.InsertString(tc.paste)
			got, err := m.Document().Source()
			if err != nil || got != tc.want {
				t.Fatalf("source = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestPlainTextPasteRejectsUnsafeInputBeforeSelectionDeletion(t *testing.T) {
	m := New()
	original := notetext.Split("ab\r\ncd")
	if err := m.SetDocument(original); err != nil {
		t.Fatal(err)
	}
	m.Selection = Selection{Anchor: Point{}, Active: true}
	m.Cursor = Point{1, 2}
	for _, input := range []string{"bad\x1btext", "invalid\xff", "bad\x00"} {
		m.InsertString(input)
		if !m.Document().Equal(original) || m.TakeInputError() == nil || m.CanUndo() {
			t.Fatalf("unsafe paste mutated buffer: %q", input)
		}
	}
}
