package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/filerepo"
	"example.com/parchment/internal/recovery"
	"example.com/parchment/internal/spreadsheet"
)

// driveSheet sends a message and feeds command results back into the model.
func driveSheet(t *testing.T, m *spreadsheetModel, message tea.Msg) tea.Msg {
	t.Helper()
	m.find.input.Cursor.SetMode(cursor.CursorStatic)
	_, cmd := m.Update(message)
	var last tea.Msg
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil || m.editing {
			return
		}
		result := c()
		switch result := result.(type) {
		case tea.BatchMsg:
			for _, inner := range result {
				run(inner)
			}
		case nil:
		case tea.QuitMsg:
			last = result
		default:
			if _, isBlink := result.(interface{ Blink() }); isBlink {
				return
			}
			last = result
			_, next := m.Update(result)
			run(next)
		}
	}
	run(cmd)
	return last
}

func sheetKey(name string) tea.KeyMsg {
	switch name {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "shift+enter":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shift+enter")}
	case "alt+enter":
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "delete":
		return tea.KeyMsg{Type: tea.KeyDelete}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+z":
		return tea.KeyMsg{Type: tea.KeyCtrlZ}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

func newTestRepository(t *testing.T) *filerepo.Repository {
	t.Helper()
	repository, err := filerepo.New(t.TempDir(), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func newSheetModel(t *testing.T, rows [][]spreadsheet.Cell) (*spreadsheetModel, *spreadsheet.Service, *filerepo.Repository, string) {
	t.Helper()
	repository := newTestRepository(t)
	service := spreadsheet.NewService(repository, 10)
	path := filepath.Join(t.TempDir(), "budget.md")
	book, err := service.Create(context.Background(), path, rows)
	if err != nil {
		t.Fatal(err)
	}

	m := newSpreadsheetModel(service, book.Path, Theme{}, 10)
	driveSheet(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	if msg := m.Init()(); msg != nil {
		driveSheet(t, m, msg)
	}
	if !m.loaded || m.pending {
		t.Fatalf("model not loaded: loaded %t pending %t err %q", m.loaded, m.pending, m.errMessage)
	}
	return m, service, repository, book.Path
}

func TestSpreadsheetRangeClipboard(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "7"}, {Formula: "=A1+$A$1"}}})
	var system string
	m.clipboardWrite = func(text string) error { system = text; return nil }
	m.clipboardRead = func() (string, error) { return "=untrusted\t\"two\nlines\"\n", nil }
	driveSheet(t, m, sheetKey("v"))
	driveSheet(t, m, sheetKey("right"))
	driveSheet(t, m, sheetKey("y"))
	if system != "7\t=A1+$A$1\n" {
		t.Fatalf("system copy = %q", system)
	}
	driveSheet(t, m, sheetKey("down"))
	driveSheet(t, m, sheetKey("left"))
	driveSheet(t, m, sheetKey("p"))
	if got := m.book.Sheets[0].Rows[1]; got[0].Value != "7" || got[1].Formula != "=A2+$A$1" {
		t.Fatalf("internal paste = %#v", got)
	}
	driveSheet(t, m, sheetKey("u"))
	if got := m.cell(1, 0); got != (spreadsheet.Cell{}) {
		t.Fatalf("undo did not revert range: %#v", got)
	}
	driveSheet(t, m, sheetKey("P"))
	if got := m.book.Sheets[0].Rows[1]; got[0].Value != "=untrusted" || got[0].Formula != "" || got[1].Value != "two\nlines" {
		t.Fatalf("external paste = %#v", got)
	}
}

func TestSpreadsheetClipboardRoundTripsEmptyRows(t *testing.T) {
	copied := spreadsheet.CellClipboard{Cells: [][]spreadsheet.Cell{{{Value: "first"}}, {{}}, {{Value: "last"}}}}
	text, err := encodeSpreadsheetClipboard(copied)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := decodeSpreadsheetClipboard(text)
	if err != nil || len(rows) != 3 || rows[1][0].Value != "" || rows[2][0].Value != "last" {
		t.Fatalf("clipboard round trip = %#v, %v", rows, err)
	}
	rows, err = decodeSpreadsheetClipboard("\"first\n\nline\"\r\n\r\nlast\r\n")
	if err != nil || len(rows) != 3 || rows[0][0].Value != "first\n\nline" || rows[1][0].Value != "" || rows[2][0].Value != "last" {
		t.Fatalf("external blank rows = %#v, %v", rows, err)
	}
}

func TestSpreadsheetClipboardFailuresKeepInternalCopy(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "source"}}})
	m.clipboardWrite = func(string) error { return errors.New("unavailable") }
	m.clipboardRead = func() (string, error) { return "", errors.New("unavailable") }
	driveSheet(t, m, sheetKey("y"))
	if m.clipboard == nil || !strings.Contains(m.status, "internally") {
		t.Fatalf("copy failed to retain internal clipboard: %q", m.status)
	}
	driveSheet(t, m, sheetKey("down"))
	driveSheet(t, m, sheetKey("P"))
	if !strings.Contains(m.errMessage, "unavailable") {
		t.Fatalf("read failure not reported: %q", m.errMessage)
	}
	driveSheet(t, m, sheetKey("p"))
	if got := m.cell(1, 0).Value; got != "source" {
		t.Fatalf("internal copy after system failure = %q", got)
	}
}

func TestSpreadsheetDisplaysAndProtectsSpill(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Formula: "=SEQUENCE(2,2)"}}})
	var copied string
	m.clipboardWrite = func(text string) error { copied = text; return nil }
	driveSheet(t, m, sheetKey("v"))
	driveSheet(t, m, sheetKey("down"))
	driveSheet(t, m, sheetKey("right"))
	driveSheet(t, m, sheetKey("y"))
	if copied != "=SEQUENCE(2,2)\t2\n3\t4\n" {
		t.Fatalf("copied spill text = %q", copied)
	}

	m.row, m.column = 0, 0
	if got := m.displayValue(1, 1); got != "4" {
		t.Fatalf("spill display = %q", got)
	}
	driveSheet(t, m, sheetKey("down"))
	driveSheet(t, m, sheetKey("right"))
	driveSheet(t, m, sheetKey("enter"))
	if m.editing || !strings.Contains(m.errMessage, "spilled cell") {
		t.Fatalf("spill edit: editing=%t, error=%q", m.editing, m.errMessage)
	}
}

func TestSpreadsheetSearchFindsSpilledCells(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Formula: "=SEQUENCE(2,2)"}}})
	m.searchSpreadsheet("4")
	if len(m.searchMatches) != 1 {
		t.Fatalf("search matches = %+v", m.searchMatches)
	}
	match := m.searchMatches[0]
	if match.sheet != 0 || match.row != 1 || match.column != 1 ||
		m.row != 1 || m.column != 1 || m.displayValue(1, 1) != "4" {
		t.Fatalf("spill search selection = %+v at %s: %q", match,
			spreadsheet.CellName(m.row+1, m.column+1), m.displayValue(1, 1))
	}
}

