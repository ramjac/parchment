package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/recovery"
	"example.com/parchment/internal/spreadsheet"
)

const (
	spreadsheetColumnWidth = 12
	// spreadsheetChromeLines counts the title, formula bar, top/bottom border, column header,
	// status, and help lines surrounding the grid.
	spreadsheetChromeLines = 7
)

type spreadsheetLoadedMsg struct {
	seq  int
	book spreadsheet.Spreadsheet
	err  error
}

type spreadsheetHistoryMsg struct {
	seq         int
	description string
	err         error
}

type spreadsheetSavedMsg struct {
	seq        int
	book       spreadsheet.Spreadsheet
	err        error
	cleanupErr error
}

type spreadsheetCloseFinishedMsg struct {
	seq int
	err error
}

type spreadsheetAutosaveTickMsg struct{ session uint64 }

type spreadsheetAutosaveFinishedMsg struct {
	session uint64
	err     error
	cleared bool
}

type spreadsheetEmbeddedFinishedMsg struct {
	workbook         spreadsheet.Workbook
	committed        bool
	chartReferenceID string
}

type spreadsheetSearchMatch struct {
	sheet, row, column int
}

// spreadsheetGrid holds viewport navigation, selection, and value display state.
type spreadsheetGrid struct {
	sheet         int
	row, column   int
	rowOffset     int
	columnOffset  int
	width, height int
	values        map[[2]int]string
	showFormulas  bool
	find          findBar
	searchMatches []spreadsheetSearchMatch
	searchMatch   int
	gridClicks    mouseClickTracker
}

// spreadsheetAsync holds status, error messages, and async operation tracking.
type spreadsheetAsync struct {
	pending             bool
	seq                 int
	status              string
	errMessage          string
	pendingShortcut     string
	loadedStatus        string
	newOperationContext func() (context.Context, context.CancelFunc)
	cancelOperation     context.CancelFunc
}

// spreadsheetModel is a single-workbook grid editor. It owns only UI state;
// it reads and edits the workbook file through the spreadsheet service.
type spreadsheetModel struct {
	service          *spreadsheet.Service
	theme            Theme
	path             string
	embedded         bool
	book             spreadsheet.Spreadsheet
	baseline         spreadsheet.Spreadsheet
	loaded           bool
	editing          bool
	input            textinput.Model
	pdfInput         textinput.Model
	pdfStore         PDFExportStore
	lastPDFPath      string
	pdfPrompt        promptKind
	pdfOverwritePath string
	pdfPending       bool
	clipboard        *spreadsheet.CellClipboard
	clipboardRead    func() (string, error)
	clipboardWrite   func(string) error
	selecting        bool
	selectionRow     int
	selectionColumn  int
	charts           spreadsheetChartUI
	gridChartSeq     uint64
	gridChartDirty   bool
	gridCharts       map[[2]int][]string
	gridChartInfo    map[[2]int]string

	undoLimit    int
	bufferUndo   []spreadsheet.Spreadsheet
	bufferRedo   []spreadsheet.Spreadsheet
	closeWarning bool

	recoveryStore          recovery.Store
	autosaveSession        uint64
	autosaveCancel         context.CancelFunc
	autosaveActive         bool
	recoveryCleanupPending bool
	closing                bool
	draftStored            bool
	autosaveScheduler      func(uint64) tea.Cmd

	spreadsheetGrid
	spreadsheetAsync
}

func newSpreadsheetModel(service *spreadsheet.Service, path string, theme Theme, undoLimit int) *spreadsheetModel {
	input := textinput.New()
	input.Prompt = ""
	input.CharLimit = spreadsheet.MaxFormulaLength + 1
	pdfInput := textinput.New()
	pdfInput.CharLimit = 64 << 10
	if undoLimit < 1 {
		undoLimit = 100
	}
	m := &spreadsheetModel{
		service:        service,
		path:           path,
		theme:          theme,
		input:          input,
		undoLimit:      undoLimit,
		pdfInput:       pdfInput,
		clipboardRead:  readSystemClipboard,
		clipboardWrite: writeSystemClipboard,
		spreadsheetGrid: spreadsheetGrid{
			find:        newFindBar(theme),
			searchMatch: -1,
		},
	}
	m.newOperationContext = func() (context.Context, context.CancelFunc) {
		return context.WithCancel(context.Background())
	}
	return m
}

func newEmbeddedSpreadsheetModel(book spreadsheet.Workbook, activeSheet int, theme Theme, undoLimit int) *spreadsheetModel {
	m := newSpreadsheetModel(nil, "", theme, undoLimit)
	m.embedded = true
	book = book.Clone()
	m.book = spreadsheet.Spreadsheet{
		Version: spreadsheet.FileVersion,
		Sheets:  book.Sheets,
		Charts:  append([]spreadsheet.Chart(nil), book.Charts...),
	}
	m.baseline = cloneSpreadsheetState(m.book)
	m.loaded = true
	if len(m.book.Sheets) > 0 {
		m.sheet = min(max(activeSheet, 0), len(m.book.Sheets)-1)
	}
	m.computeValues()
	m.normalizeSpanCursor()
	return m
}

func (m *spreadsheetModel) startAutosaveSession() {
	m.autosaveSession++
	m.autosaveActive = false
	if m.autosaveCancel != nil {
		m.autosaveCancel()
		m.autosaveCancel = nil
	}
}

func (m *spreadsheetModel) scheduleAutosave(session uint64) tea.Cmd {
	if m.recoveryStore == nil || m.autosaveScheduler == nil ||
		session != m.autosaveSession || !m.autosaveActive || m.pending || m.closing || !m.dirty() {
		return nil
	}
	return m.autosaveScheduler(session)
}

func (m *spreadsheetModel) stopAutosave() { m.startAutosaveSession() }

func (m *spreadsheetModel) syncAutosave() tea.Cmd {
	if m.recoveryStore == nil || m.pending || m.closing {
		return nil
	}
	if !m.dirty() {
		m.stopAutosave()
		if !m.draftStored || m.recoveryCleanupPending {
			return nil
		}
		return m.deleteRecoveryDraft()
	}
	if m.recoveryCleanupPending || m.autosaveActive {
		return nil
	}
	m.startAutosaveSession()
	m.autosaveActive = true
	return m.scheduleAutosave(m.autosaveSession)
}

