package tui

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/notetext"
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

func TestPlainNoteBoundaryCollisionIsSavedAndRecovered(t *testing.T) {
	h := newHarness(t, "")
	original := []byte("a\rb\nc")
	if err := os.WriteFile(h.path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.NoteKind)
	h.m.bodyInput.Cursor = editor.Point{Line: 1, Col: 1}
	h.m.bodyInput.Selection = editor.Selection{
		Anchor: editor.Point{Line: 1, Col: 0}, Active: true,
	}
	h.m.bodyInput.DeleteSelection()

	source, err := h.m.bodyInput.Document().Source()
	if err != nil || source != "a\r\r\nc" {
		t.Fatalf("edited source = %q, %v; want both logical newlines", source, err)
	}
	h.run(h.m.saveNoteRecovery(h.m.autosaveSession))
	draft, exists, err := h.repo.LoadRecovery(context.Background(), h.path)
	if err != nil || !exists {
		t.Fatalf("recovery draft = %t, %v", exists, err)
	}
	var data noteRecoveryData
	if err := json.Unmarshal(draft.Data, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Endings) != 2 || data.Endings[0] != notetext.CR || data.Endings[1] != notetext.CRLF {
		t.Fatalf("recovery endings = %#v; want [CR, CRLF]", data.Endings)
	}

	h.key("ctrl+s")
	got, err := os.ReadFile(h.path)
	if err != nil || string(got) != source {
		t.Fatalf("saved source = %q, %v; want %q", got, err, source)
	}
	h.key("ctrl+z")
	got, err = os.ReadFile(h.path)
	if err != nil || string(got) != string(original) {
		t.Fatalf("undo source = %q, %v; want %q", got, err, original)
	}
}