func TestSpreadsheetAutosavesWorkbookAndActiveCellInput(t *testing.T) {
	m, _, repository, path := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "original"}}})
	m.recoveryStore = repository
	m.baseline.Body = "Workbook notes"
	m.baseline.Blocks = map[string]json.RawMessage{
		"parchment-custom": json.RawMessage(`{"value":"kept"}`),
	}
	m.book = cloneSpreadsheetState(m.baseline)
	m.autosaveScheduler = func(session uint64) tea.Cmd {
		return func() tea.Msg { return spreadsheetAutosaveTickMsg{session: session} }
	}
	if cmd := m.syncAutosave(); cmd != nil || m.autosaveActive {
		t.Fatal("clean workbook started an autosave timer")
	}

	if !m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
		return spreadsheet.SetCellInBook(book, spreadsheet.SetCellOptions{
			Sheet: "Sheet1", Row: 1, Column: 1, Cell: spreadsheet.Cell{Value: "buffered"},
		})
	}) {
		t.Fatalf("buffered edit failed: %s", m.errMessage)
	}
	m.column = 1
	m.editing = true
	m.input.SetValue("pending cell input")
	tickCmd := m.syncAutosave()
	if tickCmd == nil || !m.autosaveActive {
		t.Fatal("dirty workbook did not start an autosave timer")
	}
	session := m.autosaveSession
	if _, ok := tickCmd().(spreadsheetAutosaveTickMsg); !ok {
		t.Fatal("autosave scheduler returned the wrong tick")
	}
	_, saveCmd := m.Update(spreadsheetAutosaveTickMsg{session: session})
	if saveCmd == nil {
		t.Fatal("autosave tick did not save a recovery draft")
	}
	finished, ok := saveCmd().(spreadsheetAutosaveFinishedMsg)
	if !ok || finished.err != nil {
		t.Fatalf("autosave result = %#v", finished)
	}
	_, nextTick := m.Update(finished)
	if nextTick == nil {
		t.Fatal("dirty workbook did not schedule its next autosave")
	}

	draft, found, err := repository.LoadRecovery(context.Background(), path)
	if err != nil || !found {
		t.Fatalf("LoadRecovery = found %t, err %v", found, err)
	}
	if draft.Kind != string(artifact.SpreadsheetKind) {
		t.Fatalf("draft kind = %q", draft.Kind)
	}
	var data spreadsheetRecoveryData
	if err := json.Unmarshal(draft.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Baseline.Sheets[0].Rows[0][0].Value != "original" ||
		data.Dirty.Sheets[0].Rows[0][0].Value != "buffered" ||
		data.PendingCellInput != "pending cell input" ||
		!data.Editing ||
		data.BaselineBody != "Workbook notes" ||
		string(data.BaselineBlocks["parchment-custom"]) != `{"value":"kept"}` ||
		data.DirtyBody != "Workbook notes" ||
		string(data.DirtyBlocks["parchment-custom"]) != `{"value":"kept"}` ||
		data.ActiveSheet != 0 || data.CursorRow != 0 || data.CursorCol != 1 {
		t.Fatalf("recovery data = %#v", data)
	}

	m.editing = false
	m.book = cloneSpreadsheetState(m.baseline)
	m.computeValues()
	oldSession := m.autosaveSession
	clearCmd := m.syncAutosave()
	if clearCmd == nil || m.autosaveActive {
		t.Fatal("reverting to the baseline did not stop autosaving and remove the draft")
	}
	if _, cmd := m.Update(spreadsheetAutosaveTickMsg{session: oldSession}); cmd != nil {
		t.Fatal("stale autosave tick was not ignored")
	}
	cleared, ok := clearCmd().(spreadsheetAutosaveFinishedMsg)
	if !ok || cleared.err != nil || !cleared.cleared {
		t.Fatalf("draft cleanup result = %#v", cleared)
	}
	m.Update(cleared)
	if _, found, err := repository.LoadRecovery(context.Background(), path); err != nil || found {
		t.Fatalf("draft remained after returning clean: found %t, err %v", found, err)
	}
}

func storedCell(t *testing.T, service *spreadsheet.Service, path string, row, column int) spreadsheet.Cell {
	t.Helper()
	book, err := service.Get(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	rows := book.Sheets[0].Rows
	if row > len(rows) || column > len(rows[row-1]) {
		return spreadsheet.Cell{}
	}
	return rows[row-1][column-1]
}

func TestSpreadsheetViewShowsValuesAndFormulas(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{
		{{Value: "Rent"}, {Value: "1200"}},
		{{Value: "Food"}, {Value: "300"}},
		{{Value: "Total"}, {Formula: "=B1+B2"}},
	})
	view := m.View()
	for _, want := range []string{"budget.md", "Rent", "1200", "1500", " A ", " B "} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	driveSheet(t, m, sheetKey("down"))
	driveSheet(t, m, sheetKey("down"))
	driveSheet(t, m, sheetKey("right"))
	if view := m.View(); !strings.Contains(view, "B3 │ =B1+B2") {
		t.Fatalf("formula bar missing formula:\n%s", view)
	}
	driveSheet(t, m, sheetKey("f"))
	if view := m.View(); !strings.Contains(view, "=B1+B2") || strings.Contains(view, "1500") {
		t.Fatalf("formula view not shown:\n%s", view)
	}
}

func TestSpreadsheetEditDistinguishesFormulasAndLiterals(t *testing.T) {
	m, service, _, id := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "2"}}})
	edit := func(text string) {
		t.Helper()
		driveSheet(t, m, sheetKey("enter"))
		if !m.editing {
			t.Fatal("enter did not start editing")
		}
		driveSheet(t, m, sheetKey("ctrl+u"))
		driveSheet(t, m, sheetKey(text))
		driveSheet(t, m, sheetKey("enter"))
		if m.editing || m.pending || m.errMessage != "" || !m.dirty() {
			t.Fatalf("edit %q state: editing %t pending %t err %q", text, m.editing, m.pending, m.errMessage)
		}
	}

	driveSheet(t, m, sheetKey("right"))
	edit("=A1*3")
	if got := storedCell(t, service, id, 1, 2); got != (spreadsheet.Cell{}) {
		t.Fatalf("formula was persisted before Ctrl+S: %#v", got)
	}
	if !strings.Contains(m.View(), "6") {
		t.Fatalf("evaluated formula missing:\n%s", m.View())
	}

	driveSheet(t, m, sheetKey("down"))
	edit("'=not a formula")
	if got := storedCell(t, service, id, 2, 2); got != (spreadsheet.Cell{}) {
		t.Fatalf("literal was persisted before Ctrl+S: %#v", got)
	}
	driveSheet(t, m, sheetKey("enter"))
	if m.input.Value() != "'=not a formula" {
		t.Fatalf("literal draft = %q", m.input.Value())
	}
	driveSheet(t, m, sheetKey("esc"))

	driveSheet(t, m, sheetKey("left"))
	edit("hello")
	if got := storedCell(t, service, id, 2, 1); got != (spreadsheet.Cell{}) {
		t.Fatalf("literal was persisted before Ctrl+S: %#v", got)
	}

	driveSheet(t, m, sheetKey("ctrl+s"))
	if m.dirty() || m.pending {
		t.Fatalf("save did not clear dirty state: dirty %t pending %t", m.dirty(), m.pending)
	}
	if got := storedCell(t, service, id, 1, 2); got != (spreadsheet.Cell{Formula: "=A1*3"}) {
		t.Fatalf("saved formula cell = %#v", got)
	}
	if got := storedCell(t, service, id, 2, 2); got != (spreadsheet.Cell{Value: "=not a formula"}) {
		t.Fatalf("saved escaped literal cell = %#v", got)
	}
	if got := storedCell(t, service, id, 2, 1); got != (spreadsheet.Cell{Value: "hello"}) {
		t.Fatalf("saved literal cell = %#v", got)
	}

	driveSheet(t, m, sheetKey("delete"))
	if got := storedCell(t, service, id, 2, 1); got != (spreadsheet.Cell{Value: "hello"}) {
		t.Fatalf("clear was persisted before Ctrl+S: %#v", got)
	}
	driveSheet(t, m, sheetKey("u"))
	if got := m.cell(1, 0); got != (spreadsheet.Cell{Value: "hello"}) {
		t.Fatalf("buffer undo cell = %#v", got)
	}
	if !strings.Contains(m.View(), "hello") || !strings.Contains(m.status, "Undid") {
		t.Fatalf("undo not reflected: status %q\n%s", m.status, m.View())
	}
	driveSheet(t, m, sheetKey("ctrl+r"))
	if got := m.cell(1, 0); got != (spreadsheet.Cell{}) {
		t.Fatalf("buffer redo cell = %#v", got)
	}
	driveSheet(t, m, sheetKey("u"))
}

