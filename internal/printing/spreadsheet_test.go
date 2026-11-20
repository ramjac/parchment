package printing

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"example.com/parchment/internal/spreadsheet"
)

func TestSpreadsheetPrintAcrossColumnsBeforeRows(t *testing.T) {
	rows := make([][]spreadsheet.Cell, 60)
	for r := range rows {
		rows[r] = make([]spreadsheet.Cell, 11)
		for c := range rows[r] {
			rows[r][c].Value = fmt.Sprintf("r%02dc%02d", r+1, c+1)
		}
	}
	book := spreadsheet.Spreadsheet{Sheets: []spreadsheet.Sheet{{Name: "First", Rows: rows}, {Name: "Empty"}}}
	pages, err := SpreadsheetPages(book)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 7 {
		t.Fatalf("got %d pages, want six bands plus empty sheet", len(pages))
	}
	for i, check := range []struct{ contains, excludes string }{
		{"r01c01", "r01c05"}, {"r01c05", "r01c01"}, {"r01c09", "r01c05"},
		{"r60c01", "r60c05"}, {"r60c05", "r60c01"}, {"r60c09", "r60c05"},
		{"Sheet: Empty", "r01c01"},
	} {
		text := strings.Join(pages[i].Lines, "\n")
		if !strings.Contains(text, check.contains) || strings.Contains(text, check.excludes) {
			t.Fatalf("page %d has wrong band:\n%s", i+1, text)
		}
	}
}

func TestWorkbookSheetPagesUseSharedWorkbookContext(t *testing.T) {
	book := spreadsheet.Workbook{Sheets: []spreadsheet.Sheet{
		{Name: "Sales", Rows: [][]spreadsheet.Cell{{{Value: "3"}}}},
		{Name: "Summary", Rows: [][]spreadsheet.Cell{{{Formula: "=Sales!A1+1"}}}},
	}}
	if err := spreadsheet.NormalizeWorkbook(&book); err != nil {
		t.Fatal(err)
	}
	pages, err := WorkbookSheetPagesContext(context.Background(), book, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) == 0 || pages[0].SheetIndex != 1 || !strings.Contains(strings.Join(pages[0].Lines, "\n"), "4") {
		t.Fatalf("embedded sheet pages = %+v", pages)
	}
	if pages, err := WorkbookPagesContext(context.Background(), spreadsheet.Workbook{}); err != nil || len(pages) != 0 {
		t.Fatalf("empty host workbook pages = %v, %v", pages, err)
	}
}

func TestSpreadsheetPrintWrappingAndFormula(t *testing.T) {
	book := spreadsheet.Spreadsheet{Sheets: []spreadsheet.Sheet{{Name: "Values", Rows: [][]spreadsheet.Cell{
		{{Value: "123456789012long\nlast"}, {Formula: "=2+3"}, {Formula: "=1/0"}, {Formula: `="hello"`}},
		{{Value: "next"}},
	}}}}
	pages, err := SpreadsheetPages(book)
	if err != nil {
		t.Fatal(err)
	}

	text := strings.Join(pages[0].Lines, "\n")
	for _, want := range []string{"123456789012", "long", "last", "5", "#DIV/0!", "hello", "next"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "last") > strings.Index(text, "next") {
		t.Fatal("wrapped cell overlapped the next row")
	}
	book.Sheets[0].Rows[0][0].Value = strings.Repeat("x\n", 100)
	if _, err := SpreadsheetPages(book); err == nil {
		t.Fatal("oversized row should return an error")
	}
}

func TestSpreadsheetPrintIncludesSpilledCells(t *testing.T) {
	book := spreadsheet.Spreadsheet{Sheets: []spreadsheet.Sheet{{Name: "Values", Rows: [][]spreadsheet.Cell{{{Formula: "=SEQUENCE(2,2)"}}}}}}
	pages, err := SpreadsheetPages(book)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(pages[0].Lines, "\n")
	for _, value := range []string{"1", "2", "3", "4"} {
		if !strings.Contains(text, value) {
			t.Fatalf("missing spilled value %q:\n%s", value, text)
		}
	}
}

func TestSpreadsheetPrintSpanAcrossPageBands(t *testing.T) {
	rows := make([][]spreadsheet.Cell, 60)
	for i := range rows {
		rows[i] = make([]spreadsheet.Cell, 5)
	}
	rows[0][0].Value = strings.Repeat("x", 50) + "QXYZ"
	book := spreadsheet.Spreadsheet{Sheets: []spreadsheet.Sheet{{Name: "Span", Rows: rows,
		Spans: []spreadsheet.CellSpan{{FirstRow: 1, FirstColumn: 1, LastRow: 60, LastColumn: 5}}}}}
	pages, err := SpreadsheetPages(book)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 4 || len(pages[0].Spans) != 1 || len(pages[1].Spans) != 1 {
		t.Fatalf("span was not clipped into page bands: %+v", pages)
	}
	first := strings.Join(pages[0].Lines, "\n")
	second := strings.Join(pages[1].Lines, "\n")
	if !strings.Contains(first, strings.Repeat("x", 50)+"Q") || !strings.Contains(second, "XYZ") {
		t.Fatalf("span content lost at column boundary:\n%s\n%s", first, second)
	}
	for _, page := range pages[2:] {
		if strings.Contains(strings.Join(page.Lines, "\n"), "QXYZ") {
			t.Fatal("span content was duplicated on another row band")
		}
	}
}
