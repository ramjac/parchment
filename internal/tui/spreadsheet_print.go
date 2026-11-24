package tui

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-runewidth"

	"github.com/ramjac/parchment/internal/spreadsheet"
)

type spreadsheetPDFExportedMsg struct {
	seq int
	pdfExportResult
}

func (m *spreadsheetModel) resizePDFPrompt() {
	m.pdfInput.SetWidth(max(m.width-4-runewidth.StringWidth(m.pdfInput.Prompt), 1))
}

func (m *spreadsheetModel) openPDFPrompt() tea.Cmd {
	m.pdfPrompt = promptPDF
	m.pdfInput.Prompt = "PDF output path: "
	m.resizePDFPrompt()
	m.pdfInput.SetValue(pdfDefaultPath(m.path, m.lastPDFPath))
	m.pdfInput.CursorEnd()
	m.input.Blur()
	return m.pdfInput.Focus()
}

func (m *spreadsheetModel) closePDFPrompt() tea.Cmd {
	m.pdfPrompt = promptNone
	m.pdfInput.Blur()
	m.scrollToSelection()
	if m.editing {
		return m.input.Focus()
	}
	return nil
}

func (m *spreadsheetModel) updatePDFPrompt(msg tea.KeyPressMsg) tea.Cmd {
	if m.pdfPrompt == promptPDFOverwrite {
		switch msg.String() {
		case "y", "Y":
			path := m.pdfOverwritePath
			m.pdfOverwritePath = ""
			return tea.Batch(m.closePDFPrompt(), m.exportPDF(path, true))
		case "n", "N", "esc", "enter", "ctrl+c":
			m.pdfOverwritePath = ""
			m.status = "PDF export cancelled"
			return m.closePDFPrompt()
		}
		return nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		return m.closePDFPrompt()
	case "enter":
		path := strings.TrimSpace(m.pdfInput.Value())
		return tea.Batch(m.closePDFPrompt(), m.exportPDF(path, false))
	}
	var cmd tea.Cmd
	m.pdfInput, cmd = m.pdfInput.Update(msg)
	return cmd
}

func (m *spreadsheetModel) exportPDF(path string, overwrite bool) tea.Cmd {
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
	book := cloneSpreadsheetState(m.book)
	if m.editing {
		cell, err := parseCellInput(m.input.Value())
		if err == nil {
			err = spreadsheet.SetCellInBook(&book, spreadsheet.SetCellOptions{
				Sheet: book.Sheets[m.sheet].Name, Row: m.row + 1, Column: m.column + 1, Cell: cell,
			})
		}
		if err != nil {
			m.errMessage = "PDF export failed: " + err.Error()
			return nil
		}
	}
	m.stopAutosave()
	ctx, seq := m.startOperation()
	m.pdfPending = true
	m.status, m.errMessage = "Exporting PDF…", ""
	store, source := m.pdfStore, m.path
	return func() tea.Msg {
		return spreadsheetPDFExportedMsg{seq, writeSpreadsheetPDF(ctx, store, path, source, book, overwrite)}
	}
}

func (m *spreadsheetModel) spreadsheetPDFExported(msg spreadsheetPDFExportedMsg) tea.Cmd {
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
		m.input.Blur()
		m.resizePDFPrompt()
		return tea.Batch(m.pdfInput.Focus(), m.syncAutosave())
	case msg.err != nil:
		m.errMessage, m.status = "PDF export failed: "+msg.err.Error(), ""
	default:
		m.lastPDFPath = msg.path
		m.errMessage, m.status = "", "PDF exported to "+msg.path
		if msg.warning != nil {
			m.status += " (could not remember PDF path: " + msg.warning.Error() + ")"
		}
	}
	return m.syncAutosave()
}