func TestSpreadsheetShiftEnterContinuesDownColumn(t *testing.T) {
	m, service, _, path := newSheetModel(t, [][]spreadsheet.Cell{
		{{}, {Value: "old"}},
		{{}, {Formula: "=1+2"}},
	})
	driveSheet(t, m, sheetKey("right"))
	driveSheet(t, m, sheetKey("enter"))
	m.input.SetValue("first")
	_, _ = m.Update(shiftEnterMsg{})
	if got := m.cell(0, 1); got.Value != "first" {
		t.Fatalf("staged cell = %#v", got)
	}
	if !m.editing || m.row != 1 || m.column != 1 || m.input.Value() != "=1+2" || !m.input.Focused() {
		t.Fatalf("next edit: row=%d column=%d editing=%t input=%q", m.row, m.column, m.editing, m.input.Value())
	}
	m.input.SetValue("second")
	driveSheet(t, m, sheetKey("shift+enter"))
	if !m.editing || m.row != 2 || m.column != 1 || m.input.Value() != "" || m.cell(1, 1).Value != "second" {
		t.Fatalf("continued edit: row=%d column=%d editing=%t input=%q", m.row, m.column, m.editing, m.input.Value())
	}
	if got := storedCell(t, service, path, 1, 2); got.Value != "old" {
		t.Fatalf("staged edit was persisted before saving: %#v", got)
	}
	driveSheet(t, m, sheetKey("esc"))
	driveSheet(t, m, sheetKey("ctrl+s"))
	if got := storedCell(t, service, path, 2, 2); got.Value != "second" {
		t.Fatalf("saved cell = %#v", got)
	}
}

func TestSpreadsheetAutosaveRestartsAroundPDFExport(t *testing.T) {
	h := newHarness(t, artifact.SpreadsheetKind)
	m := h.m.sheet
	if !m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
		return spreadsheet.SetCellInBook(book, spreadsheet.SetCellOptions{
			Sheet: book.Sheets[0].Name, Row: 1, Column: 1, Cell: spreadsheet.Cell{Value: "draft"},
		})
	}) {
		t.Fatal(m.errMessage)
	}
	m.syncAutosave()
	if !m.autosaveActive {
		t.Fatal("autosave did not start for dirty workbook")
	}

	existing := filepath.Join(t.TempDir(), "existing.pdf")
	if err := os.WriteFile(existing, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := m.exportPDF(existing, false)
	if cmd == nil || !m.pending || m.autosaveActive {
		t.Fatal("export did not pause autosave")
	}
	message := cmd().(spreadsheetPDFExportedMsg)
	m.spreadsheetPDFExported(message)
	if m.pdfPrompt != promptPDFOverwrite || !m.autosaveActive {
		t.Fatalf("overwrite prompt state: prompt=%v autosave=%v", m.pdfPrompt, m.autosaveActive)
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.pdfPrompt != promptNone || !m.autosaveActive {
		t.Fatalf("cancelled overwrite state: prompt=%v autosave=%v", m.pdfPrompt, m.autosaveActive)
	}

	cmd = m.exportPDF(filepath.Join(t.TempDir(), "cancelled.pdf"), false)
	if cmd == nil || m.autosaveActive {
		t.Fatal("second export did not pause autosave")
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.autosaveActive || m.pending {
		t.Fatalf("cancelled export state: pending=%v autosave=%v", m.pending, m.autosaveActive)
	}
}

func TestSpreadsheetShiftEnterScrollsAndStopsAtLastRow(t *testing.T) {
	m, _, _, _ := newSheetModel(t, nil)
	for _, row := range []int{m.visibleRows() - 1, spreadsheet.MaxRows - 1} {
		m.row = row
		m.scrollToSelection()
		driveSheet(t, m, sheetKey("enter"))
		m.input.SetValue("value")
		driveSheet(t, m, sheetKey("shift+enter"))
		if !m.editing || m.row != min(row+1, spreadsheet.MaxRows-1) || m.column != 0 {
			t.Fatalf("row=%d: selection=%d,%d editing=%t", row, m.row, m.column, m.editing)
		}
		if m.row < m.rowOffset || m.row >= m.rowOffset+m.visibleRows() {
			t.Fatalf("selection %d outside visible rows starting at %d", m.row, m.rowOffset)
		}
		driveSheet(t, m, sheetKey("esc"))
	}
}

func TestSpreadsheetAltEnterContinuesDownColumn(t *testing.T) {
	m, _, _, _ := newSheetModel(t, nil)
	driveSheet(t, m, sheetKey("enter"))
	m.input.SetValue("value")
	driveSheet(t, m, sheetKey("alt+enter"))
	if !m.editing || m.row != 1 || m.column != 0 || m.cell(0, 0).Value != "value" {
		t.Fatalf("fallback shortcut: row=%d column=%d editing=%t", m.row, m.column, m.editing)
	}
}

type loadedSpreadsheetInputModel struct {
	*spreadsheetModel
}

func (loadedSpreadsheetInputModel) Init() tea.Cmd { return nil }

func TestSpreadsheetShiftEnterTerminalSequences(t *testing.T) {
	for _, sequence := range []string{"\x1b[13;2u", "\x1b[27;2;13~"} {
		t.Run(fmt.Sprintf("%q", sequence), func(t *testing.T) {
			m, _, _, _ := newSheetModel(t, nil)
			driveSheet(t, m, sheetKey("enter"))
			m.input.SetValue("typed")
			program := tea.NewProgram(loadedSpreadsheetInputModel{m},
				tea.WithInput(strings.NewReader(sequence+"\x03")),
				tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler(),
			)
			if _, err := program.Run(); err != nil {
				t.Fatal(err)
			}
			if !m.editing || m.row != 1 || m.cell(0, 0).Value != "typed" {
				t.Fatalf("terminal shortcut: row=%d editing=%t cell=%#v", m.row, m.editing, m.cell(0, 0))
			}
		})
	}
}

func TestSpreadsheetInvalidFormulaKeepsDraft(t *testing.T) {
	for _, key := range []string{"enter", "shift+enter"} {
		t.Run(key, func(t *testing.T) {
			m, service, _, id := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "1"}}})
			driveSheet(t, m, sheetKey("right"))
			driveSheet(t, m, sheetKey("enter"))
			driveSheet(t, m, sheetKey("=B1+"))
			driveSheet(t, m, sheetKey(key))
			if !m.editing || m.row != 0 || m.column != 1 || m.input.Value() != "=B1+" || !m.dirty() || !strings.Contains(m.errMessage, "Edit failed") {
				t.Fatalf("state after invalid formula: editing %t dirty %t draft %q err %q", m.editing, m.dirty(), m.input.Value(), m.errMessage)
			}
			if got := storedCell(t, service, id, 1, 2); got != (spreadsheet.Cell{}) {
				t.Fatalf("invalid formula was stored: %#v", got)
			}
			driveSheet(t, m, sheetKey("esc"))
			if m.editing {
				t.Fatal("esc did not cancel editing")
			}
			driveSheet(t, m, sheetKey("q"))
			if got := storedCell(t, service, id, 1, 2); got != (spreadsheet.Cell{}) {
				t.Fatalf("cancelled edit was stored: %#v", got)
			}
		})
	}
}

func TestSpreadsheetTypingQWhileEditingDoesNotQuit(t *testing.T) {
	m, service, _, id := newSheetModel(t, [][]spreadsheet.Cell{{{}}})
	driveSheet(t, m, sheetKey("enter"))
	if msg := driveSheet(t, m, sheetKey("q")); msg != nil {
		t.Fatalf("q while editing produced %#v", msg)
	}
	if !m.dirty() {
		t.Fatal("active cell input was not considered dirty")
	}
	driveSheet(t, m, sheetKey("ctrl+s"))
	if m.editing || m.dirty() {
		t.Fatalf("Ctrl+S did not commit pending cell input: editing=%t dirty=%t", m.editing, m.dirty())
	}
	if got := storedCell(t, service, id, 1, 1); got != (spreadsheet.Cell{Value: "q"}) {
		t.Fatalf("saved cell = %#v", got)
	}
	if _, ok := driveSheet(t, m, sheetKey("esc")).(tea.QuitMsg); !ok {
		t.Fatal("esc while browsing did not quit")
	}
}

