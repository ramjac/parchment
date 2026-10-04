package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/spreadsheet"
)

const (
	spreadsheetColumnWidth = 12
	// spreadsheetChromeLines counts the title, formula bar, column header,
	// status, and help lines surrounding the grid.
	spreadsheetChromeLines = 5
)

type spreadsheetLoadedMsg struct {
	seq  int
	book spreadsheet.Spreadsheet
	err  error
}

type spreadsheetCellSavedMsg struct {
	seq    int
	book   spreadsheet.Spreadsheet
	row    int
	column int
	draft  string
	err    error
}

type spreadsheetHistoryMsg struct {
	seq         int
	description string
	err         error
}

// spreadsheetModel is a single-workbook grid editor. It owns only UI state;
// reads go through the repository and edits through the spreadsheet service.
type spreadsheetModel struct {
	service             *spreadsheet.Service
	repository          spreadsheet.Repository
	id                  string
	filePath            string
	book                spreadsheet.Spreadsheet
	loaded              bool
	sheet               int
	row, column         int
	rowOffset           int
	columnOffset        int
	width, height       int
	values              map[[2]int]string
	showFormulas        bool
	editing             bool
	input               textinput.Model
	pending             bool
	seq                 int
	status              string
	errMessage          string
	loadedStatus        string
	theme               theme
	newOperationContext func() (context.Context, context.CancelFunc)
	cancelOperation     context.CancelFunc
}

func newSpreadsheetModel(service *spreadsheet.Service, repository spreadsheet.Repository, id, filePath string) *spreadsheetModel {
	input := textinput.New()
	input.Prompt = ""
	input.CharLimit = spreadsheet.MaxFormulaLength + 1
	return &spreadsheetModel{
		service: service, repository: repository, id: id, filePath: filePath, input: input,
		newOperationContext: func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		},
		theme: theme{
			primary: lipgloss.AdaptiveColor{Light: "#4b3f72", Dark: "#c4b5fd"},
			border:  lipgloss.AdaptiveColor{Light: "#b8b4c7", Dark: "#55516a"},
		},
	}
}

// RunSpreadsheet opens one spreadsheet artifact in a full-screen grid editor.
// filePath is shown to the user only; the workbook is loaded by id through
// repository and edited through service.
func RunSpreadsheet(ctx context.Context, service *spreadsheet.Service, repository spreadsheet.Repository, id, filePath string) error {
	model := newSpreadsheetModel(service, repository, id, filePath)
	model.newOperationContext = func() (context.Context, context.CancelFunc) {
		return context.WithCancel(ctx)
	}
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx))
	_, err := program.Run()
	return err
}

func (m *spreadsheetModel) Init() tea.Cmd { return m.load() }

func (m *spreadsheetModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = max(1, m.width-12)
		m.scrollToSelection()
		return m, nil
	case spreadsheetLoadedMsg:
		if msg.seq != m.seq {
			return m, nil
		}
		m.finishOperation()
		if msg.err != nil {
			m.setError("Load failed", msg.err)
			return m, nil
		}
		m.setBook(msg.book)
		m.status, m.loadedStatus = m.loadedStatus, ""
		return m, nil
	case spreadsheetCellSavedMsg:
		if msg.seq != m.seq {
			return m, nil
		}
		m.finishOperation()
		if msg.err != nil {
			m.setError("Save failed", msg.err)
			m.row, m.column = msg.row, msg.column
			m.scrollToSelection()
			return m, m.beginEdit(msg.draft)
		}
		m.setBook(msg.book)
		m.status = "Saved " + spreadsheet.CellName(msg.row+1, msg.column+1)
		return m, nil
	case spreadsheetHistoryMsg:
		if msg.seq != m.seq {
			return m, nil
		}
		m.finishOperation()
		if msg.err != nil {
			m.setError("History failed", msg.err)
			return m, nil
		}
		cmd := m.load()
		m.loadedStatus = msg.description
		return m, cmd
	case tea.KeyMsg:
		if m.editing {
			return m, m.updateEditKey(msg)
		}
		return m, m.updateBrowseKey(msg)
	case tea.MouseMsg:
		if !m.pending && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if m.editing {
				if msg.Y == 1 {
					m.input.SetCursor(max(msg.X-len(spreadsheet.CellName(m.row+1, m.column+1))-3, 0))
				}
			} else if m.loaded && msg.Y >= 3 && msg.Y < 3+m.visibleRows() {
				column := (msg.X - m.rowHeaderWidth() - 1) / (spreadsheetColumnWidth + 1)
				if msg.X > m.rowHeaderWidth() && column >= 0 && column < m.visibleColumns() {
					m.row = min(m.rowOffset+msg.Y-3, spreadsheet.MaxRows-1)
					m.column = min(m.columnOffset+column, spreadsheet.MaxColumns-1)
					m.scrollToSelection()
				}
			}
		}
		return m, nil
	}
	if m.editing {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(message)
		return m, cmd
	}
	return m, nil
}

