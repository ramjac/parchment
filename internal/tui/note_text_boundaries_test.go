package tui

import (
	"os"
	"testing"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/tui/editor"
)

func TestPlainNoteBoundaryOnlyEditsAreDirtyAndSaved(t *testing.T) {
	h := newHarness(t, "")
	if err := os.WriteFile(h.path, []byte("a\r\nb\nc"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.NoteKind)
	h.m.bodyInput.Cursor = editor.Point{Line: 0, Col: 1}
	h.key("delete")
	h.key("enter")
	if h.m.bodyInput.Value() != h.m.originalBody || !h.m.dirty() {
		t.Fatal("boundary-only edit was treated as clean")
	}
	h.key("ctrl+s")
	got, _ := os.ReadFile(h.path)
	if string(got) != "a\nb\nc" || h.m.dirty() {
		t.Fatalf("boundary-only save = %q, error=%q", got, h.m.errMessage)
	}
	h.key("ctrl+z")
	got, _ = os.ReadFile(h.path)
	if string(got) != "a\r\nb\nc" {
		t.Fatal("saved undo lost original mixed boundaries")
	}
}
