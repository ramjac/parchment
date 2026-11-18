package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"example.com/parchment/internal/artifact"
)

func TestPresentationPDFExportViewerAndUnsavedEditor(t *testing.T) {
	h := newHarness(t, artifact.PresentationKind)
	deck := h.m.deck
	original, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "slides.pdf")
	h.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	if deck.pdfPrompt != promptPDF {
		t.Fatal("viewer did not open PDF prompt")
	}
	deck.pdfInput.SetValue(output)
	h.key("enter")
	if pdf, err := os.ReadFile(output); err != nil || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("viewer PDF export failed: %v, %s", err, deck.errMessage)
	}
	h.key("e")
	deck.editor.SetValue("# Unsaved title\n\n## Draft slide\n\nUnsaved content\n\n: Draft reminder\n")
	h.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	if deck.pdfInput.Value() != output {
		t.Fatalf("remembered path = %q", deck.pdfInput.Value())
	}
	h.key("enter")
	if deck.pdfPrompt != promptPDFOverwrite {
		t.Fatal("did not offer overwrite confirmation")
	}
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	h.key("n")
	if pdf, err := os.ReadFile(output); err != nil || !bytes.Equal(pdf, before) {
		t.Fatalf("declining overwrite changed PDF: %v", err)
	}
	h.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	h.key("enter")
	h.key("y")
	pdf, err := os.ReadFile(output)
	if err != nil || !notePDFContains(pdf, "Unsaved") || !notePDFContains(pdf, "reminder") {
		t.Fatalf("PDF missing unsaved content or notes: %v, %s", err, deck.errMessage)
	}
	if saved, err := os.ReadFile(h.path); err != nil || !bytes.Equal(saved, original) || !deck.editing {
		t.Fatalf("export changed source or closed editor: %v", err)
	}
	h.open(artifact.PresentationKind)
	h.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	if h.m.deck.pdfInput.Value() != output {
		t.Fatalf("reopened remembered path = %q", h.m.deck.pdfInput.Value())
	}
}

func TestPresentationPDFExportErrorsAndCancellation(t *testing.T) {
	h := newHarness(t, artifact.PresentationKind)
	deck := h.m.deck
	before, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatal(err)
	}

	h.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	deck.pdfInput.SetValue(h.path)
	h.key("enter")
	h.key("y")
	if saved, err := os.ReadFile(h.path); err != nil || !bytes.Equal(saved, before) ||
		!strings.Contains(deck.errMessage, "cannot overwrite the source file") {
		t.Fatalf("source protection failed: %v, %s", err, deck.errMessage)
	}
	h.key("e")
	deck.editor.SetValue("not a presentation")
	output := filepath.Join(t.TempDir(), "invalid.pdf")
	h.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	deck.pdfInput.SetValue(output)
	h.key("enter")
	if deck.errMessage == "" {
		t.Fatal("invalid draft did not produce an error")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("invalid draft created output: %v", err)
	}
	deck.editor.SetValue("# Title\n\n## Slide\n\n" + strings.Repeat(": Excessive speaker notes.\n", 500))
	h.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	deck.pdfInput.SetValue(output)
	h.key("enter")
	if !strings.Contains(deck.errMessage, "fit on one PDF page") {
		t.Fatalf("oversized draft error = %q", deck.errMessage)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("oversized draft created output: %v", err)
	}
	deck.editor.SetValue("# Title\n\n## Slide\n\nContent\n")
	cmd := deck.exportPDF(output, false)
	if cmd == nil || !deck.pending {
		t.Fatal("export did not start")
	}
	_, _ = deck.Update(tea.KeyMsg{Type: tea.KeyEsc})
	msg := cmd()
	_, _ = deck.Update(msg)
	if deck.pending || deck.pdfPending || deck.status != "PDF export cancelled" {
		t.Fatalf("cancelled export state: pending=%v pdfPending=%v status=%q", deck.pending, deck.pdfPending, deck.status)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("cancelled export created output: %v", err)
	}
}

func TestPresentationPDFTwoUpFromEditorAndOverwrite(t *testing.T) {
	h := newHarness(t, artifact.PresentationKind)
	deck := h.m.deck
	h.key("e")
	deck.editor.SetValue("# Title\n\n## First\n\nContent\n\n: Reminder\n\n## Second\n\nLast slide\n")
	output := filepath.Join(t.TempDir(), "slides.pdf")
	if err := os.WriteFile(output, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	if !strings.Contains(deck.pdfInput.Prompt, "1 slide, landscape") {
		t.Fatalf("default PDF layout prompt = %q", deck.pdfInput.Prompt)
	}
	h.key("tab")
	if !strings.Contains(deck.pdfInput.Prompt, "2 slides, portrait") {
		t.Fatalf("two-up PDF layout prompt = %q", deck.pdfInput.Prompt)
	}
	deck.pdfInput.SetValue(output)
	h.key("enter")
	if deck.pdfPrompt != promptPDFOverwrite {
		t.Fatal("missing overwrite confirmation")
	}
	h.key("y")
	pdf, err := os.ReadFile(output)
	if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF-")) ||
		!bytes.Contains(pdf, []byte("/Count 2")) || !notePDFContains(pdf, "Reminder") {
		t.Fatalf("two-up export failed: %v, %s", err, deck.errMessage)
	}
	if !deck.editing || deck.editor.Value() == deck.item.Source {
		t.Fatal("export changed or discarded unsaved edits")
	}
}
