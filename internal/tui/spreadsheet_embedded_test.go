package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"example.com/parchment/internal/spreadsheet"
)

func embeddedChartTestWorkbook(t *testing.T, kind string) (spreadsheet.Workbook, string) {
	t.Helper()
	book := spreadsheet.Workbook{
		Sheets: []spreadsheet.Sheet{{
			ID: "monthly-data", Name: "Data",
			Rows: [][]spreadsheet.Cell{
				{{Value: "Month"}, {Value: "Planned"}, {Value: "Completed"}},
				{{Value: "Jan"}, {Value: "12"}, {Formula: "=B2-2"}},
				{{Value: "Feb"}, {Value: "24"}, {Formula: "=B3-3"}},
			},
		}},
	}
	columns := 2
	if kind == "line" {
		columns = 3
	}
	_, err := book.AddChart(spreadsheet.Chart{
		ID: "monthly-chart", Title: "Monthly results", Type: kind, Sheet: "Data",
		FirstRow: 1, LastRow: 3, FirstColumn: 1, LastColumn: columns,
		Header: true, Labels: true,
		PlaceSheet: "Data", PlaceRow: 1, PlaceColumn: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	return book, "![Chart](parchment-chart:monthly-chart)"
}

func TestEmbeddedTerminalChartSmallWidthKeepsText(t *testing.T) {
	book, _ := embeddedChartTestWorkbook(t, "line")
	data, err := spreadsheet.EvaluateChartInWorkbook(&book, book.Charts[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{1, 16, 40, 80} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			rendered := renderEmbeddedTerminalChart(data, width, 12, Theme{})
			for _, line := range strings.Split(rendered, "\n") {
				if got := ansi.StringWidth(line); got > width {
					t.Fatalf("chart line is %d cells wide, want at most %d: %q", got, width, line)
				}
			}
			if width >= 16 {
				for _, text := range []string{"Monthly results", "Planned", "Completed", "Jan", "Feb"} {
					if !strings.Contains(ansi.Strip(rendered), text) {
						t.Fatalf("chart is missing readable %q: %q", text, rendered)
					}
				}
			}
		})
	}
}

func TestEmbeddedSpreadsheetEditorEditsWithoutArtifactLifecycle(t *testing.T) {
	book := spreadsheet.Workbook{Sheets: []spreadsheet.Sheet{
		{Name: "Sales", Rows: [][]spreadsheet.Cell{{{Value: "3"}}}},
	}}
	model := newEmbeddedSpreadsheetModel(book, 0, Theme{}, 10)
	if model.Init() != nil {
		t.Fatal("embedded editor attempted to load a standalone artifact")
	}
	if !model.editWorkbook(func(next *spreadsheet.Spreadsheet) error {
		return spreadsheet.SetCellInBook(next, spreadsheet.SetCellOptions{
			Sheet: "Sales", Row: 1, Column: 1, Cell: spreadsheet.Cell{Value: "4"},
		})
	}) {
		t.Fatalf("embedded workbook edit failed: %s", model.errMessage)
	}
	if cmd := model.finishEmbedded(true); cmd == nil {
		t.Fatal("embedded editor did not return a commit")
	} else {
		msg, ok := cmd().(spreadsheetEmbeddedFinishedMsg)
		if !ok || !msg.committed || msg.workbook.Sheets[0].Rows[0][0].Value != "4" {
			t.Fatalf("embedded commit = %#v", msg)
		}
	}
	if model.book.Kind != "" || model.book.Path != "" {
		t.Fatal("embedded editing assigned standalone artifact metadata")
	}
}

func TestEmbeddedSpreadsheetEditorCancelDoesNotCommitChanges(t *testing.T) {
	book := spreadsheet.Workbook{Sheets: []spreadsheet.Sheet{
		{Name: "Sales", Rows: [][]spreadsheet.Cell{{{Value: "3"}}}},
	}}
	model := newEmbeddedSpreadsheetModel(book, 0, Theme{}, 10)
	if !model.editWorkbook(func(next *spreadsheet.Spreadsheet) error {
		return spreadsheet.SetCellInBook(next, spreadsheet.SetCellOptions{
			Sheet: "Sales", Row: 1, Column: 1, Cell: spreadsheet.Cell{Value: "4"},
		})
	}) {
		t.Fatalf("embedded workbook edit failed: %s", model.errMessage)
	}
	msg := model.finishEmbedded(false)().(spreadsheetEmbeddedFinishedMsg)
	if msg.committed || msg.workbook.Sheets[0].Rows[0][0].Value != "4" {
		t.Fatalf("cancel result = %+v", msg)
	}
}