func TestSpreadsheetDirtyCloseRequiresConfirmation(t *testing.T) {
	m, service, _, path := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "saved"}}})
	driveSheet(t, m, sheetKey("enter"))
	driveSheet(t, m, sheetKey("ctrl+u"))
	driveSheet(t, m, sheetKey("unsaved"))
	if !m.dirty() {
		t.Fatal("active cell input was not considered dirty")
	}
	driveSheet(t, m, sheetKey("enter"))
	if !m.dirty() {
		t.Fatal("staged edit was not dirty")
	}
	if msg := driveSheet(t, m, sheetKey("esc")); msg != nil {
		t.Fatalf("first esc unexpectedly quit: %#v", msg)
	}
	if !m.closeWarning || !strings.Contains(m.status, "Unsaved changes") {
		t.Fatalf("dirty close warning missing: warning=%t status=%q", m.closeWarning, m.status)
	}
	stored, err := service.Get(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Sheets[0].Rows[0][0].Value != "saved" {
		t.Fatalf("unsaved cell was persisted: %#v", stored.Sheets[0].Rows[0][0])
	}
	if _, ok := driveSheet(t, m, sheetKey("q")).(tea.QuitMsg); !ok {
		t.Fatal("second q did not confirm discard and quit")
	}
}

func TestSpreadsheetSaveAndCloseDeleteRecoveryDraft(t *testing.T) {
	m, _, repository, path := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "original"}}})
	m.recoveryStore = repository
	draft := recovery.Draft{
		Path: path, Kind: string(artifact.SpreadsheetKind),
		UpdatedAt: time.Now().UTC(), Data: json.RawMessage(`{"dirty":true}`),
	}
	if err := repository.SaveRecovery(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	m.draftStored = true
	if !m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
		return spreadsheet.SetCellInBook(book, spreadsheet.SetCellOptions{
			Sheet: "Sheet1", Row: 1, Column: 1, Cell: spreadsheet.Cell{Value: "saved"},
		})
	}) {
		t.Fatalf("buffered edit failed: %s", m.errMessage)
	}
	driveSheet(t, m, sheetKey("ctrl+s"))
	if _, found, err := repository.LoadRecovery(context.Background(), path); err != nil || found {
		t.Fatalf("draft remained after save: found %t, err %v", found, err)
	}

	if err := repository.SaveRecovery(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	m.draftStored = true
	if !m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
		return spreadsheet.SetCellInBook(book, spreadsheet.SetCellOptions{
			Sheet: "Sheet1", Row: 1, Column: 1, Cell: spreadsheet.Cell{Value: "closing"},
		})
	}) {
		t.Fatalf("second buffered edit failed: %s", m.errMessage)
	}
	if msg := driveSheet(t, m, sheetKey("q")); msg != nil {
		t.Fatalf("dirty spreadsheet closed without confirmation: %#v", msg)
	}
	if _, ok := driveSheet(t, m, sheetKey("q")).(tea.QuitMsg); !ok {
		t.Fatal("confirmed close did not quit")
	}
	if _, found, err := repository.LoadRecovery(context.Background(), path); err != nil || found {
		t.Fatalf("draft remained after normal close: found %t, err %v", found, err)
	}
}

func TestSpreadsheetCloseReportsRecoveryCleanupFailure(t *testing.T) {
	m, _, repository, path := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "original"}}})
	m.recoveryStore = failingDeleteStore{Store: repository}
	m.draftStored = true
	msg := driveSheet(t, m, sheetKey("q"))
	if _, ok := msg.(tea.QuitMsg); ok {
		t.Fatal("spreadsheet quit despite recovery cleanup failure")
	}
	if !strings.Contains(m.errMessage, "disk is read-only") {
		t.Fatalf("cleanup error was not shown: %q", m.errMessage)
	}
	if _, found, err := repository.LoadRecovery(context.Background(), path); err != nil || found {
		t.Fatalf("unexpected recovery draft state: found %t, err %v", found, err)
	}
}

func TestSpreadsheetCleanUndoRedoUsesServiceHistory(t *testing.T) {
	m, service, _, path := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "before"}}})
	driveSheet(t, m, sheetKey("enter"))
	driveSheet(t, m, sheetKey("ctrl+u"))
	driveSheet(t, m, sheetKey("3"))
	driveSheet(t, m, sheetKey("enter"))
	driveSheet(t, m, sheetKey("right"))
	driveSheet(t, m, sheetKey("enter"))
	driveSheet(t, m, sheetKey("ctrl+u"))
	driveSheet(t, m, sheetKey("=A1*2"))
	driveSheet(t, m, sheetKey("enter"))
	driveSheet(t, m, sheetKey("ctrl+s"))
	if m.dirty() || m.pending || len(m.bufferUndo) != 0 || len(m.bufferRedo) != 0 {
		t.Fatalf("successful save did not clear the local buffer history: dirty=%t pending=%t undo=%d redo=%d err=%q",
			m.dirty(), m.pending, len(m.bufferUndo), len(m.bufferRedo), m.errMessage)
	}

	driveSheet(t, m, sheetKey("u"))
	stored, err := service.Get(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Sheets[0].Rows[0][0].Value != "before" || len(stored.Sheets[0].Rows[0]) != 1 {
		t.Fatalf("service undo did not revert the complete save: %#v", stored.Sheets)
	}
	driveSheet(t, m, sheetKey("ctrl+r"))
	stored, err = service.Get(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Sheets[0].Rows[0][0].Value != "3" || stored.Sheets[0].Rows[0][1].Formula != "=A1*2" {
		t.Fatalf("service redo did not restore the complete save: %#v", stored.Sheets)
	}
}

func TestSpreadsheetBufferUndoRespectsLimit(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{}}, {{}}})
	m.undoLimit = 1
	for row, value := range []string{"one", "two"} {
		if row > 0 {
			driveSheet(t, m, sheetKey("down"))
		}
		driveSheet(t, m, sheetKey("enter"))
		driveSheet(t, m, sheetKey("ctrl+u"))
		driveSheet(t, m, sheetKey(value))
		driveSheet(t, m, sheetKey("enter"))
	}
	if len(m.bufferUndo) != 1 {
		t.Fatalf("buffer undo depth = %d, want 1", len(m.bufferUndo))
	}
	driveSheet(t, m, sheetKey("u"))
	if m.cell(1, 0).Value != "" || m.cell(0, 0).Value != "one" {
		t.Fatalf("undo did not restore most recent retained state: %#v", m.book.Sheets[0].Rows)
	}
	driveSheet(t, m, sheetKey("u"))
	if m.cell(0, 0).Value != "one" {
		t.Fatalf("undo crossed the configured limit: %#v", m.book.Sheets[0].Rows)
	}
}

func TestSpreadsheetResizeKeepsSelectionVisible(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "top"}}})
	for range 30 {
		driveSheet(t, m, sheetKey("down"))
	}
	for range 12 {
		driveSheet(t, m, sheetKey("right"))
	}
	if m.rowOffset == 0 || m.columnOffset == 0 {
		t.Fatalf("offsets = %d,%d", m.rowOffset, m.columnOffset)
	}
	view := m.View()
	if !strings.Contains(view, "31") || !strings.Contains(view, " M ") || strings.Contains(view, "top") {
		t.Fatalf("scrolled view wrong:\n%s", view)
	}
	driveSheet(t, m, tea.WindowSizeMsg{Width: 40, Height: 8})
	if m.row < m.rowOffset || m.row >= m.rowOffset+m.visibleRows() ||
		m.column < m.columnOffset || m.column >= m.columnOffset+m.visibleColumns() {
		t.Fatalf("selection %d,%d not visible after resize (offset %d,%d)", m.row, m.column, m.rowOffset, m.columnOffset)
	}
	for _, line := range strings.Split(m.View(), "\n") {
		if w := len([]rune(line)); w > 40*4 {
			t.Fatalf("line too long after resize: %q", line)
		}
	}
	driveSheet(t, m, tea.WindowSizeMsg{Width: 10, Height: 3})
	if !strings.Contains(m.View(), "too small") {
		t.Fatalf("tiny view = %q", m.View())
	}
}