func (m *spreadsheetModel) saveRecovery(session uint64) tea.Cmd {
	if m.recoveryStore == nil || session != m.autosaveSession || !m.autosaveActive ||
		m.pending || m.closing || !m.dirty() {
		return nil
	}
	pendingInput := ""
	if m.editing {
		pendingInput = m.input.Value()
	}
	baseline, dirty := cloneSpreadsheetState(m.baseline), cloneSpreadsheetState(m.book)
	data := spreadsheetRecoveryData{
		Baseline:         baseline,
		BaselineBody:     m.baseline.Body,
		BaselineBlocks:   baseline.Blocks,
		Dirty:            dirty,
		DirtyBody:        m.book.Body,
		DirtyBlocks:      dirty.Blocks,
		ActiveSheet:      m.sheet,
		CursorRow:        m.row,
		CursorCol:        m.column,
		Editing:          m.editing,
		PendingCellInput: pendingInput,
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.autosaveCancel = cancel
	store := m.recoveryStore
	draft := recovery.Draft{
		Path: m.path, Kind: string(artifact.SpreadsheetKind), UpdatedAt: time.Now().UTC(),
	}
	m.draftStored = true
	return func() tea.Msg {
		encoded, err := json.Marshal(data)
		if err == nil {
			draft.Data = encoded
			err = store.SaveRecovery(ctx, draft)
		}
		return spreadsheetAutosaveFinishedMsg{session: session, err: err}
	}
}

func (m *spreadsheetModel) deleteRecoveryDraft() tea.Cmd {
	m.recoveryCleanupPending = true
	session := m.autosaveSession
	store, path := m.recoveryStore, m.path
	return func() tea.Msg {
		return spreadsheetAutosaveFinishedMsg{
			session: session, err: store.DeleteRecovery(context.Background(), path), cleared: true,
		}
	}
}

func (m *spreadsheetModel) Init() tea.Cmd {
	if m.embedded {
		return nil
	}
	return m.load()
}

func (m *spreadsheetModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(message)
	if m.gridChartDirty && m.loaded {
		m.gridChartDirty = false
		if len(m.book.Charts) > 0 {
			return model, tea.Batch(cmd, m.prepareGridCharts())
		}
	}
	return model, cmd
}

func (m *spreadsheetModel) update(message tea.Msg) (tea.Model, tea.Cmd) {
	message = normalizeTerminalKey(message)
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(1, m.width-12))
		m.resizePDFPrompt()
		m.scrollToSelection()
		if m.charts.mode == chartForm {
			m.resizeChartForm()
			return m, m.renderChartFormPreview()
		}
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
	case spreadsheetSavedMsg:
		if msg.seq != m.seq {
			return m, nil
		}
		m.finishOperation()
		if msg.err != nil {
			m.setError("Save failed", msg.err)
			return m, m.syncAutosave()
		}
		selecting, selectionRow, selectionColumn := m.selecting, m.selectionRow, m.selectionColumn
		m.setBook(msg.book)
		m.selecting, m.selectionRow, m.selectionColumn = selecting, selectionRow, selectionColumn
		m.status = "Saved spreadsheet"
		m.draftStored = msg.cleanupErr != nil
		if msg.cleanupErr != nil {
			m.status += " (could not remove recovery draft: " + msg.cleanupErr.Error() + ")"
		}
		return m, nil
	case spreadsheetCloseFinishedMsg:
		if msg.seq != m.seq {
			return m, nil
		}
		m.finishOperation()
		if msg.err != nil {
			m.closing = false
			m.setError("Discard autosaved draft", msg.err)
			if m.dirty() {
				return m, m.syncAutosave()
			}
			return m, nil
		}
		m.draftStored = false
		return m, tea.Quit
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
	case spreadsheetPDFExportedMsg:
		return m, m.spreadsheetPDFExported(msg)
	case spreadsheetChartFormPreviewMsg:
		m.chartFormPreviewed(msg)
		return m, nil
	case spreadsheetChartFormPreviewRequest:
		return m, m.prepareChartFormPreview(msg)
	case spreadsheetGridChartsMsg:
		if msg.seq == m.gridChartSeq {
			m.gridCharts = msg.charts
			m.gridChartInfo = msg.info
		}
		return m, nil
	case spreadsheetClipboardWriteMsg:
		if msg.err != nil {
			m.status = "Copied internally; system clipboard unavailable"
		}
		return m, nil
	case spreadsheetClipboardReadMsg:
		return m, m.spreadsheetClipboardRead(msg)
	case spreadsheetAutosaveTickMsg:
		if msg.session == m.autosaveSession && m.autosaveActive && m.dirty() {
			return m, m.saveRecovery(msg.session)
		}
		return m, nil
	case spreadsheetAutosaveFinishedMsg:
		if msg.cleared {
			m.recoveryCleanupPending = false
			if msg.err == nil {
				m.draftStored = false
			} else {
				m.errMessage = "Discard autosaved draft: " + msg.err.Error()
			}
			if m.dirty() {
				return m, m.syncAutosave()
			}
			return m, nil
		}
		if msg.session != m.autosaveSession {
			return m, nil
		}
		if m.autosaveCancel != nil {
			m.autosaveCancel()
			m.autosaveCancel = nil
		}
		if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
			m.errMessage = "Autosave failed: " + msg.err.Error()
		}
		if m.autosaveActive && m.dirty() {
			return m, m.scheduleAutosave(msg.session)
		}
		return m, m.syncAutosave()
	case shiftEnterMsg:
		return m, m.updateEditKey(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	case tea.KeyPressMsg:
		m.gridClicks.reset()
		if m.charts.mode != chartClosed {
			return m, m.updateCharts(msg)
		}
		if m.pdfPrompt != promptNone {
			return m, m.updatePDFPrompt(msg)
		}
		if m.find.active {
			return m, m.updateFindKey(msg)
		}
		if m.editing {
			return m, m.updateEditKey(msg)
		}
		return m, m.updateBrowseKey(msg)
	case mouseEvent:
		if m.charts.mode == chartClosed && !m.pending && m.pdfPrompt == promptNone && msg.Action == mousePress && msg.Button == mouseLeft {
			if m.editing {
				if msg.Y == 1 {
					m.input.SetCursor(max(msg.X-len(spreadsheet.CellName(m.row+1, m.column+1))-3, 0))
				}
			} else if m.loaded && msg.Y >= 4 && msg.Y < 4+m.visibleRows() {
				gridX := msg.X - 2
				column := (gridX - m.rowHeaderWidth() - 1) / (spreadsheetColumnWidth + 1)
				if gridX > m.rowHeaderWidth() && column >= 0 && column < m.visibleColumns() {
					m.row = min(m.rowOffset+msg.Y-4, spreadsheet.MaxRows-1)
					m.column = min(m.columnOffset+column, spreadsheet.MaxColumns-1)
					m.normalizeSpanCursor()
					m.scrollToSelection()
					if !m.selecting && m.gridClicks.click(m.row, m.column, time.Now()) {
						return m, m.beginEdit(cellDraft(m.cell(m.row, m.column)))
					}
				} else {
					m.gridClicks.reset()
				}
			} else {
				m.gridClicks.reset()
			}
		} else if msg.Action == mousePress {
			m.gridClicks.reset()
		}
		return m, nil
	}
	if m.find.active && m.pdfPrompt == promptNone {
		return m, m.updateFindKey(message)
	}
	if m.editing {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(message)
		return m, cmd
	}
	return m, nil
}

