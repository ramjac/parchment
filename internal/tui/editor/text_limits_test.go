package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ramjac/parchment/internal/notetext"
)

func TestPlainTextInputLimitsPreserveRecoverableBuffer(t *testing.T) {
	for _, source := range []string{
		strings.Repeat("x", notetext.MaxText),
		strings.Repeat("\n", notetext.MaxLines-1),
	} {
		m := New()
		if err := m.SetDocument(notetext.Split(source)); err != nil {
			t.Fatal(err)
		}
		original := m.Document()
		m.InsertString("extra\n")
		if !m.Document().Equal(original) || m.TakeInputError() == nil || m.CanUndo() {
			t.Fatal("oversized insertion changed the buffer")
		}
	}
	m := New()
	m.Focus()
	if err := m.SetDocument(notetext.Split("a\r\nb")); err != nil {
		t.Fatal(err)
	}
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x\xff"})
	if m.Value() != "a\nb" || m.TakeInputError() == nil {
		t.Fatal("malformed key text was converted to replacement characters")
	}
}
