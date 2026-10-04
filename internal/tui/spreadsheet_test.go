package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"example.com/parchment/internal/spreadsheet"
)

// driveSheet sends a message and feeds command results back into the model.
func driveSheet(t *testing.T, m *spreadsheetModel, message tea.Msg) tea.Msg {
	t.Helper()
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
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "delete":
		return tea.KeyMsg{Type: tea.KeyDelete}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

func newSheetModel(t *testing.T, rows [][]spreadsheet.Cell) (*spreadsheetModel, *spreadsheet.Service, *memoryRepository, string) {
	t.Helper()
	ws := openTestWorkspace(t)
	service := spreadsheet.NewService(ws, 10)
	book, err := service.Create(context.Background(), "Budget", rows)
	if err != nil {
		t.Fatal(err)
	}
	m := newSpreadsheetModel(service, ws, book.ID, "budget.md")
	driveSheet(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	if msg := m.Init()(); msg != nil {
		driveSheet(t, m, msg)
	}
	if !m.loaded || m.pending {
		t.Fatalf("model not loaded: loaded %t pending %t err %q", m.loaded, m.pending, m.errMessage)
	}
	return m, service, ws, book.ID
}

func storedCell(t *testing.T, service *spreadsheet.Service, id string, row, column int) spreadsheet.Cell {
	t.Helper()
	book, err := service.Get(context.Background(), id)
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
		if m.editing || m.pending || m.errMessage != "" {
			t.Fatalf("edit %q state: editing %t pending %t err %q", text, m.editing, m.pending, m.errMessage)
		}
	}

	driveSheet(t, m, sheetKey("right"))
	edit("=A1*3")
	if got := storedCell(t, service, id, 1, 2); got != (spreadsheet.Cell{Formula: "=A1*3"}) {
		t.Fatalf("formula cell = %#v", got)
	}
	if !strings.Contains(m.View(), "6") {
		t.Fatalf("evaluated formula missing:\n%s", m.View())
	}

	driveSheet(t, m, sheetKey("down"))
	edit("'=not a formula")
	if got := storedCell(t, service, id, 2, 2); got != (spreadsheet.Cell{Value: "=not a formula"}) {
		t.Fatalf("escaped literal cell = %#v", got)
	}
	driveSheet(t, m, sheetKey("enter"))
	if m.input.Value() != "'=not a formula" {
		t.Fatalf("literal draft = %q", m.input.Value())
	}
	driveSheet(t, m, sheetKey("esc"))

	driveSheet(t, m, sheetKey("left"))
	edit("hello")
	if got := storedCell(t, service, id, 2, 1); got != (spreadsheet.Cell{Value: "hello"}) {
		t.Fatalf("literal cell = %#v", got)
	}

	driveSheet(t, m, sheetKey("delete"))
	if got := storedCell(t, service, id, 2, 1); got != (spreadsheet.Cell{}) {
		t.Fatalf("cleared cell = %#v", got)
	}
	driveSheet(t, m, sheetKey("u"))
	if got := storedCell(t, service, id, 2, 1); got != (spreadsheet.Cell{Value: "hello"}) {
		t.Fatalf("undone cell = %#v", got)
	}
	if !strings.Contains(m.View(), "hello") || !strings.Contains(m.status, "Undid") {
		t.Fatalf("undo not reflected: status %q\n%s", m.status, m.View())
	}
}

func TestSpreadsheetInvalidFormulaKeepsDraft(t *testing.T) {
	m, service, _, id := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "1"}}})
	driveSheet(t, m, sheetKey("right"))
	driveSheet(t, m, sheetKey("enter"))
	driveSheet(t, m, sheetKey("=B1+"))
	driveSheet(t, m, sheetKey("enter"))
	if !m.editing || m.input.Value() != "=B1+" || !strings.Contains(m.errMessage, "Save failed") {
		t.Fatalf("state after invalid formula: editing %t draft %q err %q", m.editing, m.input.Value(), m.errMessage)
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
}

func TestSpreadsheetTypingQWhileEditingDoesNotQuit(t *testing.T) {
	m, service, _, id := newSheetModel(t, [][]spreadsheet.Cell{{{}}})
	driveSheet(t, m, sheetKey("enter"))
	if msg := driveSheet(t, m, sheetKey("q")); msg != nil {
		t.Fatalf("q while editing produced %#v", msg)
	}
	driveSheet(t, m, sheetKey("enter"))
	if got := storedCell(t, service, id, 1, 1); got != (spreadsheet.Cell{Value: "q"}) {
		t.Fatalf("cell = %#v", got)
	}
	if _, ok := driveSheet(t, m, sheetKey("q")).(tea.QuitMsg); !ok {
		t.Fatal("q while browsing did not quit")
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
	ws := openTestWorkspace(t)
	m := newSpreadsheetModel(spreadsheet.NewService(ws, 10), ws, "smissing", "missing.md")
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
	book, err := service.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if book.Sheets[1].Rows[0][0].Value != "second" || book.Sheets[0].Rows[0][0].Value != "first" || m.sheet != 1 {
		t.Fatalf("sheet edit went to wrong sheet: %#v (current %d)", book.Sheets, m.sheet)
	}
}
