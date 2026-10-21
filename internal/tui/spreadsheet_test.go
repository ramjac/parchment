package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/filerepo"
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

func newTestRepository(t *testing.T) *filerepo.Repository {
	t.Helper()
	repository, err := filerepo.New(t.TempDir())
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
	m := newSpreadsheetModel(service, book.Path)
	driveSheet(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	if msg := m.Init()(); msg != nil {
		driveSheet(t, m, msg)
	}
	if !m.loaded || m.pending {
		t.Fatalf("model not loaded: loaded %t pending %t err %q", m.loaded, m.pending, m.errMessage)
	}
	return m, service, repository, book.Path
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
	if _, ok := driveSheet(t, m, sheetKey("esc")).(tea.QuitMsg); !ok {
		t.Fatal("esc while browsing did not quit")
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
	m := newSpreadsheetModel(spreadsheet.NewService(repository, 10), filepath.Join(t.TempDir(), "missing.md"))
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

	// 2. spreadsheetCellSavedMsg seq mismatch
	model, cmd = m.Update(spreadsheetCellSavedMsg{seq: 5, draft: "test"})
	if cmd != nil {
		t.Fatalf("expected spreadsheetCellSavedMsg with mismatched seq to be ignored, got cmd=%v", cmd)
	}

	// 3. spreadsheetCellSavedMsg with error
	m.seq = 11
	m.editing = false
	saveErrMsg := spreadsheetCellSavedMsg{
		seq:    11,
		row:    2,
		column: 3,
		draft:  "invalid draft",
		err:    errors.New("disk write failure"),
	}
	model, cmd = m.Update(saveErrMsg)
	if cmd == nil || !m.editing || m.row != 2 || m.column != 3 || !strings.Contains(m.errMessage, "disk write failure") {
		t.Fatalf("spreadsheetCellSavedMsg error handled incorrectly: editing=%t, row=%d, col=%d, errMessage=%q",
			m.editing, m.row, m.column, m.errMessage)
	}
	m.editing = false

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
	cmd = m.save()
	if cmd != nil || m.errMessage != "formula is empty" {
		t.Fatalf("expected 'formula is empty' error, got cmd=%v, errMessage=%q", cmd, m.errMessage)
	}

	// Oversized formula
	m.editing = true
	longFormula := "=" + strings.Repeat("A1+", 1400)
	m.input.SetValue(longFormula)
	cmd = m.save()
	if cmd != nil || !strings.Contains(m.errMessage, "formula is longer than") {
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
	if !strings.Contains(view, "enter save • esc cancel • =formula • 'literal") {
		t.Fatalf("editing help string missing:\n%s", view)
	}
}

func TestSpreadsheetViewUnloadedAndError(t *testing.T) {
	repository := newTestRepository(t)
	m := newSpreadsheetModel(spreadsheet.NewService(repository, 10), "")
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

func TestSpreadsheetUpdateCellSavedMsgSuccess(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "A1"}}})

	m.seq = 7
	m.pending = true
	m.row, m.column = 0, 1
	book := m.book
	model, cmd := m.Update(spreadsheetCellSavedMsg{seq: 7, book: book, row: 0, column: 1})
	if model != m || cmd != nil {
		t.Fatalf("expected (m, nil), got model=%v, cmd=%v", model, cmd)
	}
	if m.status != "Saved B1" {
		t.Fatalf("expected status 'Saved B1', got %q", m.status)
	}
	if m.pending {
		t.Fatalf("expected pending false")
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