func (m *spreadsheetModel) updateEditKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.cancelPending()
		return tea.Quit
	case "esc":
		m.editing = false
		m.input.Blur()
		m.status, m.errMessage = "Edit cancelled", ""
		return nil
	case "enter":
		return m.save()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

func (m *spreadsheetModel) updateBrowseKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	switch key {
	case "ctrl+c", "q":
		m.cancelPending()
		return tea.Quit
	case "esc":
		if m.pending {
			m.cancelPending()
			m.status, m.errMessage = "Cancelled", ""
		}
		return nil
	}
	if m.pending {
		m.status = "Working… (esc cancels)"
		return nil
	}
	if key == "r" {
		return m.load()
	}
	if !m.loaded {
		return nil
	}
	page := max(1, m.visibleRows()-1)
	switch key {
	case "up", "k":
		m.move(-1, 0)
	case "down", "j":
		m.move(1, 0)
	case "left", "h":
		m.move(0, -1)
	case "right", "l":
		m.move(0, 1)
	case "pgup":
		m.move(-page, 0)
	case "pgdown":
		m.move(page, 0)
	case "home":
		m.column = 0
		m.scrollToSelection()
	case "end":
		m.column = max(0, len(m.currentRow())-1)
		m.scrollToSelection()
	case "tab", "shift+tab":
		if len(m.book.Sheets) > 1 {
			step := 1
			if key == "shift+tab" {
				step = len(m.book.Sheets) - 1
			}
			m.sheet = (m.sheet + step) % len(m.book.Sheets)
			m.row, m.column, m.rowOffset, m.columnOffset = 0, 0, 0, 0
			m.computeValues()
		}
	case "f":
		m.showFormulas = !m.showFormulas
	case "enter", "e":
		return m.beginEdit(cellDraft(m.cell(m.row, m.column)))
	case "delete", "backspace":
		if m.cell(m.row, m.column) != (spreadsheet.Cell{}) {
			return m.saveCell(m.row, m.column, spreadsheet.Cell{}, "")
		}
	case "u":
		return m.history("Undid", m.service.Undo)
	case "ctrl+r":
		return m.history("Redid", m.service.Redo)
	}
	return nil
}

func (m *spreadsheetModel) move(rows, columns int) {
	m.row = min(max(m.row+rows, 0), spreadsheet.MaxRows-1)
	m.column = min(max(m.column+columns, 0), spreadsheet.MaxColumns-1)
	m.scrollToSelection()
}

func (m *spreadsheetModel) beginEdit(draft string) tea.Cmd {
	m.editing = true
	m.input.SetValue(draft)
	m.input.CursorEnd()
	return m.input.Focus()
}

func (m *spreadsheetModel) save() tea.Cmd {
	draft := m.input.Value()
	cell, err := parseCellInput(draft)
	if err != nil {
		m.errMessage = err.Error()
		return nil
	}
	m.editing = false
	m.input.Blur()
	return m.saveCell(m.row, m.column, cell, draft)
}

func (m *spreadsheetModel) saveCell(row, column int, cell spreadsheet.Cell, draft string) tea.Cmd {
	ctx, seq := m.startOperation()
	m.status, m.errMessage = "Saving…", ""
	service, id, name := m.service, m.id, m.book.Sheets[m.sheet].Name
	return func() tea.Msg {
		book, err := service.SetCellInSheet(ctx, id, name, row+1, column+1, cell)
		return spreadsheetCellSavedMsg{seq: seq, book: book, row: row, column: column, draft: draft, err: err}
	}
}

func (m *spreadsheetModel) load() tea.Cmd {
	ctx, seq := m.startOperation()
	m.status, m.errMessage = "Loading…", ""
	repository, id := m.repository, m.id
	return func() tea.Msg {
		book, err := repository.GetSpreadsheet(ctx, id)
		return spreadsheetLoadedMsg{seq: seq, book: book, err: err}
	}
}

