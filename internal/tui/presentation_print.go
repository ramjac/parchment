package tui

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

type presentationPDFExportedMsg struct {
	seq int
	pdfExportResult
}

func (m *presentationModel) openPDFPrompt() tea.Cmd {
	m.pdfPrompt = promptPDF
	m.updatePDFInputPrompt()
	m.resizePDFPrompt()
	m.pdfInput.SetValue(pdfDefaultPath(m.path, m.lastPDFPath))
	m.pdfInput.CursorEnd()
	return m.pdfInput.Focus()
}

func (m *presentationModel) updatePDFInputPrompt() {
	layout := "1 slide, landscape"
	if m.pdfTwoUp {
		layout = "2 slides, portrait"
	}
	m.pdfInput.Prompt = "PDF output path: [" + layout + "; Tab toggles] "
}

func (m *presentationModel) resizePDFPrompt() {
	rows := 0
	if m.pdfPrompt != promptNone {
		rows = 1
	}
	m.editor.SetHeight(max(1, m.height-5-rows))
	m.pdfInput.Width = max(m.width-4-runewidth.StringWidth(m.pdfInput.Prompt), 1)
}

func (m *presentationModel) closePDFPrompt() {
	m.pdfPrompt = promptNone
	m.pdfInput.Blur()
	m.resizePDFPrompt()
}

func (m *presentationModel) updatePDFPrompt(msg tea.KeyMsg) tea.Cmd {
	if m.pdfPrompt == promptPDFOverwrite {
		switch msg.String() {
		case "y", "Y":
			path := m.pdfOverwritePath
			m.pdfOverwritePath = ""
			m.closePDFPrompt()
			return m.exportPDF(path, true)
		case "n", "N", "esc", "enter", "ctrl+c":
			m.pdfOverwritePath = ""
			m.closePDFPrompt()
			m.status = "PDF export cancelled"
		}
		return nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closePDFPrompt()
		return nil
	case "tab":
		m.pdfTwoUp = !m.pdfTwoUp
		m.updatePDFInputPrompt()
		m.resizePDFPrompt()
		return nil
	case "enter":
		path := strings.TrimSpace(m.pdfInput.Value())
		m.closePDFPrompt()
		return m.exportPDF(path, false)
	}
	var cmd tea.Cmd
	m.pdfInput, cmd = m.pdfInput.Update(msg)
	return cmd
}

func (m *presentationModel) exportPDF(path string, overwrite bool) tea.Cmd {
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
	item := m.item
	if m.editing {
		item.Source = m.editor.Value()
	}
	ctx, seq := m.startOperation()
	m.pdfPending = true
	m.status, m.errMessage = "Exporting PDF…", ""
	store, source, twoUp := m.pdfStore, m.path, m.pdfTwoUp
	return func() tea.Msg {
		return presentationPDFExportedMsg{seq, writePresentationPDF(ctx, store, path, source, item, overwrite, twoUp)}
	}
}

func (m *presentationModel) presentationPDFExported(msg presentationPDFExportedMsg) tea.Cmd {
	if msg.seq != m.seq {
		return nil
	}
	m.finishOperation()
	m.pdfPending = false
	switch {
	case errors.Is(msg.err, context.Canceled):
		m.errMessage, m.status = "", "PDF export cancelled"
	case errors.Is(msg.err, fs.ErrExist):
		m.errMessage, m.status = "", ""
		m.pdfOverwritePath = msg.path
		m.pdfPrompt = promptPDFOverwrite
		m.pdfInput.Prompt = "PDF exists. Overwrite? [y/N] "
		m.pdfInput.SetValue("")
		m.resizePDFPrompt()
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