func (m *spreadsheetModel) updateEditKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if key == "ctrl+c" {
		if m.embedded {
			return m.requestEmbeddedClose()
		}
		m.cancelPending()
		return tea.Quit
	}
	if m.pending {
		if key == "esc" {
			m.cancelPending()
			m.closing = false
			if m.pdfPending {
				m.pdfPending = false
				m.status, m.errMessage = "PDF export cancelled", ""
			} else {
				m.status, m.errMessage = "Save cancelled", ""
			}
			return m.syncAutosave()
		} else {
			if m.pdfPending {
				m.status = "Exporting PDF… (esc cancels)"
			} else {
				m.status = "Saving… (esc cancels)"
			}
		}
		return nil
	}
	switch key {
	case "esc":
		m.editing = false
		m.input.Blur()
		m.status, m.errMessage = "Cell edit cancelled", ""
		return m.syncAutosave()
	case "enter", "shift+enter", "alt+enter":
		if m.commitCell() {
			if key != "enter" {
				m.move(1, 0)
				return tea.Batch(m.beginEdit(cellDraft(m.cell(m.row, m.column))), m.syncAutosave())
			}
			return m.syncAutosave()
		}
		return nil
	case "ctrl+s":
		if !m.commitCell() {
			return nil
		}
		if m.embedded {
			return m.finishEmbedded(true)
		}
		return m.saveWorkbook()
	case "ctrl+p":
		if m.embedded {
			return nil
		}
		return m.openPDFPrompt()
	}
	m.closeWarning = false
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return tea.Batch(cmd, m.syncAutosave())
}

func (m *spreadsheetModel) updateBrowseKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if key != "esc" && key != "q" && key != "ctrl+c" {
		m.closeWarning = false
	}
	switch key {
	case "ctrl+c":
		if m.embedded {
			return m.requestEmbeddedClose()
		}
		m.cancelPending()
		return tea.Quit
	case "esc":
		if m.embedded {
			return m.requestEmbeddedClose()
		}
		if m.pendingShortcut != "" {
			m.pendingShortcut = ""
			m.status = ""
			return nil
		}
		if m.pending {
			m.cancelPending()
			m.closing = false
			if m.pdfPending {
				m.pdfPending = false
				m.status, m.errMessage = "PDF export cancelled", ""
			} else {
				m.status, m.errMessage = "Cancelled", ""
			}
			return m.syncAutosave()
		}
		if m.selecting {
			m.selecting = false
			m.status = "Range selection cancelled"
			return nil
		}
		if m.find.visible {
			return m.updateFindKey(msg)
		}
		return m.requestClose()
	case "q":
		if m.pending {
			m.status = "Working… (esc cancels)"
			return nil
		}
		if m.embedded {
			return m.requestEmbeddedClose()
		}
		return m.requestClose()
	}
	if m.pending {
		m.status = "Working… (esc cancels)"
		return nil
	}

	if key == "r" && m.pendingShortcut == "" {
		if m.dirty() {
			m.status = "Unsaved changes: Ctrl+S saves before reloading"
			return nil
		}
		return m.load()
	}
	if !m.loaded {
		return nil
	}
	if m.embedded && key == "ctrl+shift+r" {
		return m.finishEmbeddedChartReference()
	}
	if m.find.visible {
		switch key {
		case "n":
			m.moveSearchMatch(1)
			return nil
		case "N":
			m.moveSearchMatch(-1)
			return nil
		}
	}
	if key == "/" || key == "ctrl+f" {
		m.searchSpreadsheet(m.find.query())
		return m.find.open(m.width)
	}

	if m.pendingShortcut == "d" {
		m.pendingShortcut = ""
		name := m.book.Sheets[m.sheet].Name
		switch key {
		case "r":
			if m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
				return spreadsheet.DeleteRowFromBook(book, name, m.row+1)
			}) {
				m.status = "Row deleted"
				return m.syncAutosave()
			}
		case "c":
			if m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
				return spreadsheet.DeleteColumnFromBook(book, name, m.column+1)
			}) {
				m.status = "Column deleted"
				return m.syncAutosave()
			}
		case "s":
			if m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
				return spreadsheet.DeleteSheetFromBook(book, name)
			}) {
				m.status = "Sheet deleted"
				return m.syncAutosave()
			}
		default:
			m.status, m.errMessage = "Cancelled", ""
		}
		return nil
	}
	if m.pendingShortcut == "i" {
		m.pendingShortcut = ""
		name := m.book.Sheets[m.sheet].Name
		switch key {
		case "r":
			if m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
				return spreadsheet.InsertRowInBook(book, name, m.row+1)
			}) {
				m.status = "Row inserted"
				return m.syncAutosave()
			}
		case "c":
			if m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
				return spreadsheet.InsertColumnInBook(book, name, m.column+1)
			}) {
				m.status = "Column inserted"
				return m.syncAutosave()
			}
		default:
			m.status, m.errMessage = "Cancelled", ""
		}
		return nil
	}
	if key == "v" {
		m.selecting = !m.selecting
		if m.selecting {
			m.selectionRow, m.selectionColumn = m.row, m.column
			m.status = "Range selection: Enter spans and edits, g charts, m spans, y copies"
		} else {
			m.status = "Range selection cancelled"
		}
		return nil
	}
	if key == "m" {
		if !m.selecting {
			m.errMessage = "Select a range with v before spanning cells"
			return nil
		}
		span := m.selectedCellRange()
		if m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
			return spreadsheet.SpanCellsInBook(book, book.Sheets[m.sheet].Name, span)
		}) {
			m.selecting = false
			m.row, m.column = span.FirstRow-1, span.FirstColumn-1
			m.scrollToSelection()
			m.status = "Cells spanned"
			return m.syncAutosave()
		}
		return nil
	}
	if key == "M" {
		if m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
			return spreadsheet.SplitCellInBook(book, book.Sheets[m.sheet].Name, m.row+1, m.column+1)
		}) {
			m.selecting = false
			m.status = "Cell span split"
			return m.syncAutosave()
		}
		return nil
	}
	if key == "g" {
		m.status, m.errMessage = "", ""
		if m.selecting {
			return m.openSelectedRangeChart()
		}
		return m.openCellChart()
	}
	if key == "y" {
		return m.copySelection()
	}
	if key == "p" {
		return m.pasteInternal()
	}
	if key == "P" {
		return m.pasteSystemClipboard()
	}
	if key == "d" {
		m.pendingShortcut = "d"
		m.status = "Delete (r)ow, (c)olumn, or (s)heet?"
		return nil
	}
	if key == "i" {
		m.pendingShortcut = "i"
		m.status = "Insert (r)ow or (c)olumn before selection?"
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
		m.normalizeSpanCursor()
		m.scrollToSelection()
	case "end":
		m.column = max(0, len(m.currentRow())-1)
		m.normalizeSpanCursor()
		m.scrollToSelection()
	case "tab", "shift+tab":
		if len(m.book.Sheets) > 1 {
			m.selecting = false
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
		if key == "enter" && m.selecting {
			return m.editSelectedRange()
		}
		if _, ok := spreadsheet.ChartAt(&m.book, m.book.Sheets[m.sheet].Name, m.row+1, m.column+1); ok {
			return m.openCellChart()
		}
		return m.beginEdit(cellDraft(m.cell(m.row, m.column)))
	case "delete", "backspace":
		if chart, ok := spreadsheet.ChartAt(&m.book, m.book.Sheets[m.sheet].Name, m.row+1, m.column+1); ok {
			for i := range m.book.Charts {
				if m.book.Charts[i].ID == chart.ID {
					m.charts.index = i
					m.charts.mode = chartDelete
					break
				}
			}
			return nil
		}
		if m.cell(m.row, m.column) != (spreadsheet.Cell{}) {
			if m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
				return spreadsheet.SetCellInBook(book, spreadsheet.SetCellOptions{
					Sheet: m.book.Sheets[m.sheet].Name, Row: m.row + 1, Column: m.column + 1,
				})
			}) {
				return m.syncAutosave()
			}
		}
	case "u", "ctrl+z":
		return m.undo()
	case "ctrl+r", "ctrl+y", "ctrl+shift+z":
		return m.redo()
	case "ctrl+s":
		if m.embedded {
			if m.editing && !m.commitCell() {
				return nil
			}
			return m.finishEmbedded(true)
		}
		return m.saveWorkbook()
	case "ctrl+p":
		if m.embedded {
			return nil
		}
		return m.openPDFPrompt()
	}
	return nil
}

