package tui

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-runewidth"

	"github.com/ramjac/parchment/internal/printing"
)

type notePDFExportedMsg struct{ pdfExportResult }

func (m *Model) openNotePDFPrompt() tea.Cmd {
	m.pdfPrompt = promptPDF
	m.pdfInput.Prompt = "PDF output path: "
	m.resizeNotePDFPrompt()
	m.pdfInput.SetValue(pdfDefaultPath(m.path, m.lastPDFPath))
	m.pdfInput.CursorEnd()
	return m.pdfInput.Focus()
}

func (m *Model) closeNotePDFPrompt() {
	m.pdfPrompt = promptNone
	m.pdfInput.Blur()
	m.resizeNotePDFPrompt()
}

func (m *Model) resizeNotePDFPrompt() {
	promptRows := 0
	if m.pdfPrompt != promptNone {
		promptRows = 1
	}
	m.bodyInput.SetHeight(max(m.height-6-promptRows, 1))
	m.preview.SetHeight(max(m.height-6-promptRows, 1))
	m.pdfInput.SetWidth(max(m.width-4-runewidth.StringWidth(m.pdfInput.Prompt), 1))
}

func (m *Model) updateNotePDFPrompt(msg tea.KeyPressMsg) tea.Cmd {
	if m.pdfPrompt == promptPDFOverwrite {
		switch msg.String() {
		case "y", "Y":
			path := m.pdfOverwritePath
			m.pdfOverwritePath = ""
			m.closeNotePDFPrompt()
			return m.exportNotePDF(path, true)
		case "n", "N", "esc", "enter", "ctrl+c":
			m.pdfOverwritePath = ""
			m.closeNotePDFPrompt()
			m.status = "PDF export cancelled"
		}
		return nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeNotePDFPrompt()
		return nil
	case "enter":
		path := strings.TrimSpace(m.pdfInput.Value())
		m.closeNotePDFPrompt()
		return m.exportNotePDF(path, false)
	}
	var cmd tea.Cmd
	m.pdfInput, cmd = m.pdfInput.Update(msg)
	return cmd
}

func (m *Model) exportNotePDF(path string, overwrite bool) tea.Cmd {
	if path == "" {
		m.errMessage = "PDF output path is required"
		return nil
	}
	path, err := filepath.Abs(path)
	if err != nil {
		m.errMessage = "PDF output path: " + err.Error()
		return nil
	}
	if m.pdfStore == nil {
		m.errMessage = "PDF export storage is unavailable"
		return nil
	}
	n := m.snapshot
	n.Body = m.bodyInput.Value()
	d := printing.NoteDocument(n)
	ctx := m.startOperation()
	m.pending = true
	m.status, m.errMessage = "Exporting PDF…", ""
	store, source := m.pdfStore, m.path
	return func() tea.Msg {
		return notePDFExportedMsg{writePDF(ctx, store, path, source, d, overwrite)}
	}
}

func (m *Model) notePDFExported(msg notePDFExportedMsg) tea.Cmd {
	m.finishOperation()
	m.pending = false
	switch {
	case errors.Is(msg.err, context.Canceled):
		m.errMessage, m.status = "", "PDF export cancelled"
	case errors.Is(msg.err, fs.ErrExist):
		m.errMessage, m.status = "", ""
		m.pdfOverwritePath = msg.path
		m.pdfPrompt = promptPDFOverwrite
		m.pdfInput.Prompt = "PDF exists. Overwrite? [y/N] "
		m.pdfInput.SetValue("")
		m.resizeNotePDFPrompt()
		return m.pdfInput.Focus()
	case msg.err != nil:
		m.errMessage, m.status = "PDF export failed: "+msg.err.Error(), ""
	default:
		m.lastPDFPath = msg.path
		m.errMessage, m.status = "", "PDF exported to "+msg.path
		if msg.warning != nil {
			m.status += " (could not remember PDF path: " + msg.warning.Error() + ")"
		}
	}
	return nil
}
