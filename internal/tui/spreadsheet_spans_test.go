package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ramjac/parchment/internal/spreadsheet"
)

func TestSpreadsheetSpanSelectionNavigationAndUndo(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "A long cell value"}}})
	driveSheet(t, m, tea.WindowSizeMsg{Width: 30, Height: 10})
	driveSheet(t, m, sheetKey("v"))
	if !strings.Contains(m.View().Content, "m span") {
		t.Fatal("selection help does not show the span shortcut")
	}
	driveSheet(t, m, sheetKey("right"))
	driveSheet(t, m, sheetKey("down"))
	driveSheet(t, m, sheetKey("down"))
	driveSheet(t, m, sheetKey("m"))
	if len(m.book.Sheets[0].Spans) != 1 || m.row != 0 || m.column != 0 || m.selecting {
		t.Fatalf("span not staged: %+v row=%d col=%d", m.book.Sheets[0].Spans, m.row, m.column)
	}
	if !strings.Contains(m.View().Content, "A long cell value") {
		t.Fatalf("span value did not use both columns: %q", m.View().Content)
	}
	if !strings.Contains(m.View().Content, "M split") {
		t.Fatal("span help does not show the split shortcut")
	}
	driveSheet(t, m, sheetKey("right"))
	if m.column != 2 {
		t.Fatalf("right did not skip span: column %d", m.column)
	}
	driveSheet(t, m, sheetKey("left"))
	if m.column != 0 {
		t.Fatalf("left did not select anchor: column %d", m.column)
	}
	driveSheet(t, m, sheetKey("down"))
	if m.row != 3 {
		t.Fatalf("down did not skip span: row %d", m.row)
	}
	driveSheet(t, m, sheetKey("up"))
	if m.row != 0 {
		t.Fatalf("up did not select anchor: row %d", m.row)
	}
	driveSheet(t, m, mouseEvent{X: 2 + m.rowHeaderWidth() + spreadsheetColumnWidth + 3, Y: 5,
		Action: mousePress, Button: mouseLeft})
	if m.row != 0 || m.column != 0 {
		t.Fatalf("click inside span did not select anchor: %d,%d", m.row, m.column)
	}
	driveSheet(t, m, sheetKey("M"))
	if len(m.book.Sheets[0].Spans) != 0 || m.cell(0, 0).Value != "A long cell value" {
		t.Fatal("split lost the anchor")
	}
	driveSheet(t, m, sheetKey("u"))
	if len(m.book.Sheets[0].Spans) != 1 {
		t.Fatal("undo did not restore span")
	}
	driveSheet(t, m, sheetKey("ctrl+r"))
	if len(m.book.Sheets[0].Spans) != 0 {
		t.Fatal("redo did not split span")
	}
}

func TestSpreadsheetSpanRecoveryAndSavedUndo(t *testing.T) {
	m, service, repository, path := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "3"}}})
	m.recoveryStore = repository
	m.autosaveScheduler = func(uint64) tea.Cmd { return nil }
	driveSheet(t, m, sheetKey("v"))
	driveSheet(t, m, sheetKey("down"))
	driveSheet(t, m, sheetKey("m"))
	if !m.autosaveActive {
		t.Fatal("span edit did not start autosave")
	}
	cmd := m.saveRecovery(m.autosaveSession)
	if cmd == nil {
		t.Fatal("span recovery was not scheduled")
	}
	driveSheet(t, m, cmd())
	draft, found, err := repository.LoadRecovery(context.Background(), path)
	if err != nil || !found {
		t.Fatalf("span recovery draft: %t, %v", found, err)
	}
	var data spreadsheetRecoveryData
	if err := json.Unmarshal(draft.Data, &data); err != nil || len(data.Dirty.Sheets[0].Spans) != 1 {
		t.Fatalf("span recovery data: %+v, %v", data.Dirty.Sheets, err)
	}
	driveSheet(t, m, sheetKey("ctrl+s"))
	saved, err := service.Get(context.Background(), path)
	if err != nil || len(saved.Sheets[0].Spans) != 1 {
		t.Fatalf("saved span: %+v, %v", saved.Sheets, err)
	}
	driveSheet(t, m, sheetKey("u"))
	if len(m.book.Sheets[0].Spans) != 0 {
		t.Fatal("saved undo did not remove span")
	}
	driveSheet(t, m, sheetKey("ctrl+r"))
	if len(m.book.Sheets[0].Spans) != 1 {
		t.Fatal("saved redo did not restore span")
	}
}

func TestSpreadsheetSpanClipboardAndClipping(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "first column plus second"}}})
	m.clipboardWrite = func(string) error { return nil }
	if !m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
		return spreadsheet.SpanCellsInBook(book, "Sheet1", spreadsheet.CellSpan{FirstRow: 1, FirstColumn: 1, LastRow: 2, LastColumn: 2})
	}) {
		t.Fatal(m.errMessage)
	}
	driveSheet(t, m, sheetKey("y"))
	if m.clipboard == nil || len(m.clipboard.Spans) != 1 || len(m.clipboard.Cells) != 2 {
		t.Fatalf("copy did not include full span: %+v", m.clipboard)
	}
	driveSheet(t, m, sheetKey("right"))
	driveSheet(t, m, sheetKey("p"))
	if len(m.book.Sheets[0].Spans) != 2 {
		t.Fatalf("paste lost span: %+v", m.book.Sheets[0].Spans)
	}
	driveSheet(t, m, tea.WindowSizeMsg{Width: 35, Height: 10})
	view := m.View().Content
	if tuiScreenWidth(view) > 35 || strings.Count(view, "\n")+1 > 10 {
		t.Fatalf("clipped span exceeds terminal: %q", view)
	}
}