func (m *spreadsheetModel) move(rows, columns int) {
	if m.loaded {
		if span, ok := spreadsheet.SpanAt(m.book.Sheets[m.sheet], m.row+1, m.column+1); ok {
			if rows > 0 {
				m.row = span.LastRow - 1
			} else if columns > 0 {
				m.column = span.LastColumn - 1
			}
		}
	}
	m.row = min(max(m.row+rows, 0), spreadsheet.MaxRows-1)
	m.column = min(max(m.column+columns, 0), spreadsheet.MaxColumns-1)
	m.normalizeSpanCursor()
	m.scrollToSelection()
}

func (m *spreadsheetModel) normalizeSpanCursor() {
	if m.loaded {
		if span, ok := spreadsheet.SpanAt(m.book.Sheets[m.sheet], m.row+1, m.column+1); ok {
			m.row, m.column = span.FirstRow-1, span.FirstColumn-1
		}
	}
}

func (m *spreadsheetModel) selectedCellRange() spreadsheet.CellSpan {
	span := spreadsheet.CellSpan{
		FirstRow: m.row + 1, LastRow: m.row + 1,
		FirstColumn: m.column + 1, LastColumn: m.column + 1,
	}
	if m.selecting {
		span.FirstRow, span.LastRow = min(m.row, m.selectionRow)+1, max(m.row, m.selectionRow)+1
		span.FirstColumn, span.LastColumn = min(m.column, m.selectionColumn)+1, max(m.column, m.selectionColumn)+1
	}
	if !m.loaded {
		return span
	}
	return spreadsheet.ExpandSpanRange(m.book.Sheets[m.sheet], span)
}

func (m *spreadsheetModel) beginEdit(draft string) tea.Cmd {
	if m.cell(m.row, m.column) == (spreadsheet.Cell{}) {
		if _, ok := m.values[[2]int{m.row, m.column}]; ok {
			m.errMessage = "Cannot edit a spilled cell; edit its formula anchor"
			return nil
		}
	}
	m.editing = true
	m.input.SetValue(draft)
	m.input.CursorEnd()
	return m.input.Focus()
}

func (m *spreadsheetModel) commitCell() bool {
	draft := m.input.Value()
	cell, err := parseCellInput(draft)
	if err != nil {
		m.errMessage = err.Error()
		return false
	}
	if !m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
		return spreadsheet.SetCellInBook(book, spreadsheet.SetCellOptions{
			Sheet: m.book.Sheets[m.sheet].Name, Row: m.row + 1, Column: m.column + 1, Cell: cell,
		})
	}) {
		return false
	}
	m.editing = false
	m.input.Blur()
	return true
}

func (m *spreadsheetModel) saveWorkbook() tea.Cmd {
	if !m.dirty() {
		m.status, m.errMessage = "No changes to save", ""
		return nil
	}
	m.stopAutosave()
	ctx, seq := m.startOperation()
	m.status, m.errMessage = "Saving…", ""
	service, expected, updated := m.service, cloneSpreadsheetState(m.baseline), cloneSpreadsheetState(m.book)
	store, path := m.recoveryStore, m.path
	return func() tea.Msg {
		book, err := service.Update(ctx, expected, updated)
		var cleanupErr error
		if err == nil && store != nil {
			cleanupErr = store.DeleteRecovery(context.Background(), path)
		}
		return spreadsheetSavedMsg{seq: seq, book: book, err: err, cleanupErr: cleanupErr}
	}
}