func (m *spreadsheetModel) history(prefix string, run func(context.Context) (string, error)) tea.Cmd {
	ctx, seq := m.startOperation()
	m.status, m.errMessage = "Working…", ""
	return func() tea.Msg {
		description, err := run(ctx)
		if err == nil {
			description = prefix + " " + strings.ToLower(description)
		}
		return spreadsheetHistoryMsg{seq: seq, description: description, err: err}
	}
}

func (m *spreadsheetModel) startOperation() (context.Context, int) {
	m.cancelPending()
	ctx, cancel := m.newOperationContext()
	m.cancelOperation = cancel
	m.pending = true
	m.seq++
	return ctx, m.seq
}

func (m *spreadsheetModel) finishOperation() {
	if m.cancelOperation != nil {
		m.cancelOperation()
		m.cancelOperation = nil
	}
	m.pending = false
}

// cancelPending abandons the current operation; its late result is ignored.
func (m *spreadsheetModel) cancelPending() {
	if m.pending {
		m.seq++
	}
	m.finishOperation()
}

func (m *spreadsheetModel) setError(prefix string, err error) {
	switch {
	case isCancelled(err) || errors.Is(err, context.Canceled):
		m.status, m.errMessage = "Cancelled", ""
	case errors.Is(err, spreadsheet.ErrNotFound):
		m.status, m.errMessage = "", prefix+": spreadsheet not found"
	default:
		m.status, m.errMessage = "", prefix+": "+err.Error()
	}
}

func (m *spreadsheetModel) setBook(book spreadsheet.Spreadsheet) {
	name := ""
	if m.loaded && m.sheet < len(m.book.Sheets) {
		name = m.book.Sheets[m.sheet].Name
	}
	m.book, m.loaded = book, true
	m.sheet = 0
	if index, err := spreadsheet.SheetIndex(book, name); err == nil && name != "" {
		m.sheet = index
	}
	m.status, m.errMessage = "", ""
	m.computeValues()
	m.scrollToSelection()
}

// computeValues evaluates formulas in the current sheet so views stay pure.
func (m *spreadsheetModel) computeValues() {
	m.values = make(map[[2]int]string)
	for row, cells := range m.book.Sheets[m.sheet].Rows {
		for column, cell := range cells {
			if cell.Formula == "" {
				continue
			}
			value, err := spreadsheet.Evaluate(&m.book, m.sheet, row+1, column+1)
			if err != nil {
				m.values[[2]int{row, column}] = "#ERR"
				continue
			}
			m.values[[2]int{row, column}] = strconv.FormatFloat(value, 'f', -1, 64)
		}
	}
}

func (m *spreadsheetModel) currentRow() []spreadsheet.Cell {
	rows := m.book.Sheets[m.sheet].Rows
	if m.row < len(rows) {
		return rows[m.row]
	}
	return nil
}

func (m *spreadsheetModel) cell(row, column int) spreadsheet.Cell {
	if !m.loaded {
		return spreadsheet.Cell{}
	}
	rows := m.book.Sheets[m.sheet].Rows
	if row < len(rows) && column < len(rows[row]) {
		return rows[row][column]
	}
	return spreadsheet.Cell{}
}

func (m *spreadsheetModel) displayValue(row, column int) string {
	cell := m.cell(row, column)
	if cell.Formula != "" {
		if m.showFormulas {
			return cell.Formula
		}
		return m.values[[2]int{row, column}]
	}
	return cell.Value
}

// cellDraft renders a cell for editing. Literals that would otherwise be read
// back as a formula or as an escaped literal are prefixed with an apostrophe.
func cellDraft(cell spreadsheet.Cell) string {
	if cell.Formula != "" {
		return cell.Formula
	}
	if strings.HasPrefix(cell.Value, "=") || strings.HasPrefix(cell.Value, "'") {
		return "'" + cell.Value
	}
	return cell.Value
}

// parseCellInput interprets edited text: a leading '=' makes a formula, a
// leading apostrophe forces the rest to be a literal, and anything else is a
// literal value. Empty input clears the cell.
func parseCellInput(text string) (spreadsheet.Cell, error) {
	switch {
	case strings.HasPrefix(text, "'"):
		return spreadsheet.Cell{Value: text[1:]}, nil
	case strings.HasPrefix(text, "="):
		if strings.TrimSpace(text[1:]) == "" {
			return spreadsheet.Cell{}, errors.New("formula is empty")
		}
		if len(text) > spreadsheet.MaxFormulaLength {
			return spreadsheet.Cell{}, fmt.Errorf("formula is longer than %d bytes", spreadsheet.MaxFormulaLength)
		}
		return spreadsheet.Cell{Formula: text}, nil
	}
	return spreadsheet.Cell{Value: text}, nil
}