func TestSpreadsheetSelectionEnterSpansAndEditsAnchor(t *testing.T) {
	m, _, repository, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "existing"}}})
	m.recoveryStore = repository
	m.autosaveScheduler = func(uint64) tea.Cmd { return nil }
	driveSheet(t, m, sheetKey("v"))
	driveSheet(t, m, sheetKey("right"))
	driveSheet(t, m, sheetKey("down"))
	driveSheet(t, m, sheetKey("enter"))
	want := spreadsheet.CellSpan{FirstRow: 1, FirstColumn: 1, LastRow: 2, LastColumn: 2}
	if len(m.book.Sheets[0].Spans) != 1 || m.book.Sheets[0].Spans[0] != want ||
		m.row != 0 || m.column != 0 || m.selecting || !m.editing || m.input.Value() != "existing" {
		t.Fatalf("Enter did not span and edit anchor: span=%+v cursor=%d,%d editing=%t input=%q",
			m.book.Sheets[0].Spans, m.row, m.column, m.editing, m.input.Value())
	}
	if !m.autosaveActive || !m.dirty() {
		t.Fatal("span was not staged for recovery")
	}
	m.input.SetValue("changed")
	driveSheet(t, m, sheetKey("enter"))
	if m.editing || m.cell(0, 0).Value != "changed" {
		t.Fatal("cell edit was not staged at the anchor")
	}
	driveSheet(t, m, sheetKey("u"))
	if m.cell(0, 0).Value != "existing" || len(m.book.Sheets[0].Spans) != 1 {
		t.Fatal("undoing cell edit should preserve the span")
	}
	driveSheet(t, m, sheetKey("u"))
	if len(m.book.Sheets[0].Spans) != 0 {
		t.Fatal("span creation was not undoable")
	}
}

func TestSpreadsheetSelectionEnterConflictsAndExistingCells(t *testing.T) {
	t.Run("cancel new span edit", func(t *testing.T) {
		m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "anchor"}}})
		driveSheet(t, m, sheetKey("v"))
		driveSheet(t, m, sheetKey("right"))
		driveSheet(t, m, sheetKey("enter"))
		m.input.SetValue("discarded")
		driveSheet(t, m, sheetKey("esc"))
		if m.editing || len(m.book.Sheets[0].Spans) != 1 || m.cell(0, 0).Value != "anchor" || !m.dirty() {
			t.Fatal("cancelling edit should keep the newly staged span and original value")
		}
	})
	t.Run("covered cell occupied", func(t *testing.T) {
		m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "anchor"}, {Value: "keep"}}})
		before := cloneSpreadsheetState(m.book)
		driveSheet(t, m, sheetKey("v"))
		driveSheet(t, m, sheetKey("right"))
		driveSheet(t, m, sheetKey("enter"))
		if !spreadsheet.Equal(m.book, before) || !m.selecting || m.editing || m.errMessage == "" {
			t.Fatalf("conflicting range was not rejected atomically: %q", m.errMessage)
		}
	})
	t.Run("single cell", func(t *testing.T) {
		m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Formula: "=1+2"}}})
		driveSheet(t, m, sheetKey("v"))
		driveSheet(t, m, sheetKey("enter"))
		if len(m.book.Sheets[0].Spans) != 0 || m.selecting || !m.editing || m.input.Value() != "=1+2" {
			t.Fatal("single-cell selection should edit the existing cell without a span")
		}
	})
	t.Run("single spilled cell", func(t *testing.T) {
		m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Formula: "=SEQUENCE(1,2)"}}})
		driveSheet(t, m, sheetKey("right"))
		driveSheet(t, m, sheetKey("v"))
		driveSheet(t, m, sheetKey("enter"))
		if !m.selecting || m.editing || !strings.Contains(m.errMessage, "spilled cell") {
			t.Fatalf("spilled cell was not rejected: %q", m.errMessage)
		}
	})
	t.Run("existing span and cancel", func(t *testing.T) {
		m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "anchor"}}})
		driveSheet(t, m, sheetKey("v"))
		driveSheet(t, m, sheetKey("right"))
		driveSheet(t, m, sheetKey("m"))
		driveSheet(t, m, sheetKey("v"))
		driveSheet(t, m, sheetKey("enter"))
		if !m.editing || m.input.Value() != "anchor" || len(m.book.Sheets[0].Spans) != 1 {
			t.Fatal("selecting an existing span should edit its anchor")
		}
		m.input.SetValue("discarded")
		driveSheet(t, m, sheetKey("esc"))
		if m.editing || len(m.book.Sheets[0].Spans) != 1 || m.cell(0, 0).Value != "anchor" {
			t.Fatal("cancelling edit should retain span and original value")
		}
	})
	t.Run("existing chart", func(t *testing.T) {
		m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "3"}}})
		if !m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
			if err := spreadsheet.SpanCellsInBook(book, "Sheet1", spreadsheet.CellSpan{FirstRow: 1, FirstColumn: 2, LastRow: 2, LastColumn: 3}); err != nil {
				return err
			}
			_, err := spreadsheet.AddChartToBook(book, spreadsheet.Chart{
				Type: "bar", Sheet: "Sheet1", FirstRow: 1, LastRow: 1, FirstColumn: 1, LastColumn: 1,
				PlaceSheet: "Sheet1", PlaceRow: 1, PlaceColumn: 2, Title: "Existing",
			})
			return err
		}) {
			t.Fatal(m.errMessage)
		}
		m.column = 1
		driveSheet(t, m, sheetKey("v"))
		driveSheet(t, m, sheetKey("enter"))
		if m.charts.mode != chartForm || m.editing || m.charts.fields[5].Value() != "Existing" {
			t.Fatal("Enter should edit an existing chart, not its cell")
		}
	})
}