func (m *spreadsheetModel) requestEmbeddedClose() tea.Cmd {
	if m.dirty() && !m.closeWarning {
		m.closeWarning = true
		m.status, m.errMessage = "Unsaved changes: Ctrl+S applies them; Esc/q again discards", ""
		return nil
	}
	return m.finishEmbedded(false)
}

func (m *spreadsheetModel) finishEmbedded(commit bool) tea.Cmd {
	if commit && m.editing && !m.commitCell() {
		return nil
	}
	if len(m.book.Sheets) == 0 {
		m.status = "Cannot edit an empty workbook"
		return nil
	}
	book := m.book.Workbook()
	return func() tea.Msg {
		return spreadsheetEmbeddedFinishedMsg{workbook: book, committed: commit}
	}
}

func (m *spreadsheetModel) finishEmbeddedChartReference() tea.Cmd {
	sheet := m.book.Sheets[m.sheet].Name
	chart, ok := spreadsheet.ChartAt(&m.book, sheet, m.row+1, m.column+1)
	if !ok {
		m.status, m.errMessage = "The selected cell does not contain a chart", ""
		return nil
	}
	book := m.book.Workbook()
	return func() tea.Msg {
		return spreadsheetEmbeddedFinishedMsg{
			workbook: book, committed: true, chartReferenceID: chart.ID,
		}
	}
}

func (m *spreadsheetModel) requestClose() tea.Cmd {
	if m.dirty() && !m.closeWarning {
		m.closeWarning = true
		m.status, m.errMessage = "Unsaved changes: Ctrl+S saves, Esc/q again discards and quits", ""
		return nil
	}
	return m.close()
}

func (m *spreadsheetModel) close() tea.Cmd {
	if m.embedded {
		return m.finishEmbedded(false)
	}
	m.stopAutosave()
	if m.recoveryStore == nil {
		m.cancelPending()
		return tea.Quit
	}
	m.closing = true
	ctx, seq := m.startOperation()
	m.status, m.errMessage = "Closing…", ""
	store, path := m.recoveryStore, m.path
	return func() tea.Msg {
		return spreadsheetCloseFinishedMsg{seq: seq, err: store.DeleteRecovery(ctx, path)}
	}
}

