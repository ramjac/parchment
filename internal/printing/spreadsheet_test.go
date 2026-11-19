package printing

import (
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
	for _, want := range []string{"123456789012", "long", "last", "5", "#ERR", "hello", "next"} {
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
