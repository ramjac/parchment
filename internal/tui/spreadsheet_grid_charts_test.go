package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ramjac/parchment/internal/spreadsheet"
)

func TestChartInSpannedCellCreateEditAndDelete(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "2"}}, {{Value: "4"}}, {{Value: "6"}}})
	if !m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
		return spreadsheet.SpanCellsInBook(book, "Sheet1", spreadsheet.CellSpan{FirstRow: 1, FirstColumn: 3, LastRow: 8, LastColumn: 5})
	}) {
		t.Fatal(m.errMessage)
	}
	m.row, m.column = 0, 2
	driveSheet(t, m, sheetKey("g"))
	if m.charts.mode != chartForm {
		t.Fatal("g did not open graph editor")
	}
	m.charts.fields[2].SetValue("A1:A3")
	m.charts.fields[5].SetValue("Sales")
	driveSheet(t, m, sheetKey("ctrl+s"))
	if m.charts.mode != chartClosed || len(m.book.Charts) != 1 {
		t.Fatalf("graph was not staged: %s", m.errMessage)
	}
	if !strings.Contains(m.View().Content, "Sales") {
		t.Fatalf("graph was not drawn in grid: %s", m.View().Content)
	}
	before := strings.Join(m.gridCharts[[2]int{0, 2}], "\n")
	stale := m.gridChartSeq
	if !m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
		return spreadsheet.SetCellInBook(book, spreadsheet.SetCellOptions{
			Sheet: "Sheet1", Row: 1, Column: 1, Cell: spreadsheet.Cell{Value: "30"},
		})
	}) {
		t.Fatal(m.errMessage)
	}
	driveSheet(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	after := strings.Join(m.gridCharts[[2]int{0, 2}], "\n")
	if before == after {
		t.Fatal("chart did not update after its source changed")
	}
	driveSheet(t, m, spreadsheetGridChartsMsg{seq: stale, charts: map[[2]int][]string{{0, 2}: {"stale"}}})
	if strings.Contains(strings.Join(m.gridCharts[[2]int{0, 2}], "\n"), "stale") {
		t.Fatal("stale graph rendering replaced current output")
	}
	for _, key := range []string{"enter", "g"} {
		driveSheet(t, m, sheetKey(key))
		if m.charts.mode != chartForm || m.charts.fields[5].Value() != "Sales" {
			t.Fatalf("%s did not open existing graph", key)
		}
		driveSheet(t, m, sheetKey("esc"))
	}
	driveSheet(t, m, sheetKey("delete"))
	driveSheet(t, m, sheetKey("y"))
	if len(m.book.Charts) != 0 || len(m.book.Sheets[0].Spans) != 1 {
		t.Fatal("deleting chart should preserve span")
	}
	driveSheet(t, m, sheetKey("u"))
	if len(m.book.Charts) != 1 {
		t.Fatal("chart deletion was not undoable")
	}
	driveSheet(t, m, tea.WindowSizeMsg{Width: 35, Height: 12})
	if tuiScreenWidth(m.View().Content) > 35 {
		t.Fatal("chart exceeded narrow terminal")
	}
}