func TestSpreadsheetCancelIgnoresLateResult(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "x"}}})
	cmd := m.load()
	if !m.pending {
		t.Fatal("reload not pending")
	}
	driveSheet(t, m, sheetKey("esc"))
	if m.pending || m.status != "Cancelled" {
		t.Fatalf("after esc: pending %t status %q", m.pending, m.status)
	}
	m.Update(spreadsheetLoadedMsg{seq: m.seq - 1, err: context.Canceled})
	_ = cmd
	if m.errMessage != "" || m.status != "Cancelled" || !m.loaded {
		t.Fatalf("late result changed state: status %q err %q", m.status, m.errMessage)
	}

	m.load()
	if _, ok := driveSheet(t, m, sheetKey("ctrl+c")).(tea.QuitMsg); !ok || m.pending || m.cancelOperation != nil {
		t.Fatalf("ctrl+c while pending did not cancel and quit")
	}
}

func TestSpreadsheetCancelledContextReportsCancelled(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "x"}}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.newOperationContext = func() (context.Context, context.CancelFunc) { return context.WithCancel(ctx) }
	driveSheet(t, m, sheetKey("r"))
	if m.pending || m.errMessage != "" || m.status != "Cancelled" {
		t.Fatalf("cancelled load: pending %t status %q err %q", m.pending, m.status, m.errMessage)
	}
}

func TestSpreadsheetLoadErrorAllowsRetryAndQuit(t *testing.T) {
	repository := newTestRepository(t)
	m := newSpreadsheetModel(spreadsheet.NewService(repository, 10), filepath.Join(t.TempDir(), "missing.md"), Theme{}, 10)
	driveSheet(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	driveSheet(t, m, m.Init()())
	if m.loaded || m.errMessage == "" || !strings.Contains(m.View(), "Error:") {
		t.Fatalf("load error not shown: %q\n%s", m.errMessage, m.View())
	}
	if msg := driveSheet(t, m, sheetKey("enter")); msg != nil || m.editing {
		t.Fatal("editing allowed without a loaded spreadsheet")
	}
	if _, ok := driveSheet(t, m, sheetKey("q")).(tea.QuitMsg); !ok {
		t.Fatal("q did not quit")
	}
}

func TestSpreadsheetTabSwitchesSheets(t *testing.T) {
	m, service, _, id := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "first"}}})
	if _, err := service.AddSheet(context.Background(), id, "Second"); err != nil {
		t.Fatal(err)
	}
	driveSheet(t, m, sheetKey("r"))
	driveSheet(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.sheet != 1 || !strings.Contains(m.View(), "Second") {
		t.Fatalf("tab did not switch sheet:\n%s", m.View())
	}
	driveSheet(t, m, sheetKey("enter"))
	driveSheet(t, m, sheetKey("second"))
	driveSheet(t, m, sheetKey("enter"))
	driveSheet(t, m, sheetKey("ctrl+s"))
	book, err := service.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if book.Sheets[1].Rows[0][0].Value != "second" || book.Sheets[0].Rows[0][0].Value != "first" || m.sheet != 1 {
		t.Fatalf("sheet edit went to wrong sheet: %#v (current %d)", book.Sheets, m.sheet)
	}
}

func TestSpreadsheetOpenedBeforeFirstResizeUsesTerminalSize(t *testing.T) {
	for _, kind := range []artifact.Kind{artifact.SpreadsheetKind, artifact.PresentationKind} {
		h := newHarness(t, "")
		var err error
		if kind == artifact.SpreadsheetKind {
			_, err = h.sheets.Create(context.Background(), h.path, nil)
		} else {
			_, err = h.decks.Create(context.Background(), h.path, "# Talk\n\n## Slide")
		}
		if err != nil {
			t.Fatal(err)
		}
		model := NewModel(Config{
			Path: h.path, Kind: kind, Notes: h.notes, Documents: h.docs,
			Spreadsheets: h.sheets, Presentations: h.decks, Recovery: h.repo,
		})
		h.m = &model
		h.run(h.m.Init())
		h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
		if view := h.m.View(); strings.Contains(view, "too small") {
			t.Fatalf("%s opened before the first resize stayed too small:\n%s", kind, view)
		}
	}
}

func TestSpreadsheetUpdateAsyncMessages(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "cell1"}}})

	// 1. spreadsheetLoadedMsg seq mismatch
	m.seq = 10
	model, cmd := m.Update(spreadsheetLoadedMsg{seq: 5, book: spreadsheet.Spreadsheet{Artifact: artifact.Artifact{Title: "Wrong"}}})
	if cmd != nil || m.book.Title == "Wrong" {
		t.Fatalf("expected spreadsheetLoadedMsg with mismatched seq to be ignored, got model=%v, cmd=%v", model, cmd)
	}

	// 2. spreadsheetSavedMsg seq mismatch
	model, cmd = m.Update(spreadsheetSavedMsg{seq: 5})
	if cmd != nil {
		t.Fatalf("expected spreadsheetSavedMsg with mismatched seq to be ignored, got cmd=%v", cmd)
	}

	// 3. spreadsheetSavedMsg with error
	m.seq = 11
	m.pending = true
	saveErrMsg := spreadsheetSavedMsg{seq: 11, err: errors.New("disk write failure")}
	model, cmd = m.Update(saveErrMsg)
	if cmd != nil || m.pending || !strings.Contains(m.errMessage, "disk write failure") {
		t.Fatalf("spreadsheetSavedMsg error handled incorrectly: pending=%t, errMessage=%q",
			m.pending, m.errMessage)
	}

	// 4. spreadsheetHistoryMsg seq mismatch
	m.seq = 12
	model, cmd = m.Update(spreadsheetHistoryMsg{seq: 5, description: "Undid edit"})
	if cmd != nil {
		t.Fatalf("expected spreadsheetHistoryMsg with mismatched seq to be ignored, got cmd=%v", cmd)
	}

	// 5. spreadsheetHistoryMsg with error
	m.seq = 13
	model, cmd = m.Update(spreadsheetHistoryMsg{seq: 13, err: errors.New("nothing to undo")})
	if cmd != nil || !strings.Contains(m.errMessage, "History failed: nothing to undo") {
		t.Fatalf("spreadsheetHistoryMsg error handled incorrectly: cmd=%v, errMessage=%q", cmd, m.errMessage)
	}

	// 6. spreadsheetHistoryMsg success
	m.seq = 14
	m.errMessage = ""
	model, cmd = m.Update(spreadsheetHistoryMsg{seq: 14, description: "Undid cell change"})
	if cmd == nil || m.loadedStatus != "Undid cell change" {
		t.Fatalf("spreadsheetHistoryMsg success handled incorrectly: cmd=%v, loadedStatus=%q", cmd, m.loadedStatus)
	}
}