func (m *spreadsheetModel) load() tea.Cmd {
	ctx, seq := m.startOperation()
	m.status, m.errMessage = "Loading…", ""
	service, path := m.service, m.path
	return func() tea.Msg {
		book, err := service.Get(ctx, path)
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
	case errors.Is(err, context.Canceled):
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
	m.book, m.baseline, m.loaded = cloneSpreadsheetState(book), cloneSpreadsheetState(book), true
	m.selecting = false
	m.clearSearch()
	m.bufferUndo, m.bufferRedo = nil, nil
	m.sheet = 0
	if index, err := spreadsheet.SheetIndex(book, name); err == nil && name != "" {
		m.sheet = index
	}
	m.status, m.errMessage, m.closeWarning = "", "", false
	m.computeValues()
	m.normalizeSpanCursor()
	m.scrollToSelection()
}

func (m *spreadsheetModel) dirty() bool {
	if !m.loaded {
		return false
	}
	if m.embedded {
		if !m.book.Workbook().Equal(m.baseline.Workbook()) {
			return true
		}
		return m.editing && m.input.Value() != cellDraft(m.cell(m.row, m.column))
	}
	if !spreadsheet.Equal(m.book, m.baseline) {
		return true
	}
	return m.editing && m.input.Value() != cellDraft(m.cell(m.row, m.column))
}

func (m *spreadsheetModel) editWorkbook(edit func(*spreadsheet.Spreadsheet) error) bool {
	selectedSheet := m.book.Sheets[m.sheet].Name
	next := cloneSpreadsheetState(m.book)
	if err := edit(&next); err != nil {
		m.setError("Edit failed", err)
		return false
	}
	if m.embedded {
		workbook := next.Workbook()
		if err := spreadsheet.NormalizeWorkbook(&workbook); err != nil {
			m.setError("Edit failed", err)
			return false
		}
		next.Sheets, next.Charts = workbook.Sheets, workbook.Charts
	} else if err := spreadsheet.Normalize(&next); err != nil {
		m.setError("Edit failed", err)
		return false
	}
	if spreadsheet.Equal(m.book, next) {
		m.errMessage = ""
		return true
	}
	m.bufferUndo = appendBoundedWorkbook(m.bufferUndo, m.book, m.undoLimit)
	m.bufferRedo = nil
	m.book = next
	m.clearSearch()
	m.sheet = min(m.sheet, len(m.book.Sheets)-1)
	if index, err := spreadsheet.SheetIndex(m.book, selectedSheet); err == nil {
		m.sheet = index
	}
	m.status, m.errMessage, m.closeWarning = "", "", false
	m.computeValues()
	m.normalizeSpanCursor()
	m.scrollToSelection()
	return true
}

func appendBoundedWorkbook(stack []spreadsheet.Spreadsheet, book spreadsheet.Spreadsheet, limit int) []spreadsheet.Spreadsheet {
	stack = append(stack, cloneSpreadsheetState(book))
	if len(stack) > limit {
		stack = append([]spreadsheet.Spreadsheet(nil), stack[len(stack)-limit:]...)
	}
	return stack
}

func (m *spreadsheetModel) undo() tea.Cmd {
	if len(m.bufferUndo) > 0 {
		m.bufferRedo = appendBoundedWorkbook(m.bufferRedo, m.book, m.undoLimit)
		m.book = m.bufferUndo[len(m.bufferUndo)-1]
		m.clearSearch()
		m.bufferUndo = m.bufferUndo[:len(m.bufferUndo)-1]
		m.status, m.errMessage = "Undid spreadsheet edit", ""
		m.sheet = min(max(m.sheet, 0), len(m.book.Sheets)-1)
		m.computeValues()
		m.normalizeSpanCursor()
		m.scrollToSelection()
		return m.syncAutosave()
	}
	if m.dirty() || len(m.bufferRedo) > 0 {
		return nil
	}
	if m.embedded {
		return nil
	}
	return m.history("Undid", m.service.Undo)
}

func (m *spreadsheetModel) redo() tea.Cmd {
	if len(m.bufferRedo) > 0 {
		m.bufferUndo = appendBoundedWorkbook(m.bufferUndo, m.book, m.undoLimit)
		m.book = m.bufferRedo[len(m.bufferRedo)-1]
		m.clearSearch()
		m.bufferRedo = m.bufferRedo[:len(m.bufferRedo)-1]
		m.status, m.errMessage = "Redid spreadsheet edit", ""
		m.sheet = min(max(m.sheet, 0), len(m.book.Sheets)-1)
		m.computeValues()
		m.normalizeSpanCursor()
		m.scrollToSelection()
		return m.syncAutosave()
	}
	if m.dirty() || len(m.bufferUndo) > 0 {
		return nil
	}
	if m.embedded {
		return nil
	}
	return m.history("Redid", m.service.Redo)
}

func cloneSpreadsheetState(book spreadsheet.Spreadsheet) spreadsheet.Spreadsheet {
	clone := book
	clone.Charts = append([]spreadsheet.Chart(nil), book.Charts...)
	clone.Sheets = append([]spreadsheet.Sheet(nil), book.Sheets...)
	for i := range clone.Sheets {
		clone.Sheets[i].Spans = append([]spreadsheet.CellSpan(nil), book.Sheets[i].Spans...)
		clone.Sheets[i].Rows = make([][]spreadsheet.Cell, len(book.Sheets[i].Rows))
		for row := range book.Sheets[i].Rows {
			clone.Sheets[i].Rows[row] = append([]spreadsheet.Cell(nil), book.Sheets[i].Rows[row]...)
		}
	}
	if len(book.Blocks) > 0 {
		clone.Blocks = make(map[string]json.RawMessage, len(book.Blocks))
		for key, value := range book.Blocks {
			clone.Blocks[key] = append(json.RawMessage(nil), value...)
		}
	}
	return clone
}

// computeValues evaluates formulas in the current sheet so views stay pure.
func (m *spreadsheetModel) computeValues() {
	m.gridChartSeq++
	m.gridChartDirty = true
	m.gridCharts = nil
	m.gridChartInfo = nil
	m.values = make(map[[2]int]string)
	evaluator := spreadsheet.NewEvaluator(&m.book)
	spills, err := evaluator.SpillCells(m.sheet)
	if err != nil {
		m.errMessage = "Calculation failed: " + err.Error()
		return
	}
	for cell, value := range spills {
		m.values[[2]int{cell[0] - 1, cell[1] - 1}] = value.Display()
	}
	for row, cells := range m.book.Sheets[m.sheet].Rows {
		for column, cell := range cells {
			if cell.Formula == "" {
				continue
			}
			value, err := evaluator.Evaluate(m.sheet, row+1, column+1)
			if err != nil {
				m.values[[2]int{row, column}] = "#ERR"
				continue
			}
			m.values[[2]int{row, column}] = value.Display()
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
	if value, ok := m.values[[2]int{row, column}]; ok {
		return value
	}
	return cell.Value
}

func (m *spreadsheetModel) clearSearch() {
	m.find.close()
	m.searchMatches, m.searchMatch = nil, -1
}

func (m *spreadsheetModel) updateFindKey(msg tea.Msg) tea.Cmd {
	action, cmd := m.find.update(msg)
	switch action {
	case findChanged:
		m.searchSpreadsheet(m.find.query())
	case findNext:
		m.moveSearchMatch(1)
		m.find.blur()
	case findPrevious:
		m.moveSearchMatch(-1)
		m.find.blur()
	case findClosed:
		m.searchSpreadsheet("")
	}
	return cmd
}

func (m *spreadsheetModel) searchSpreadsheet(query string) {
	m.searchMatches = nil
	m.searchMatch = -1
	if !m.loaded || query == "" {
		return
	}
	originalSheet, originalValues := m.sheet, m.values
	for sheet := range m.book.Sheets {
		m.sheet = sheet
		m.computeValues()
		rows, columns := m.sheetDimensions(sheet)
		for row := 0; row < rows; row++ {
			for column := 0; column < columns; column++ {
				cell := m.cellOnSheet(sheet, row, column)
				if len(findTextMatches(cell.Value, query)) > 0 ||
					len(findTextMatches(cell.Formula, query)) > 0 ||
					len(findTextMatches(m.values[[2]int{row, column}], query)) > 0 {
					m.searchMatches = append(m.searchMatches, spreadsheetSearchMatch{sheet, row, column})
				}
			}
		}
	}
	m.sheet, m.values = originalSheet, originalValues
	if len(m.searchMatches) == 0 {
		return
	}
	m.searchMatch = 0
	m.gotoSearchMatch()
}

func (m *spreadsheetModel) gotoSearchMatch() {
	if m.searchMatch < 0 || m.searchMatch >= len(m.searchMatches) {
		return
	}
	match := m.searchMatches[m.searchMatch]
	if match.sheet < 0 || match.sheet >= len(m.book.Sheets) ||
		match.row < 0 || match.column < 0 {
		m.clearSearch()
		return
	}
	m.sheet, m.row, m.column = match.sheet, match.row, match.column
	m.normalizeSpanCursor()
	m.computeValues()
	rows, columns := m.sheetDimensions(m.sheet)
	if match.row >= rows || match.column >= columns {
		m.clearSearch()
		return
	}
	m.scrollToSelection()
}

func (m *spreadsheetModel) sheetDimensions(sheet int) (rows, columns int) {
	for _, row := range m.book.Sheets[sheet].Rows {
		columns = max(columns, len(row))
	}
	rows = len(m.book.Sheets[sheet].Rows)
	for coordinate := range m.values {
		rows = max(rows, coordinate[0]+1)
		columns = max(columns, coordinate[1]+1)
	}
	return rows, columns
}

func (m *spreadsheetModel) cellOnSheet(sheet, row, column int) spreadsheet.Cell {
	rows := m.book.Sheets[sheet].Rows
	if row >= 0 && row < len(rows) && column >= 0 && column < len(rows[row]) {
		return rows[row][column]
	}
	return spreadsheet.Cell{}
}

func (m *spreadsheetModel) moveSearchMatch(step int) {
	if len(m.searchMatches) == 0 {
		return
	}
	m.searchMatch = (m.searchMatch + step) % len(m.searchMatches)
	if m.searchMatch < 0 {
		m.searchMatch += len(m.searchMatches)
	}
	m.gotoSearchMatch()
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
	promptRows := 0
	if m.pdfPrompt != promptNone {
		promptRows = 1
	}
	return max(1, m.height-spreadsheetChromeLines-promptRows)
}

func (m *spreadsheetModel) visibleColumns() int {
	return max(1, (m.width-4-m.rowHeaderWidth())/(spreadsheetColumnWidth+1))
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

func (m *spreadsheetModel) renderHeader() string {
	title := "Spreadsheet"
	if m.loaded {
		title = m.book.Title
		if len(m.book.Sheets) > 1 || m.book.Sheets[m.sheet].Name != "Sheet1" {
			title += " — " + m.book.Sheets[m.sheet].Name
		}
		if m.charts.mode == chartForm {
			title += " — Chart " + spreadsheet.CellName(m.row+1, m.column+1)
		}
	}
	header := sanitizeTerminalLine(title)
	if m.path != "" && !m.embedded {
		header += "  " + sanitizeTerminalLine(m.path)
	}
	return m.theme.Chrome.Render(runewidth.Truncate(header, m.width, "…"))
}

func (m *spreadsheetModel) renderFormulaBar() string {
	reference := spreadsheet.CellName(m.row+1, m.column+1)
	if m.editing {
		return reference + " > " + m.input.View()
	}
	line := reference + " │ " + sanitizeTerminalLine(cellDraft(m.cell(m.row, m.column)))
	if m.find.visible && m.find.query() != "" {
		line = highlightLine(line, m.find.query(), m.theme)
	}
	return ansi.Truncate(line, m.width, "…")
}

func (m *spreadsheetModel) renderColumnHeader(rowWidth, columns int) string {
	var lineBuf strings.Builder
	lineBuf.Grow(rowWidth + columns*(spreadsheetColumnWidth+1))
	lineBuf.WriteString(strings.Repeat(" ", rowWidth))
	for c := m.columnOffset; c < m.columnOffset+columns && c < spreadsheet.MaxColumns; c++ {
		lineBuf.WriteByte(' ')
		lineBuf.WriteString(fitCell(columnName(c), spreadsheetColumnWidth))
	}
	return lineBuf.String()
}

func (m *spreadsheetModel) renderGrid() string {
	muted := m.theme.Muted
	selected := m.theme.Selection

	rowWidth := m.rowHeaderWidth()
	columns := m.visibleColumns()
	visibleRows := m.visibleRows()
	selection := m.selectedCellRange()
	var sheet spreadsheet.Sheet
	if m.loaded {
		sheet = m.book.Sheets[m.sheet]
	}
	visibleSpans := make(map[[2]int]spreadsheet.CellSpan)
	for _, span := range sheet.Spans {
		for row := max(span.FirstRow-1, m.rowOffset); row < min(span.LastRow, m.rowOffset+visibleRows); row++ {
			for col := max(span.FirstColumn-1, m.columnOffset); col < min(span.LastColumn, m.columnOffset+columns); col++ {
				visibleSpans[[2]int{row, col}] = span
			}
		}
	}

	lineBuf := m.renderColumnHeader(rowWidth, columns)

	var gridBuf strings.Builder
	gridBuf.Grow((visibleRows + 1) * (rowWidth + columns*(spreadsheetColumnWidth+1) + 1))
	gridBuf.WriteString(muted.Render(lineBuf))
	gridBuf.WriteByte('\n')

	for r := m.rowOffset; r < m.rowOffset+visibleRows; r++ {
		if r >= spreadsheet.MaxRows {
			gridBuf.WriteByte('\n')
			continue
		}
		gridBuf.WriteString(muted.Render(fmt.Sprintf("%*d", rowWidth, r+1)))
		for c := m.columnOffset; c < m.columnOffset+columns && c < spreadsheet.MaxColumns; {
			span, spanned := visibleSpans[[2]int{r, c}]
			end := c + 1
			text := fitCell(m.displayValue(r, c), spreadsheetColumnWidth)
			if lines, ok := m.gridCharts[[2]int{r, c}]; ok && len(lines) > 0 {
				text = chartFormLine(lines[0], spreadsheetColumnWidth)
			} else if _, ok := spreadsheet.ChartAt(&m.book, sheet.Name, r+1, c+1); ok {
				text = fitCell("[Chart]", spreadsheetColumnWidth)
			}
			if spanned {
				end = min(span.LastColumn, m.columnOffset+columns, spreadsheet.MaxColumns)
				width := (span.LastColumn-span.FirstColumn+1)*(spreadsheetColumnWidth+1) - 1
				value := ""
				if r+1 == span.FirstRow {
					value = m.displayValue(span.FirstRow-1, span.FirstColumn-1)
				}
				_, chart := spreadsheet.ChartAt(&m.book, sheet.Name, span.FirstRow, span.FirstColumn)
				if chart {
					value = ""
					lines := m.gridCharts[[2]int{span.FirstRow - 1, span.FirstColumn - 1}]
					line := r + 1 - span.FirstRow
					if line < len(lines) {
						value = lines[line]
					} else if line == 0 && len(lines) == 0 {
						value = "[Chart]"
					}
				}
				start := (c - span.FirstColumn + 1) * (spreadsheetColumnWidth + 1)
				if chart {
					text = ansi.Cut(chartFormLine(value, width), start, start+(end-c)*(spreadsheetColumnWidth+1)-1)
				} else {
					text = ansi.Cut(fitCell(value, width), start, start+(end-c)*(spreadsheetColumnWidth+1)-1)
				}
			}
			if m.find.visible && m.find.query() != "" {
				text = highlightLine(text, m.find.query(), m.theme)
			}
			if r+1 >= selection.FirstRow && r+1 <= selection.LastRow &&
				c+1 >= selection.FirstColumn && c+1 <= selection.LastColumn {
				text = selected.Render(text)
			}
			gridBuf.WriteByte(' ')
			gridBuf.WriteString(text)
			c = end
		}
		if r < m.rowOffset+visibleRows-1 {
			gridBuf.WriteByte('\n')
		}
	}

	return gridBuf.String()
}

func (m *spreadsheetModel) renderStatus() string {
	status := m.status
	if m.errMessage != "" {
		status = "Error: " + m.errMessage
	} else if status == "" && m.loaded {
		dimensions := fmt.Sprintf("%d×%d", len(m.book.Sheets[m.sheet].Rows), len(m.book.Sheets[m.sheet].Rows[0]))
		if m.dirty() {
			status = "Unsaved changes • " + dimensions
		} else {
			status = dimensions
		}
		if info := m.gridChartInfo[[2]int{m.row, m.column}]; info != "" {
			status += " • " + sanitizeTerminalLine(info)
		}
	}
	return runewidth.Truncate(sanitizeTerminalLine(status), m.width, "…")
}

func (m *spreadsheetModel) centerSheetIndicator(status string) string {
	if !m.loaded || len(m.book.Sheets) < 2 {
		return status
	}
	indicator := fmt.Sprintf("Sheet %d/%d", m.sheet+1, len(m.book.Sheets))
	indicatorWidth := runewidth.StringWidth(indicator)
	indicatorStart := (m.width - indicatorWidth) / 2
	if indicatorStart < 0 {
		return ansi.Truncate(indicator, m.width, "…")
	}
	left := ansi.Truncate(status, max(indicatorStart-1, 0), "…")
	padding := max(indicatorStart-ansi.StringWidth(left), 0)
	return left + strings.Repeat(" ", padding) + indicator
}

func (m *spreadsheetModel) renderHelp() string {
	muted := m.theme.Muted
	help := "g chart • v select • m span • M split • arrows move • enter edit • ctrl+s save • del clear • y copy • p paste • P text paste • ctrl+p PDF • f formulas • tab sheet • u/ctrl+z undo • ctrl+r redo • d delete • i insert • r reload • esc/q quit"
	if m.embedded {
		help = "g chart • ctrl+shift+r insert chart reference • arrows move • enter edit • ctrl+s apply • esc cancel • tab sheet"
	}
	if m.editing {
		help = "enter stage • ctrl+s save • esc cancel • =formula • 'literal • shift+enter/alt+enter down • ctrl+p PDF"
	} else if !m.loaded {
		help = "r retry • esc/q quit"
	} else if m.selecting {
		help = "enter span • g chart • m span • y copy • esc cancel • arrows select"
	} else if _, ok := spreadsheet.SpanAt(m.book.Sheets[m.sheet], m.row+1, m.column+1); ok {
		help = "M split • v select • g new chart • arrows move • enter edit • ctrl+s save"
	}
	return muted.Render(runewidth.Truncate(help, m.width, "…"))
}

func (m *spreadsheetModel) chartTargetError(row, column int) error {
	name := m.book.Sheets[m.sheet].Name
	if _, ok := spreadsheet.ChartAt(&m.book, name, row+1, column+1); ok {
		return nil
	}
	if m.cell(row, column) != (spreadsheet.Cell{}) {
		return errors.New("clear the cell before creating a chart")
	}
	spills, err := spreadsheet.NewEvaluator(&m.book).SpillCells(m.sheet)
	if err != nil {
		return err
	}
	if _, ok := spills[[2]int{row + 1, column + 1}]; ok {
		return errors.New("cannot create a chart in a spilled cell")
	}
	return nil
}

func (m *spreadsheetModel) openSelectedRangeChart() tea.Cmd {
	span := m.selectedCellRange()
	row, column := span.FirstRow-1, span.FirstColumn-1
	if err := m.chartTargetError(row, column); err != nil {
		m.setError("Chart", err)
		return nil
	}
	if !m.finalizeSelectedSpan(span) {
		return nil
	}
	return tea.Batch(m.syncAutosave(), m.openCellChart())
}

func (m *spreadsheetModel) finalizeSelectedSpan(span spreadsheet.CellSpan) bool {
	existing, spanned := spreadsheet.SpanAt(m.book.Sheets[m.sheet], span.FirstRow, span.FirstColumn)
	needsSpan := (span.FirstRow != span.LastRow || span.FirstColumn != span.LastColumn) &&
		(!spanned || existing != span)
	if needsSpan && !m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
		return spreadsheet.SpanCellsInBook(book, book.Sheets[m.sheet].Name, span)
	}) {
		return false
	}
	m.selecting = false
	m.row, m.column = span.FirstRow-1, span.FirstColumn-1
	m.status, m.errMessage = "", ""
	m.scrollToSelection()
	return true
}

func (m *spreadsheetModel) editSelectedRange() tea.Cmd {
	span := m.selectedCellRange()
	row, column := span.FirstRow-1, span.FirstColumn-1
	if span.FirstRow == span.LastRow && span.FirstColumn == span.LastColumn &&
		m.cell(row, column) == (spreadsheet.Cell{}) {
		if _, ok := m.values[[2]int{row, column}]; ok {
			m.errMessage = "Cannot edit a spilled cell; edit its formula anchor"
			return nil
		}
	}
	if !m.finalizeSelectedSpan(span) {
		return nil
	}
	if _, ok := spreadsheet.ChartAt(&m.book, m.book.Sheets[m.sheet].Name, m.row+1, m.column+1); ok {
		return tea.Batch(m.syncAutosave(), m.openCellChart())
	}
	return tea.Batch(m.syncAutosave(), m.beginEdit(cellDraft(m.cell(m.row, m.column))))
}

func (m *spreadsheetModel) openCellChart() tea.Cmd {
	name := m.book.Sheets[m.sheet].Name
	chart, ok := spreadsheet.ChartAt(&m.book, name, m.row+1, m.column+1)
	if ok {
		for i := range m.book.Charts {
			if m.book.Charts[i].ID == chart.ID {
				m.charts.index = i
				return m.openChartForm(true)
			}
		}
	}
	if err := m.chartTargetError(m.row, m.column); err != nil {
		m.setError("Chart", err)
		return nil
	}
	return m.openChartForm(false)
}

func (m *spreadsheetModel) View() tea.View {
	return tea.NewView(m.viewContent())
}

func (m *spreadsheetModel) viewContent() string {
	if m.charts.mode != chartClosed {
		return m.viewCharts()
	}
	if m.width < 20 || m.height < spreadsheetChromeLines+1 {
		return "Terminal too small. Press q to quit."
	}
	shortcuts := m.renderHelp() + "\n" + m.renderFormulaBar()
	content := m.renderGrid()
	if m.pdfPrompt != promptNone {
		content = m.pdfInput.View() + "\n" + content
	}
	status := m.renderStatus()
	if m.find.visible {
		status = m.find.view(len(m.searchMatches), m.searchMatch, m.width)
	}
	status = m.centerSheetIndicator(status)
	output := chromeLayout(m.width, m.height, m.renderHeader(), shortcuts, content, status, m.theme.BorderColor)
	return output
}