func TestChartShortcutSpansSelectionFromUpperLeft(t *testing.T) {
	m, _, repository, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "2"}}, {{Value: "4"}}, {{Value: "6"}}})
	m.recoveryStore = repository
	m.autosaveScheduler = func(uint64) tea.Cmd { return nil }
	m.row, m.column = 0, 2
	driveSheet(t, m, sheetKey("v"))
	for i := 0; i < 5; i++ {
		driveSheet(t, m, sheetKey("down"))
	}
	for i := 0; i < 2; i++ {
		driveSheet(t, m, sheetKey("right"))
	}
	driveSheet(t, m, sheetKey("g"))
	want := spreadsheet.CellSpan{FirstRow: 1, FirstColumn: 3, LastRow: 6, LastColumn: 5}
	if len(m.book.Sheets[0].Spans) != 1 || m.book.Sheets[0].Spans[0] != want ||
		m.selecting || m.row != 0 || m.column != 2 || m.charts.mode != chartForm {
		t.Fatalf("chart shortcut did not create span at anchor: spans=%+v cursor=%d,%d selecting=%t mode=%v error=%q",
			m.book.Sheets[0].Spans, m.row, m.column, m.selecting, m.charts.mode, m.errMessage)
	}
	if !m.autosaveActive || !m.dirty() {
		t.Fatal("new span was not staged for recovery")
	}
	m.charts.fields[2].SetValue("A1:A3")
	driveSheet(t, m, sheetKey("ctrl+s"))
	chart, ok := spreadsheet.ChartAt(&m.book, "Sheet1", 1, 3)
	if !ok || chart.PlaceRow != 1 || chart.PlaceColumn != 3 || len(m.book.Charts) != 1 {
		t.Fatalf("chart not placed in span anchor: %+v", m.book.Charts)
	}
	driveSheet(t, m, sheetKey("u"))
	if len(m.book.Charts) != 0 || len(m.book.Sheets[0].Spans) != 1 {
		t.Fatal("undoing chart should keep the span")
	}
	driveSheet(t, m, sheetKey("u"))
	if len(m.book.Sheets[0].Spans) != 0 {
		t.Fatal("span creation was not undoable")
	}
}

func TestChartShortcutSelectionCancellationAndConflicts(t *testing.T) {
	t.Run("cancel retains span", func(t *testing.T) {
		m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{}}})
		driveSheet(t, m, sheetKey("v"))
		driveSheet(t, m, sheetKey("right"))
		driveSheet(t, m, sheetKey("g"))
		driveSheet(t, m, sheetKey("esc"))
		if len(m.book.Sheets[0].Spans) != 1 || len(m.book.Charts) != 0 || !m.dirty() {
			t.Fatal("cancelling chart editor should leave the new span staged")
		}
	})
	for _, tt := range []struct {
		name string
		rows [][]spreadsheet.Cell
	}{
		{"occupied anchor", [][]spreadsheet.Cell{{{Value: "keep"}, {}}}},
		{"occupied covered cell", [][]spreadsheet.Cell{{{}, {Value: "keep"}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, _, _, _ := newSheetModel(t, tt.rows)
			before := cloneSpreadsheetState(m.book)
			driveSheet(t, m, sheetKey("v"))
			driveSheet(t, m, sheetKey("right"))
			driveSheet(t, m, sheetKey("g"))
			if !spreadsheet.Equal(m.book, before) || !m.selecting || m.charts.mode != chartClosed || m.errMessage == "" {
				t.Fatalf("conflicting selection changed workbook or opened form: spans=%+v error=%q",
					m.book.Sheets[0].Spans, m.errMessage)
			}
		})
	}
	t.Run("spilled covered cell", func(t *testing.T) {
		m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{}, {}}, {{Formula: "=SEQUENCE(1,2)"}, {}}})
		before := cloneSpreadsheetState(m.book)
		m.column = 1
		driveSheet(t, m, sheetKey("v"))
		driveSheet(t, m, sheetKey("down"))
		driveSheet(t, m, sheetKey("g"))
		if !spreadsheet.Equal(m.book, before) || !m.selecting || m.charts.mode != chartClosed ||
			!strings.Contains(m.errMessage, "spilled cell") {
			t.Fatalf("spilled selection was not rejected atomically: %q", m.errMessage)
		}
	})
	t.Run("single cell selection", func(t *testing.T) {
		m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{}}})
		driveSheet(t, m, sheetKey("v"))
		driveSheet(t, m, sheetKey("g"))
		if len(m.book.Sheets[0].Spans) != 0 || m.selecting || m.charts.mode != chartForm {
			t.Fatal("single-cell selection should open a chart without creating a span")
		}
	})
	t.Run("existing graph span", func(t *testing.T) {
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
		driveSheet(t, m, sheetKey("g"))
		if m.charts.mode != chartForm || m.charts.fields[5].Value() != "Existing" ||
			len(m.book.Sheets[0].Spans) != 1 {
			t.Fatal("selected existing span did not open its chart")
		}
	})
}
