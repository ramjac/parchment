package tui

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"example.com/parchment/internal/spreadsheet"
)

const maxSpreadsheetClipboardBytes = 8 << 20

type spreadsheetClipboardWriteMsg struct{ err error }

type spreadsheetClipboardReadMsg struct {
	seq    int
	text   string
	err    error
	sheet  string
	row    int
	column int
}

func readSystemClipboard() (string, error)   { return clipboard.ReadAll() }
func writeSystemClipboard(text string) error { return clipboard.WriteAll(text) }

func (m *spreadsheetModel) copySelection() tea.Cmd {
	firstRow, lastRow, firstColumn, lastColumn := m.row, m.row, m.column, m.column
	if m.selecting {
		firstRow, lastRow = min(m.row, m.selectionRow), max(m.row, m.selectionRow)
		firstColumn, lastColumn = min(m.column, m.selectionColumn), max(m.column, m.selectionColumn)
	}
	copy, err := spreadsheet.CopyRange(m.book, m.book.Sheets[m.sheet].Name, firstRow+1, firstColumn+1, lastRow+1, lastColumn+1)
	if err != nil {
		m.setError("Copy failed", err)
		return nil
	}
	m.clipboard = &copy
	m.selecting = false
	m.errMessage, m.status = "", "Copied range"
	systemCopy := copy
	systemCopy.Cells = make([][]spreadsheet.Cell, len(copy.Cells))
	spills, err := spreadsheet.NewEvaluator(&m.book).SpillCells(m.sheet)
	if err != nil {
		m.status = "Copied internally; system clipboard unavailable"
		return nil
	}
	for row := range copy.Cells {
		systemCopy.Cells[row] = append([]spreadsheet.Cell(nil), copy.Cells[row]...)
		for col := range systemCopy.Cells[row] {
			if value, ok := spills[[2]int{copy.Row + row, copy.Column + col}]; ok {
				systemCopy.Cells[row][col] = spreadsheet.Cell{Value: value.Display()}
			}
		}
	}
	text, err := encodeSpreadsheetClipboard(systemCopy)
	if err != nil {
		m.status = "Copied internally; system clipboard text is too large"
		return nil
	}
	write := m.clipboardWrite
	return func() tea.Msg { return spreadsheetClipboardWriteMsg{err: write(text)} }
}

func (m *spreadsheetModel) pasteInternal() tea.Cmd {
	if m.clipboard == nil {
		m.errMessage = "Internal clipboard is empty"
		return nil
	}
	name := m.book.Sheets[m.sheet].Name
	copy := *m.clipboard
	if m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
		return spreadsheet.PasteRange(book, name, m.row+1, m.column+1, copy)
	}) {
		m.selecting = false
		m.status = "Pasted range"
		return m.syncAutosave()
	}
	return nil
}

func (m *spreadsheetModel) pasteSystemClipboard() tea.Cmd {
	ctx, seq := m.startOperation()
	read, sheet, row, column := m.clipboardRead, m.book.Sheets[m.sheet].Name, m.row+1, m.column+1
	m.status = "Reading clipboard..."
	return func() tea.Msg {
		text, err := read()
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return spreadsheetClipboardReadMsg{seq: seq, text: text, err: err, sheet: sheet, row: row, column: column}
	}
}

func (m *spreadsheetModel) spreadsheetClipboardRead(msg spreadsheetClipboardReadMsg) tea.Cmd {
	if msg.seq != m.seq {
		return nil
	}
	m.finishOperation()
	if msg.err != nil {
		m.setError("Clipboard read failed", msg.err)
		return m.syncAutosave()
	}
	cells, err := decodeSpreadsheetClipboard(msg.text)
	if err != nil {
		m.setError("Clipboard paste failed", err)
		return m.syncAutosave()
	}
	copy := spreadsheet.CellClipboard{Row: msg.row, Column: msg.column, Cells: cells}
	if m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
		return spreadsheet.PasteRange(book, msg.sheet, msg.row, msg.column, copy)
	}) {
		m.selecting = false
		m.status = "Pasted clipboard text"
	}
	return m.syncAutosave()
}

func encodeSpreadsheetClipboard(copied spreadsheet.CellClipboard) (string, error) {
	var out strings.Builder
	writer := csv.NewWriter(&out)
	writer.Comma = '\t'
	for _, row := range copied.Cells {
		record := make([]string, len(row))
		for i, cell := range row {
			record[i] = cell.Value
			if cell.Formula != "" {
				record[i] = cell.Formula
			}
		}
		if len(record) == 1 && record[0] == "" {
			writer.Flush()
			if err := writer.Error(); err != nil {
				return "", err
			}
			out.WriteString("\"\"\n")
			continue
		}
		if err := writer.Write(record); err != nil {
			return "", err
		}
		if out.Len() > maxSpreadsheetClipboardBytes {
			return "", errors.New("clipboard text exceeds size limit")
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", err
	}
	if out.Len() > maxSpreadsheetClipboardBytes {
		return "", errors.New("clipboard text exceeds size limit")
	}
	return out.String(), nil
}

func decodeSpreadsheetClipboard(text string) ([][]spreadsheet.Cell, error) {
	if len(text) > maxSpreadsheetClipboardBytes {
		return nil, errors.New("clipboard text exceeds size limit")
	}
	reader := csv.NewReader(strings.NewReader(preserveClipboardBlankRows(text)))
	reader.Comma, reader.FieldsPerRecord = '\t', -1
	var rows [][]spreadsheet.Cell
	width := 0
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid tabular clipboard text: %w", err)
		}
		if len(rows) >= spreadsheet.MaxRows || len(record) > spreadsheet.MaxColumns {
			return nil, errors.New("clipboard range exceeds workbook dimensions")
		}
		row := make([]spreadsheet.Cell, len(record))
		for i, value := range record {
			row[i] = spreadsheet.Cell{Value: value}
		}
		rows = append(rows, row)
		width = max(width, len(row))
	}
	if len(rows) == 0 {
		return [][]spreadsheet.Cell{{{Value: ""}}}, nil
	}
	for i := range rows {
		rows[i] = append(rows[i], make([]spreadsheet.Cell, width-len(rows[i]))...)
	}
	return rows, nil
}

func preserveClipboardBlankRows(text string) string {
	var out strings.Builder
	inQuotes, fieldStart, rowStart := false, true, true
	for i := 0; i < len(text); i++ {
		c := text[i]
		if !inQuotes && rowStart && c == '\r' && i+1 < len(text) && text[i+1] == '\n' {
			out.WriteString("\"\"\r\n")
			i++
			continue
		}
		if !inQuotes && rowStart && c == '\n' {
			out.WriteString("\"\"")
		}
		if c == '"' {
			if inQuotes && i+1 < len(text) && text[i+1] == '"' {
				out.WriteString("\"\"")
				i++
				continue
			}
			if inQuotes {
				inQuotes = false
			} else if fieldStart {
				inQuotes = true
			}
		}
		out.WriteByte(c)
		if inQuotes {
			fieldStart, rowStart = false, false
		} else if c == '\n' {
			fieldStart, rowStart = true, true
		} else if c == '\t' {
			fieldStart, rowStart = true, false
		} else if c != '\r' {
			fieldStart, rowStart = false, false
		}
	}
	return out.String()
}