func TestSpreadsheetUpdateMouseMsg(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{
		{{Value: "A1"}, {Value: "B1"}},
		{{Value: "A2"}, {Value: "B2"}},
	})

	// 1. Left mouse click on cell grid while browsing
	m.row, m.column = 0, 0
	// Grid starts at Y=4. Row offset 0 means Y=4 is row 0, Y=5 is row 1.
	// X: rowHeaderWidth is len(" 1")+1 = 2+1 = 3. gridX = X - 2.
	// Header end is X = 2 + rowHeaderWidth() + 1 = 6. Column 0 is at X=6..17, Column 1 starts at X=19.
	mouseGridMsg := tea.MouseMsg{
		X:      20,
		Y:      5,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	m.Update(mouseGridMsg)
	if m.row != 1 || m.column != 1 {
		t.Fatalf("mouse click grid selection failed: got row=%d, col=%d, expected row=1, col=1", m.row, m.column)
	}

	// 2. Mouse click ignored if pending
	m.pending = true
	m.Update(tea.MouseMsg{X: 20, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.row != 1 || m.column != 1 {
		t.Fatalf("mouse click should be ignored when pending")
	}
	m.pending = false

	// 3. Mouse click while editing on formula bar (Y=1)
	driveSheet(t, m, sheetKey("enter"))
	if !m.editing {
		t.Fatal("expected editing mode")
	}
	mouseFormulaMsg := tea.MouseMsg{
		X:      15,
		Y:      1,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	m.Update(mouseFormulaMsg)
	// input cursor position should have been updated
	if m.input.Position() < 0 {
		t.Fatalf("expected cursor position >= 0, got %d", m.input.Position())
	}
	driveSheet(t, m, sheetKey("esc"))
}

func TestSpreadsheetUpdateFallbackAndEditShortcuts(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "test"}}})

	// 1. Non-key / non-mouse message while editing=false returns (m, nil)
	type customMsg struct{}
	model, cmd := m.Update(customMsg{})
	if model != m || cmd != nil {
		t.Fatalf("expected non-editing custom message to return (m, nil)")
	}

	// 2. Custom message while editing=true forwards to input
	m.editing = true
	model, cmd = m.Update(customMsg{})
	if model != m {
		t.Fatalf("expected editing custom message to return m")
	}
	m.editing = false

	// 3. ctrl+c while editing cancels pending and returns Quit
	m.editing = true
	m.pending = true
	model, cmd = m.Update(sheetKey("ctrl+c"))
	if m.pending {
		t.Fatalf("expected pending to be cancelled on ctrl+c while editing")
	}
	if cmd == nil {
		t.Fatalf("expected quit cmd on ctrl+c while editing")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg on ctrl+c while editing")
	}
	m.editing = false

	// 4. Formula error cases and #ERR evaluation
	// Empty formula
	m.editing = true
	m.input.SetValue("=")
	if m.commitCell() || m.errMessage != "formula is empty" {
		t.Fatalf("expected 'formula is empty' error, got errMessage=%q", m.errMessage)
	}

	// Oversized formula
	m.editing = true
	longFormula := "=" + strings.Repeat("A1+", 1400)
	m.input.SetValue(longFormula)
	if m.commitCell() || !strings.Contains(m.errMessage, "formula is longer than") {
		t.Fatalf("expected formula length error, got errMessage=%q", m.errMessage)
	}
	m.editing = false

	// Invalid formula evaluation leading to #ERR in displayValue
	m.book.Sheets[0].Rows[0][0] = spreadsheet.Cell{Formula: "=INVALID_FUNC()"}
	m.computeValues()
	if val := m.displayValue(0, 0); val != "#ERR" {
		t.Fatalf("expected #ERR for invalid formula evaluation, got %q", val)
	}
}

func TestSpreadsheetUpdateNavigationAndSheetSwitch(t *testing.T) {
	m, service, _, id := newSheetModel(t, [][]spreadsheet.Cell{
		{{Value: "A1"}, {Value: "B1"}},
		{{Value: "A2"}, {Value: "B2"}},
	})
	if _, err := service.AddSheet(context.Background(), id, "Sheet2"); err != nil {
		t.Fatal(err)
	}
	driveSheet(t, m, sheetKey("r"))

	// Navigation keys: h, j, k, l, pgup, pgdown, home, end
	driveSheet(t, m, sheetKey("j")) // down
	if m.row != 1 {
		t.Fatalf("j key expected row 1, got %d", m.row)
	}
	driveSheet(t, m, sheetKey("l")) // right
	if m.column != 1 {
		t.Fatalf("l key expected col 1, got %d", m.column)
	}
	driveSheet(t, m, sheetKey("k")) // up
	if m.row != 0 {
		t.Fatalf("k key expected row 0, got %d", m.row)
	}
	driveSheet(t, m, sheetKey("h")) // left
	if m.column != 0 {
		t.Fatalf("h key expected col 0, got %d", m.column)
	}

	driveSheet(t, m, sheetKey("pgdown"))
	if m.row <= 0 {
		t.Fatalf("pgdown expected row > 0, got %d", m.row)
	}
	driveSheet(t, m, sheetKey("home"))
	if m.column != 0 {
		t.Fatalf("home expected col 0, got %d", m.column)
	}
	driveSheet(t, m, sheetKey("pgup"))
	if m.row != 0 {
		t.Fatalf("pgup expected row 0, got %d", m.row)
	}
	driveSheet(t, m, sheetKey("end"))
	if m.column != 1 {
		t.Fatalf("end expected col 1, got %d", m.column)
	}

	// shift+tab switches sheet backwards
	if m.sheet != 0 {
		t.Fatalf("expected sheet 0 initially")
	}
	driveSheet(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.sheet != 1 {
		t.Fatalf("shift+tab expected sheet 1 (cycling backwards), got %d", m.sheet)
	}

	// 'e' key begins edit mode
	driveSheet(t, m, sheetKey("e"))
	if !m.editing {
		t.Fatalf("'e' key expected to start editing mode")
	}
	driveSheet(t, m, sheetKey("esc"))
}

func TestSpreadsheetRedoClampsSelectionAfterSheetDeletion(t *testing.T) {
	m, service, _, path := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "first"}}})
	if _, err := service.AddSheet(context.Background(), path, "Sheet2"); err != nil {
		t.Fatal(err)
	}
	book, err := service.Get(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	m.setBook(book)

	driveSheet(t, m, sheetKey("tab"))
	if m.sheet != 1 {
		t.Fatalf("active sheet = %d, want 1", m.sheet)
	}
	driveSheet(t, m, sheetKey("d"))
	driveSheet(t, m, sheetKey("s"))
	driveSheet(t, m, sheetKey("u"))
	driveSheet(t, m, sheetKey("tab"))
	if m.sheet != 1 {
		t.Fatalf("active sheet after undo = %d, want 1", m.sheet)
	}
	driveSheet(t, m, sheetKey("ctrl+r"))
	if m.sheet != 0 || len(m.book.Sheets) != 1 {
		t.Fatalf("redo selection = %d with %d sheets, want sheet 0 with one sheet", m.sheet, len(m.book.Sheets))
	}
}

func TestSpreadsheetViewEditingAndHelp(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "test"}}})
	driveSheet(t, m, sheetKey("enter"))
	if !m.editing {
		t.Fatal("expected model to be in editing mode")
	}
	view := m.View()
	if !strings.Contains(view, "A1 >") {
		t.Fatalf("editing formula bar missing prompt:\n%s", view)
	}
	if !strings.Contains(view, "enter stage • ctrl+s save • esc cancel • =formula • 'literal") {
		t.Fatalf("editing help string missing:\n%s", view)
	}
}

func TestSpreadsheetViewUnloadedAndError(t *testing.T) {
	repository := newTestRepository(t)
	m := newSpreadsheetModel(spreadsheet.NewService(repository, 10), "", Theme{}, 10)
	m.width, m.height = 80, 20
	view := m.View()
	if !strings.Contains(view, "Spreadsheet") {
		t.Fatalf("unloaded title missing 'Spreadsheet':\n%s", view)
	}
	if !strings.Contains(view, "r retry • esc/q quit") {
		t.Fatalf("unloaded help string missing:\n%s", view)
	}

	m.errMessage = "custom failure"
	viewWithError := m.View()
	if !strings.Contains(viewWithError, "Error: custom failure") {
		t.Fatalf("error message missing from status line:\n%s", viewWithError)
	}
}

