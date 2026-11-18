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

func TestSpreadsheetPDFExportDraftAndOverwrite(t *testing.T) {
	h := newHarness(t, artifact.SpreadsheetKind)
	m := h.m.sheet
	original, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatal(err)
	}

	h.key("enter")
	m.input.SetValue("unsaved cell")
	output := filepath.Join(t.TempDir(), "sheet.pdf")
	h.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	if m.pdfPrompt != promptPDF || !strings.Contains(m.View(), "PDF output path:") {
		t.Fatal("missing PDF prompt")
	}
	m.pdfInput.SetValue(output)
	h.key("enter")
	pdf, err := os.ReadFile(output)
	if err != nil || !notePDFContains(pdf, "unsaved cell") {
		t.Fatalf("PDF missing pending edit: %v, %s", err, m.errMessage)
	}
	if saved, err := os.ReadFile(h.path); err != nil || !bytes.Equal(saved, original) || !m.editing {
		t.Fatalf("export changed source or editing state: %v", err)
	}
	h.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	if m.pdfInput.Value() != output {
		t.Fatalf("remembered path = %q", m.pdfInput.Value())
	}
	h.key("enter")
	if m.pdfPrompt != promptPDFOverwrite {
		t.Fatal("missing overwrite prompt")
	}
	h.key("n")
	if data, err := os.ReadFile(output); err != nil || !bytes.Equal(data, pdf) {
		t.Fatalf("declining overwrite changed PDF: %v", err)
	}
	h.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	h.key("enter")
	h.key("y")
	if data, err := os.ReadFile(output); err != nil || !notePDFContains(data, "unsaved cell") {
		t.Fatalf("confirmed overwrite failed: %v, %s", err, m.errMessage)
	}
	h.open(artifact.SpreadsheetKind)
	h.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	if h.m.sheet.pdfInput.Value() != output {
		t.Fatalf("reopened remembered path = %q", h.m.sheet.pdfInput.Value())
	}
}

func TestSpreadsheetPDFExportCancellationAndSourceProtection(t *testing.T) {
	h := newHarness(t, artifact.SpreadsheetKind)
	m := h.m.sheet
	original, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatal(err)
	}
	h.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	m.pdfInput.SetValue(h.path)
	h.key("enter")
	h.key("y")
	if !strings.Contains(m.errMessage, "cannot overwrite the source file") {
		t.Fatalf("missing source protection error: %s", m.errMessage)
	}
	if saved, err := os.ReadFile(h.path); err != nil || !bytes.Equal(saved, original) {
		t.Fatalf("source changed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "cancelled.pdf")
	cmd := m.exportPDF(path, false)
	if cmd == nil || !m.pending {
		t.Fatal("export did not start")
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	_, _ = m.Update(cmd())
	if m.pending || m.pdfPending || m.status != "PDF export cancelled" {
		t.Fatalf("cancellation state: pending=%v pdfPending=%v status=%q", m.pending, m.pdfPending, m.status)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cancelled export created output: %v", err)
	}
}