func (m *spreadsheetModel) rowHeaderWidth() int {
	return len(strconv.Itoa(m.rowOffset+m.visibleRows())) + 1
}

func (m *spreadsheetModel) visibleRows() int {
	return max(1, m.height-spreadsheetChromeLines)
}

func (m *spreadsheetModel) visibleColumns() int {
	return max(1, (m.width-m.rowHeaderWidth())/(spreadsheetColumnWidth+1))
}

func (m *spreadsheetModel) scrollToSelection() {
	rows := m.visibleRows()
	if m.row < m.rowOffset {
		m.rowOffset = m.row
	} else if m.row >= m.rowOffset+rows {
		m.rowOffset = m.row - rows + 1
	}
	columns := m.visibleColumns()
	if m.column < m.columnOffset {
		m.columnOffset = m.column
	} else if m.column >= m.columnOffset+columns {
		m.columnOffset = m.column - columns + 1
	}
}

func columnName(column int) string {
	return strings.TrimSuffix(spreadsheet.CellName(1, column+1), "1")
}

func fitCell(value string, width int) string {
	value = runewidth.Truncate(sanitizeTerminalLine(value), width, "…")
	return runewidth.FillRight(value, width)
}

func (m *spreadsheetModel) View() string {
	if m.width < 20 || m.height < spreadsheetChromeLines+1 {
		return "Terminal too small. Press q to quit."
	}
	accent := lipgloss.NewStyle().Foreground(m.theme.primary).Bold(true)
	muted := lipgloss.NewStyle().Foreground(m.theme.border)
	selected := lipgloss.NewStyle().Reverse(true)
	var b strings.Builder

	title := "Spreadsheet"
	if m.loaded {
		title = m.book.Title
		if len(m.book.Sheets) > 1 || m.book.Sheets[m.sheet].Name != "Sheet1" {
			title += " — " + m.book.Sheets[m.sheet].Name
		}
	}
	header := sanitizeTerminalLine(title)
	if m.filePath != "" {
		header += "  " + sanitizeTerminalLine(m.filePath)
	}
	b.WriteString(accent.Render(runewidth.Truncate(header, m.width, "…")) + "\n")

	reference := spreadsheet.CellName(m.row+1, m.column+1)
	if m.editing {
		b.WriteString(reference + " > " + m.input.View() + "\n")
	} else {
		b.WriteString(runewidth.Truncate(reference+" │ "+sanitizeTerminalLine(cellDraft(m.cell(m.row, m.column))), m.width, "…") + "\n")
	}

	rowWidth := m.rowHeaderWidth()
	columns := m.visibleColumns()
	line := strings.Repeat(" ", rowWidth)
	for c := m.columnOffset; c < m.columnOffset+columns && c < spreadsheet.MaxColumns; c++ {
		line += " " + fitCell(columnName(c), spreadsheetColumnWidth)
	}
	b.WriteString(muted.Render(line) + "\n")

	for r := m.rowOffset; r < m.rowOffset+m.visibleRows(); r++ {
		if r >= spreadsheet.MaxRows {
			b.WriteString("\n")
			continue
		}
		b.WriteString(muted.Render(fmt.Sprintf("%*d", rowWidth, r+1)))
		for c := m.columnOffset; c < m.columnOffset+columns && c < spreadsheet.MaxColumns; c++ {
			text := fitCell(m.displayValue(r, c), spreadsheetColumnWidth)
			if r == m.row && c == m.column {
				text = selected.Render(text)
			}
			b.WriteString(" " + text)
		}
		b.WriteString("\n")
	}

	status := m.status
	if m.errMessage != "" {
		status = "Error: " + m.errMessage
	} else if status == "" && m.loaded {
		status = fmt.Sprintf("%d×%d", len(m.book.Sheets[m.sheet].Rows), len(m.book.Sheets[m.sheet].Rows[0]))
	}
	b.WriteString(runewidth.Truncate(sanitizeTerminalLine(status), m.width, "…") + "\n")
	help := "arrows move • enter edit • del clear • f formulas • tab sheet • u undo • ctrl+r redo • r reload • q quit"
	if m.editing {
		help = "enter save • esc cancel • =formula • 'literal"
	} else if !m.loaded {
		help = "r retry • q quit"
	}
	b.WriteString(muted.Render(runewidth.Truncate(help, m.width, "…")))
	return b.String()
}