func TestSpreadsheetViewMultipleSheetsTitle(t *testing.T) {
	m, service, _, id := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "sheet1"}}})
	if _, err := service.AddSheet(context.Background(), id, "CustomSheet"); err != nil {
		t.Fatal(err)
	}
	driveSheet(t, m, sheetKey("r"))
	view := m.View()
	if !strings.Contains(view, "— Sheet1") {
		t.Fatalf("multi-sheet title missing sheet suffix:\n%s", view)
	}

	driveSheet(t, m, tea.KeyMsg{Type: tea.KeyTab})
	view2 := m.View()
	if !strings.Contains(view2, "— CustomSheet") {
		t.Fatalf("switched sheet title missing '— CustomSheet':\n%s", view2)
	}
}

func TestSpreadsheetViewMaxRowsEdge(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "cell"}}})
	m.rowOffset = spreadsheet.MaxRows - 2
	m.height = 30 // visible rows extends past MaxRows
	view := m.View()
	if view == "" {
		t.Fatal("empty view returned for MaxRows edge case")
	}
}

func TestSpreadsheetUpdateWindowSizeMsg(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "A1"}}})

	model, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if model != m || cmd != nil {
		t.Fatalf("expected (m, nil) from WindowSizeMsg, got model=%v, cmd=%v", model, cmd)
	}
	if m.width != 80 || m.height != 24 {
		t.Fatalf("expected width 80, height 24, got %d, %d", m.width, m.height)
	}
	if m.input.Width != 68 {
		t.Fatalf("expected input width 68, got %d", m.input.Width)
	}
}

func TestSpreadsheetUpdateLoadedMsg(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "A1"}}})

	// 1. Success case with loadedStatus transition
	m.seq = 5
	m.pending = true
	m.loadedStatus = "Reloaded"
	book := spreadsheet.Spreadsheet{
		Artifact: artifact.Artifact{Title: "LoadedBook"},
		Sheets:   []spreadsheet.Sheet{{Name: "Sheet1", Rows: [][]spreadsheet.Cell{{{Value: "New"}}}}},
	}
	model, cmd := m.Update(spreadsheetLoadedMsg{seq: 5, book: book})
	if model != m || cmd != nil {
		t.Fatalf("expected (m, nil), got model=%v, cmd=%v", model, cmd)
	}
	if m.status != "Reloaded" || m.loadedStatus != "" {
		t.Fatalf("expected status 'Reloaded', got status=%q, loadedStatus=%q", m.status, m.loadedStatus)
	}
	if m.pending {
		t.Fatalf("expected pending false")
	}

	// 2. Error case with spreadsheet.ErrNotFound
	m.seq = 6
	m.pending = true
	model, cmd = m.Update(spreadsheetLoadedMsg{seq: 6, err: spreadsheet.ErrNotFound})
	if model != m || cmd != nil {
		t.Fatalf("expected (m, nil), got model=%v, cmd=%v", model, cmd)
	}
	if !strings.Contains(m.errMessage, "Load failed: spreadsheet not found") {
		t.Fatalf("expected not found error message, got %q", m.errMessage)
	}
}

func TestSpreadsheetUpdateSavedMsgSuccess(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "A1"}}})

	m.seq = 7
	m.pending = true
	m.row, m.column = 0, 1
	book := cloneSpreadsheetState(m.book)
	book.Sheets[0].Rows[0] = append(book.Sheets[0].Rows[0], spreadsheet.Cell{Value: "saved"})
	model, cmd := m.Update(spreadsheetSavedMsg{seq: 7, book: book})
	if model != m || cmd != nil {
		t.Fatalf("expected (m, nil), got model=%v, cmd=%v", model, cmd)
	}
	if m.status != "Saved spreadsheet" {
		t.Fatalf("expected status 'Saved spreadsheet', got %q", m.status)
	}
	if m.pending || m.dirty() || m.book.Sheets[0].Rows[0][1].Value != "saved" {
		t.Fatalf("save did not update clean workbook state")
	}
}

func TestSpreadsheetSavePreservesRangeSelection(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{
		{{Value: "1"}, {Value: "2"}},
		{{Value: "3"}, {Value: "4"}},
	})
	if !m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
		return spreadsheet.SetCellInBook(book, spreadsheet.SetCellOptions{
			Sheet: "Sheet1", Row: 1, Column: 1, Cell: spreadsheet.Cell{Value: "saved"},
		})
	}) {
		t.Fatalf("edit failed: %s", m.errMessage)
	}
	var copied string
	m.clipboardWrite = func(text string) error {
		copied = text
		return nil
	}
	driveSheet(t, m, sheetKey("v"))
	driveSheet(t, m, sheetKey("right"))
	driveSheet(t, m, sheetKey("down"))
	driveSheet(t, m, sheetKey("ctrl+s"))
	if !m.selecting || m.selectionRow != 0 || m.selectionColumn != 0 {
		t.Fatalf("save lost selection: active=%t anchor=%d,%d", m.selecting, m.selectionRow, m.selectionColumn)
	}
	driveSheet(t, m, sheetKey("y"))
	if copied != "saved\t2\n3\t4\n" {
		t.Fatalf("copied range after save = %q", copied)
	}
}

