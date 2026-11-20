package spreadsheet

import (
	"reflect"
	"strings"
	"testing"
)

func TestCellSpanPersistenceAndReferences(t *testing.T) {
	book := formulaChain(0)
	span := CellSpan{FirstRow: 1, FirstColumn: 1, LastRow: 3, LastColumn: 1}
	if err := SpanCellsInBook(&book, "Sheet1", span); err != nil {
		t.Fatal(err)
	}
	if err := SetCellInBook(&book, SetCellOptions{Sheet: "Sheet1", Row: 2, Column: 1, Cell: Cell{Value: "lost"}}); err == nil {
		t.Fatal("covered cell accepted a write")
	}
	if err := SetCellInBook(&book, SetCellOptions{Sheet: "Sheet1", Row: 1, Column: 2, Cell: Cell{Formula: "=SUM(A1:A3)"}}); err != nil {
		t.Fatal(err)
	}
	value, err := Evaluate(&book, 0, 2, 1)
	if err != nil || value.Kind != EmptyKind {
		t.Fatalf("covered reference = %+v, %v", value, err)
	}
	value, err = Evaluate(&book, 0, 1, 2)
	if err != nil || value.Kind != NumKind || value.Num != 1 {
		t.Fatalf("sum of span = %+v, %v", value, err)
	}
	book.Sheets = append(book.Sheets, Sheet{Name: "Summary", Rows: [][]Cell{
		{{Formula: "=SUM(Sheet1!A2:A3)"}, {Formula: "=SUM(Sheet1!A1:A3)"}},
	}})
	for col, want := range []float64{0, 1} {
		value, err = Evaluate(&book, 1, 1, col+1)
		if err != nil || value.Kind != NumKind || value.Num != want {
			t.Fatalf("cross-sheet range %d = %+v, %v", col, value, err)
		}
	}
	data, err := Encode(book)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil || !reflect.DeepEqual(decoded.Sheets, book.Sheets) {
		t.Fatalf("round trip = %+v, %v", decoded.Sheets, err)
	}
	clone := cloneSpreadsheet(book)
	clone.Sheets[0].Spans[0].LastRow = 2
	if book.Sheets[0].Spans[0].LastRow != 3 {
		t.Fatal("workbook clone shared span metadata")
	}
	if err := SplitCellInBook(&book, "Sheet1", 1, 1); err != nil || len(book.Sheets[0].Spans) != 0 ||
		book.Sheets[0].Rows[0][0].Value != "1" {
		t.Fatalf("split = %+v, %v", book.Sheets[0], err)
	}
}

func TestCellSpanClipboardTranslatesAnchorFormula(t *testing.T) {
	book := formulaChain(0)
	if err := SetCellInBook(&book, SetCellOptions{Sheet: "Sheet1", Row: 1, Column: 2, Cell: Cell{Formula: "=A1"}}); err != nil {
		t.Fatal(err)
	}
	if err := SpanCellsInBook(&book, "Sheet1", CellSpan{1, 2, 1, 3}); err != nil {
		t.Fatal(err)
	}
	copied, err := CopyRange(book, "Sheet1", 1, 2, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := PasteRange(&book, "Sheet1", 3, 2, copied); err != nil {
		t.Fatal(err)
	}
	if got := book.Sheets[0].Rows[2][1].Formula; got != "=A3" {
		t.Fatalf("pasted span formula = %q", got)
	}
}

func TestCellSpanRejectsConflictsAtomically(t *testing.T) {
	book := formulaChain(0)
	if err := SetCellInBook(&book, SetCellOptions{Sheet: "Sheet1", Row: 2, Column: 1, Cell: Cell{Value: "keep"}}); err != nil {
		t.Fatal(err)
	}
	before := cloneSpreadsheet(book)
	err := SpanCellsInBook(&book, "Sheet1", CellSpan{1, 1, 2, 1})
	if err == nil || !reflect.DeepEqual(book, before) {
		t.Fatalf("nonempty span changed workbook: %v", err)
	}
	if err := SetCellInBook(&book, SetCellOptions{Sheet: "Sheet1", Row: 2, Column: 1}); err != nil {
		t.Fatal(err)
	}
	if err := SpanCellsInBook(&book, "Sheet1", CellSpan{1, 1, 2, 1}); err != nil {
		t.Fatal(err)
	}
	if err := SpanCellsInBook(&book, "Sheet1", CellSpan{2, 1, 3, 1}); err == nil {
		t.Fatal("overlapping span accepted")
	}
	book.Sheets[0].Rows[1][0].Value = "hidden"
	if err := Normalize(&book); err == nil {
		t.Fatal("covered content accepted by normalization")
	}
}

func TestCellSpanStructuralEditsPreserveAnchor(t *testing.T) {
	book := formulaChain(0)
	if err := SpanCellsInBook(&book, "Sheet1", CellSpan{1, 1, 3, 2}); err != nil {
		t.Fatal(err)
	}
	if err := InsertRowInBook(&book, "Sheet1", 2); err != nil {
		t.Fatal(err)
	}
	if got := book.Sheets[0].Spans[0]; got.LastRow != 4 {
		t.Fatalf("inserted row span = %+v", got)
	}
	if err := DeleteRowFromBook(&book, "Sheet1", 1); err != nil {
		t.Fatal(err)
	}
	if got := book.Sheets[0].Rows[0][0].Value; got != "1" {
		t.Fatalf("deleted anchor value = %q", got)
	}
	if err := DeleteColumnFromBook(&book, "Sheet1", 1); err != nil {
		t.Fatal(err)
	}
	if got := book.Sheets[0].Rows[0][0].Value; got != "1" {
		t.Fatalf("deleted anchor column value = %q", got)
	}
	if err := Normalize(&book); err != nil {
		t.Fatal(err)
	}
}

func TestCellSpanClipboardAndSpills(t *testing.T) {
	book := formulaChain(0)
	if err := SpanCellsInBook(&book, "Sheet1", CellSpan{1, 1, 2, 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := CopyRange(book, "Sheet1", 1, 1, 1, 1); err == nil {
		t.Fatal("partial span copy accepted")
	}
	copied, err := CopyRange(book, "Sheet1", 1, 1, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := PasteRange(&book, "Sheet1", 1, 3, copied); err != nil {
		t.Fatal(err)
	}
	if span, ok := SpanAt(book.Sheets[0], 2, 3); !ok || span.FirstColumn != 3 {
		t.Fatalf("pasted span = %+v, %v", span, ok)
	}
	before := cloneSpreadsheet(book)
	if err := PasteRange(&book, "Sheet1", 2, 1, copied); err == nil || !reflect.DeepEqual(book, before) {
		t.Fatal("conflicting paste changed workbook")
	}
	if err := SetCellInBook(&book, SetCellOptions{Sheet: "Sheet1", Row: 1, Column: 1, Cell: Cell{Formula: "=SEQUENCE(2)"}}); err != nil {
		t.Fatal(err)
	}
	value, err := Evaluate(&book, 0, 1, 1)
	if err != nil || value.Kind != ErrKind || value.Err != ErrSpill {
		t.Fatalf("spanned array = %+v, %v", value, err)
	}
	if err := SplitCellInBook(&book, "Sheet1", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := SpanCellsInBook(&book, "Sheet1", CellSpan{2, 1, 2, 2}); err == nil || !strings.Contains(err.Error(), "spilled") {
		t.Fatalf("covering spill error = %v", err)
	}
}
