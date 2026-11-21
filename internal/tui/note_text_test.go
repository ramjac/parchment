package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/notetext"
)

func TestPlainNoteTUIEncodingAndBoundaries(t *testing.T) {
	for _, format := range []notetext.Format{
		{Encoding: notetext.UTF8},
		{Encoding: notetext.UTF8, BOM: true},
		{Encoding: notetext.UTF16LE, BOM: true},
		{Encoding: notetext.UTF16BE, BOM: true},
	} {
		t.Run(format.Encoding+map[bool]string{true: "-BOM"}[format.BOM], func(t *testing.T) {
			h := newHarness(t, "")
			raw, err := notetext.Encode(notetext.Split("one\r\ntwo\rthree\n"), format)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(h.path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			h.open(artifact.NoteKind)
			if h.m.stage != stageNote || h.m.dirty() || h.m.bodyInput.Value() != "one\ntwo\nthree\n" {
				t.Fatalf("stage=%d, dirty=%t, error=%q", h.m.stage, h.m.dirty(), h.m.errMessage)
			}
			h.key("ctrl+s")
			got, _ := os.ReadFile(h.path)
			if !bytes.Equal(got, raw) {
				t.Fatal("no-op save changed bytes")
			}
			h.send(tea.PasteMsg{Content: "new\r\nline\r"})
			if !h.m.dirty() {
				t.Fatal("paste did not mark note dirty")
			}
			h.key("ctrl+s")
			want, _ := notetext.Encode(notetext.Split("new\r\nline\r\none\r\ntwo\rthree\n"), format)
			got, _ = os.ReadFile(h.path)
			if !bytes.Equal(got, want) || h.m.dirty() {
				t.Fatalf("save mismatch, error=%q", h.m.errMessage)
			}
			h.key("ctrl+z")
			got, _ = os.ReadFile(h.path)
			if !bytes.Equal(got, raw) || h.m.dirty() {
				t.Fatal("saved undo lost source format")
			}
			h.key("ctrl+r")
			got, _ = os.ReadFile(h.path)
			if !bytes.Equal(got, want) {
				t.Fatal("saved redo lost source format")
			}
		})
	}
}

func TestLegacyNoteRecoveryRetainsBytesAndUnrepresentableEdits(t *testing.T) {
	h := newHarness(t, "")
	raw := []byte("caf\xe9\r\n")
	if err := os.WriteFile(h.path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.NoteKind)
	if h.m.stage != stageFailed || !strings.Contains(h.m.errMessage, "ambiguous") {
		t.Fatal("ambiguous encoding did not require a choice")
	}
	h.m.noteEncoding = notetext.Windows1252
	h.run(h.m.openFile(artifact.NoteKind))
	h.typeText("😀")
	h.key("ctrl+s")
	if h.m.errMessage == "" || !h.m.dirty() {
		t.Fatal("unrepresentable edit was not retained")
	}
	h.run(h.m.saveNoteRecovery(h.m.autosaveSession))
	draft, exists, err := h.repo.LoadRecovery(context.Background(), h.path)
	if err != nil || !exists {
		t.Fatalf("draft = %t, %v", exists, err)
	}
	var data noteRecoveryData
	if err := json.Unmarshal(draft.Data, &data); err != nil || !bytes.Equal(data.Raw, raw) {
		t.Fatalf("recovery lost original bytes: %v", err)
	}
	h.open(artifact.NoteKind)
	if h.m.stage != stageRecovery {
		t.Fatalf("reopen without override: stage=%d, error=%q", h.m.stage, h.m.errMessage)
	}
	h.key("r")
	if h.m.stage != stageNote || h.m.bodyInput.Value() != "😀café\n" || !h.m.dirty() {
		t.Fatalf("recover = %q, error=%q", h.m.bodyInput.Value(), h.m.errMessage)
	}
	h.key("ctrl+s")
	if h.m.errMessage == "" {
		t.Fatal("recovered unrepresentable edit was saved")
	}
	got, _ := os.ReadFile(h.path)
	if !bytes.Equal(got, raw) {
		t.Fatal("failed save changed encoded source")
	}
	h.key("delete")
	h.key("ctrl+s")
	if h.m.dirty() || h.m.errMessage != "" {
		t.Fatalf("corrected draft could not be saved: %q", h.m.errMessage)
	}
	if _, exists, err := h.repo.LoadRecovery(context.Background(), h.path); err != nil || exists {
		t.Fatalf("saved draft not removed: %t, %v", exists, err)
	}
}

func TestPlainNoteRecoveryCanOpenWhenChangedFileCannotDecode(t *testing.T) {
	h := newHarness(t, "")
	raw := []byte("price \x80\r")
	if err := os.WriteFile(h.path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.NoteKind)
	h.typeText("changed ")
	h.run(h.m.saveNoteRecovery(h.m.autosaveSession))
	external := []byte("binary\x00")
	if err := os.WriteFile(h.path, external, 0o600); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.NoteKind)
	if h.m.stage != stageRecovery {
		t.Fatalf("stage=%d, error=%q", h.m.stage, h.m.errMessage)
	}
	h.key("r")
	if h.m.stage != stageNote || !strings.Contains(h.m.status, "file changed") {
		t.Fatalf("recovery did not warn: %q, %q", h.m.status, h.m.errMessage)
	}
	h.key("ctrl+s")
	if h.m.errMessage == "" || !h.m.dirty() {
		t.Fatal("recovered draft overwrote external file")
	}
	got, _ := os.ReadFile(h.path)
	if !bytes.Equal(got, external) {
		t.Fatal("external file changed")
	}
	h.open(artifact.NoteKind)
	h.key("d")
	if h.m.stage != stageFailed || h.m.errMessage == "" {
		t.Fatal("discard did not retry safe decoding")
	}
}
