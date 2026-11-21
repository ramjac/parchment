package tui

import (
	"bytes"
	"compress/zlib"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"example.com/parchment/internal/artifact"
)

func TestNotePDFExportUsesUnsavedEditsAndRemembersPath(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)
	h.typeText("Unsaved **note**")
	original, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "note.pdf")
	h.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if h.m.pdfPrompt != promptPDF || !strings.Contains(h.m.View().Content, "PDF output path:") {
		t.Fatal("Ctrl+P did not open the note PDF prompt")
	}
	h.m.pdfInput.SetValue(output)
	h.key("enter")
	pdf, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !notePDFContains(pdf, "Unsaved") || !notePDFContains(pdf, "note") {
		t.Fatal("PDF did not render the unsaved note")
	}
	saved, err := os.ReadFile(h.path)
	if err != nil || !bytes.Equal(saved, original) || !h.m.dirty() {
		t.Fatalf("PDF export changed the note or editor state: %v", err)
	}
	h.key("f5")
	h.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if h.m.pdfInput.Value() != output {
		t.Fatalf("remembered path = %q", h.m.pdfInput.Value())
	}
	h.key("enter")
	if h.m.pdfPrompt != promptPDFOverwrite || !strings.Contains(h.m.View().Content, "Overwrite?") {
		t.Fatal("note preview did not offer overwrite confirmation")
	}
	h.key("n")
	if data, err := os.ReadFile(output); err != nil || !bytes.Equal(data, pdf) {
		t.Fatalf("declined overwrite changed PDF: %v", err)
	}
	if err := os.WriteFile(output, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	h.key("enter")
	h.key("y")
	if data, err := os.ReadFile(output); err != nil || !bytes.HasPrefix(data, []byte("%PDF-")) {
		t.Fatalf("confirmed overwrite failed: %v", err)
	}
	h.open(artifact.NoteKind)
	h.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if got := h.m.pdfInput.Value(); got != output {
		t.Fatalf("reopened note PDF path = %q", got)
	}
}

func TestNotePDFPromptAcceptsBracketedPaste(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)
	h.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if h.m.pdfPrompt != promptPDF {
		t.Fatal("Ctrl+P did not open the note PDF prompt")
	}
	h.m.pdfInput.SetValue("")
	h.send(tea.PasteMsg{Content: "/tmp/pasted.pdf"})
	if got := h.m.pdfInput.Value(); got != "/tmp/pasted.pdf" {
		t.Fatalf("pasted PDF output path = %q", got)
	}

	h.m.pending = true
	h.send(tea.PasteMsg{Content: "ignored-pending"})
	h.m.pending = false
	if got := h.m.pdfInput.Value(); got != "/tmp/pasted.pdf" {
		t.Fatalf("paste during a pending operation changed the output path: %q", got)
	}

	h.m.pdfPrompt = promptPDFOverwrite
	h.send(tea.PasteMsg{Content: "ignored-overwrite"})
	if got := h.m.pdfInput.Value(); got != "/tmp/pasted.pdf" {
		t.Fatalf("paste during overwrite confirmation changed the output path: %q", got)
	}
}

func notePDFContains(pdf []byte, text string) bool {
	for _, stream := range bytes.Split(pdf, []byte("stream\n"))[1:] {
		reader, err := zlib.NewReader(bytes.NewReader(stream))
		if err != nil {
			continue
		}
		content, err := io.ReadAll(reader)
		_ = reader.Close()
		if err == nil && bytes.Contains(content, []byte(text)) {
			return true
		}
	}
	return false
}

func TestNotePDFExportCannotOverwriteSource(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)
	before, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatal(err)
	}
	h.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	h.m.pdfInput.SetValue(h.path)
	h.key("enter")
	h.key("y")
	after, err := os.ReadFile(h.path)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("source note changed: %v", err)
	}
	if !strings.Contains(h.m.errMessage, "cannot overwrite the source file") {
		t.Fatalf("error = %q", h.m.errMessage)
	}
	if _, err := h.notes.Get(context.Background(), h.path); err != nil {
		t.Fatalf("source note is no longer readable: %v", err)
	}
}