func TestSpreadsheetUpdateMouseMsgEdgeCases(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{
		{{Value: "A1"}, {Value: "B1"}},
	})
	m.width, m.height = 80, 20
	m.row, m.column = 0, 0

	// Mouse release (not press) should be ignored
	m.Update(tea.MouseMsg{X: 10, Y: 5, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if m.row != 0 || m.column != 0 {
		t.Fatalf("expected row 0, col 0 on release")
	}

	// Mouse right click should be ignored
	m.Update(tea.MouseMsg{X: 10, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	if m.row != 0 || m.column != 0 {
		t.Fatalf("expected row 0, col 0 on right click")
	}

	// Mouse click on header row (Y = 1) while browsing (not editing)
	m.Update(tea.MouseMsg{X: 10, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.row != 0 || m.column != 0 {
		t.Fatalf("expected row 0, col 0 on header click while browsing")
	}

	// Mouse click below visible grid (Y >= 4 + visibleRows)
	m.Update(tea.MouseMsg{X: 10, Y: 100, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.row != 0 || m.column != 0 {
		t.Fatalf("expected row 0, col 0 on click below grid")
	}

	// Mouse click on row header (X <= rowHeaderWidth + 2)
	m.Update(tea.MouseMsg{X: 2, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.row != 0 || m.column != 0 {
		t.Fatalf("expected row 0, col 0 on row header click")
	}
}

func TestSpreadsheetView_TerminalTooSmall(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "data"}}})

	// Width too small
	m.width, m.height = 19, 20
	if view := m.View(); view != "Terminal too small. Press q to quit." {
		t.Fatalf("expected terminal too small message for width < 20, got:\n%s", view)
	}

	// Height too small (spreadsheetChromeLines = 7, so height < 8)
	m.width, m.height = 80, 7
	if view := m.View(); view != "Terminal too small. Press q to quit." {
		t.Fatalf("expected terminal too small message for height < 8, got:\n%s", view)
	}

	// Boundary size (width = 20, height = 8) should show normal view
	m.width, m.height = 20, 8
	if view := m.View(); strings.Contains(view, "Terminal too small") {
		t.Fatalf("unexpected terminal too small message at boundary size 20x8:\n%s", view)
	}
}

func TestSpreadsheetView_HeaderAndStatus(t *testing.T) {
	m, service, _, id := newSheetModel(t, [][]spreadsheet.Cell{
		{{Value: "A1"}, {Value: "B1"}},
		{{Value: "A2"}, {Value: "B2"}},
	})

	// Default status line when status is empty and spreadsheet is loaded should display sheet dimensions "2×2"
	m.status = ""
	view := m.View()
	if !strings.Contains(view, "2×2") {
		t.Fatalf("expected status line to show dimensions '2×2', view:\n%s", view)
	}

	// Header shows path when m.path is set
	if !strings.Contains(view, m.path) {
		t.Fatalf("expected header to contain file path %q, view:\n%s", m.path, view)
	}

	// Single sheet titled "Sheet1" does not append "— Sheet1"
	if strings.Contains(view, "— Sheet1") {
		t.Fatalf("single Sheet1 workbook should not append sheet suffix, view:\n%s", view)
	}
	if strings.Contains(view, "Sheet 1/1") {
		t.Fatalf("single-sheet workbook should not show a sheet indicator, view:\n%s", view)
	}

	// Add second sheet and verify suffix appears
	if _, err := service.AddSheet(context.Background(), id, "Sheet2"); err != nil {
		t.Fatal(err)
	}
	driveSheet(t, m, sheetKey("r"))
	viewMulti := m.View()
	if !strings.Contains(viewMulti, "— Sheet1") {
		t.Fatalf("multi-sheet workbook should append sheet suffix '— Sheet1', view:\n%s", viewMulti)
	}
	viewLines := strings.Split(viewMulti, "\n")
	statusLine := viewLines[len(viewLines)-1]
	indicator := "Sheet 1/2"
	if index := strings.Index(statusLine, indicator); index < 0 ||
		ansi.StringWidth(statusLine[:index]) != (m.width-len(indicator))/2 {
		t.Fatalf("sheet indicator is not centered:\n%s", statusLine)
	}
	if !strings.Contains(statusLine, "2×2") {
		t.Fatalf("sheet dimensions missing beside centered indicator: %q", statusLine)
	}
	driveSheet(t, m, sheetKey("tab"))
	viewLines = strings.Split(m.View(), "\n")
	statusLine = viewLines[len(viewLines)-1]
	if !strings.Contains(statusLine, "Sheet 2/2") {
		t.Fatalf("sheet indicator did not follow active sheet: %q", statusLine)
	}
}

func TestSpreadsheetView_EditingAndHelp(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "Value1"}}})

	// Browsing help message
	viewBrowsing := m.View()
	if !strings.Contains(viewBrowsing, "arrows move • enter edit • ctrl+s save • del clear") {
		t.Fatalf("expected browsing help text, got:\n%s", viewBrowsing)
	}

	// Editing formula bar and help message
	driveSheet(t, m, sheetKey("enter"))
	if !m.editing {
		t.Fatal("expected model to enter editing state")
	}
	viewEditing := m.View()
	if !strings.Contains(viewEditing, "A1 >") {
		t.Fatalf("expected editing formula bar prompt 'A1 >', got:\n%s", viewEditing)
	}
	if !strings.Contains(viewEditing, "enter stage • ctrl+s save • esc cancel • =formula • 'literal") {
		t.Fatalf("expected editing help text, got:\n%s", viewEditing)
	}
	driveSheet(t, m, sheetKey("esc"))

	// Unloaded help message
	repo := newTestRepository(t)
	mUnloaded := newSpreadsheetModel(spreadsheet.NewService(repo, 10), "missing.md", Theme{}, 10)
	mUnloaded.width, mUnloaded.height = 80, 20
	viewUnloaded := mUnloaded.View()
	if !strings.Contains(viewUnloaded, "r retry • esc/q quit") {
		t.Fatalf("expected unloaded help text, got:\n%s", viewUnloaded)
	}
}

func TestSpreadsheetView_CustomSheetNameAndEmptyPath(t *testing.T) {
	m, service, _, id := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "Value1"}}})

	// Case 1: Single sheet with custom name (not "Sheet1")
	m.book.Sheets[0].Name = "CustomName"
	viewCustomSheet := m.View()
	if !strings.Contains(viewCustomSheet, "— CustomName") {
		t.Fatalf("expected title to contain '— CustomName', got:\n%s", viewCustomSheet)
	}

	// Case 2: Empty path
	m.path = ""
	viewEmptyPath := m.View()
	if strings.Contains(viewEmptyPath, id) {
		t.Fatalf("expected view with empty path to not contain file path, got:\n%s", viewEmptyPath)
	}

	// Case 3: Explicit m.status shown in renderStatus
	m.status = "Saved cell successfully"
	m.errMessage = ""
	viewStatus := m.View()
	if !strings.Contains(viewStatus, "Saved cell successfully") {
		t.Fatalf("expected status 'Saved cell successfully', got:\n%s", viewStatus)
	}

	// Silence unused variable warning if service/id are used
	_ = service
}

func TestSpreadsheetTUI_DeleteShortcuts(t *testing.T) {
	m, service, _, path := newSheetModel(t, [][]spreadsheet.Cell{
		{{Value: "A1"}, {Value: "B1"}},
		{{Value: "A2"}, {Value: "B2"}},
	})
	ctx := context.Background()
	service.AddSheet(ctx, path, "Sheet2")

	// Ensure not loaded (shortcut guarded by !m.loaded)
	m.loaded = false
	driveSheet(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	driveSheet(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})

	m.Init()
	book, _ := service.Get(ctx, path)
	driveSheet(t, m, spreadsheetLoadedMsg{seq: m.seq, book: book})

	// Delete sheet
	m.sheet = 1
	driveSheet(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	driveSheet(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})

	if len(m.book.Sheets) != 1 {
		t.Fatalf("expected 1 sheet, got %d", len(m.book.Sheets))
	}
	storedBook, err := service.Get(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(storedBook.Sheets) != 2 {
		t.Fatalf("sheet deletion was persisted before Ctrl+S: %d sheets", len(storedBook.Sheets))
	}

	if len(m.book.Sheets[0].Rows) != 2 {
		t.Fatalf("expected 2 rows before delete, got %d", len(m.book.Sheets[0].Rows))
	}

	// Delete row
	driveSheet(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	driveSheet(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	if len(m.book.Sheets[0].Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(m.book.Sheets[0].Rows))
	}

	if len(m.book.Sheets[0].Rows[0]) != 2 {
		t.Fatalf("expected 2 columns before delete, got %d", len(m.book.Sheets[0].Rows[0]))
	}

	// Delete column
	driveSheet(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	driveSheet(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})

	if len(m.book.Sheets[0].Rows[0]) != 1 {
		t.Fatalf("expected 1 column, got %d", len(m.book.Sheets[0].Rows[0]))
	}
	driveSheet(t, m, sheetKey("ctrl+s"))
	storedBook, err = service.Get(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(storedBook.Sheets) != 1 || len(storedBook.Sheets[0].Rows) != 1 || len(storedBook.Sheets[0].Rows[0]) != 1 {
		t.Fatalf("saved workbook did not include buffered deletions: %#v", storedBook.Sheets)
	}
}

func TestSpreadsheetTUI_InsertShortcutsAreBuffered(t *testing.T) {
	m, service, _, path := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "A1"}}, {{Value: "A2"}}})
	driveSheet(t, m, sheetKey("i"))
	driveSheet(t, m, sheetKey("r"))
	if len(m.book.Sheets[0].Rows) != 3 {
		t.Fatalf("buffered row count = %d, want 3", len(m.book.Sheets[0].Rows))
	}
	driveSheet(t, m, sheetKey("i"))
	driveSheet(t, m, sheetKey("c"))
	if len(m.book.Sheets[0].Rows[0]) != 2 {
		t.Fatalf("buffered column count = %d, want 2", len(m.book.Sheets[0].Rows[0]))
	}
	stored, err := service.Get(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Sheets[0].Rows) != 2 || len(stored.Sheets[0].Rows[0]) != 1 {
		t.Fatalf("insertions were persisted before Ctrl+S: %#v", stored.Sheets[0].Rows)
	}
	driveSheet(t, m, sheetKey("ctrl+s"))
	stored, err = service.Get(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Sheets[0].Rows) != 3 || len(stored.Sheets[0].Rows[0]) != 2 {
		t.Fatalf("saved workbook missed buffered insertions: %#v", stored.Sheets[0].Rows)
	}
}
