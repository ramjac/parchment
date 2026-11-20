package spreadsheet

import (
	"reflect"
	"testing"
)

func TestChartPlacementAndStructuralEdits(t *testing.T) {
	book := formulaChain(0)
	book.Sheets[0].Rows = [][]Cell{{{Value: "3"}, {}, {}, {}}, {{Value: "4"}, {}, {}, {}}, {{}, {}, {}, {}}, {{}, {}, {}, {}}}
	if err := SpanCellsInBook(&book, "Sheet1", CellSpan{FirstRow: 1, FirstColumn: 2, LastRow: 3, LastColumn: 4}); err != nil {
		t.Fatal(err)
	}
	chart, err := AddChartToBook(&book, Chart{Type: "bar", Sheet: "Sheet1", FirstRow: 1, LastRow: 2, FirstColumn: 1, LastColumn: 1, PlaceSheet: "Sheet1", PlaceRow: 1, PlaceColumn: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := SetCellInBook(&book, SetCellOptions{Sheet: "Sheet1", Row: 1, Column: 2, Cell: Cell{Value: "overwrite"}}); err == nil {
		t.Fatal("chart accepted an ordinary cell write")
	}
	if v, err := Evaluate(&book, 0, 1, 2); err != nil || v.Kind != EmptyKind {
		t.Fatalf("chart cell reference = %#v, %v", v, err)
	}
	encoded, err := Encode(book)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Decode(encoded)
	if err != nil || !reflect.DeepEqual(reopened.Charts, book.Charts) {
		t.Fatalf("chart round trip = %#v, %v", reopened.Charts, err)
	}
	if err := DeleteRowFromBook(&book, "Sheet1", 1); err != nil {
		t.Fatal(err)
	}
	got, ok := ChartAt(&book, "Sheet1", 1, 2)
	if !ok || got.ID != chart.ID {
		t.Fatalf("chart did not follow span anchor: %+v", book.Charts)
	}
	if err := SplitCellInBook(&book, "Sheet1", 1, 2); err != nil {
		t.Fatal(err)
	}
	if _, ok := ChartAt(&book, "Sheet1", 1, 2); !ok {
		t.Fatal("splitting span lost chart")
	}
	if err := DeleteRowFromBook(&book, "Sheet1", 1); err != nil {
		t.Fatal(err)
	}
	if len(book.Charts) != 0 {
		t.Fatal("deleting the chart cell retained chart")
	}
}

func TestChartPlacementSpillAndClipboard(t *testing.T) {
	book := formulaChain(0)
	book.Sheets[0].Rows = [][]Cell{{{Formula: "=SEQUENCE(2,1)"}, {}, {}, {}}, {{}, {}, {}, {}}}
	source := Chart{Type: "bar", Sheet: "Sheet1", FirstRow: 1, LastRow: 2, FirstColumn: 1, LastColumn: 1, PlaceSheet: "Sheet1", PlaceRow: 2, PlaceColumn: 1}
	if _, err := AddChartToBook(&book, source); err == nil {
		t.Fatal("chart placed on spill")
	}

	source.PlaceRow, source.PlaceColumn = 1, 2
	chart, err := AddChartToBook(&book, source)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := CopyRange(book, "Sheet1", 1, 2, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := PasteRange(&book, "Sheet1", 1, 3, copy); err != nil {
		t.Fatal(err)
	}
	duplicate, ok := ChartAt(&book, "Sheet1", 1, 3)
	if !ok || duplicate.ID == chart.ID || duplicate.FirstRow != chart.FirstRow {
		t.Fatalf("copied chart = %+v", duplicate)
	}
	if err := PasteRange(&book, "Sheet1", 1, 3, copy); err == nil {
		t.Fatal("paste overwrote chart")
	}
	if err := SetCellInBook(&book, SetCellOptions{Sheet: "Sheet1", Row: 1, Column: 4, Cell: Cell{Value: "keep"}}); err != nil {
		t.Fatal(err)
	}
	if err := PasteRange(&book, "Sheet1", 1, 4, copy); err == nil || book.Sheets[0].Rows[0][3].Value != "keep" {
		t.Fatal("chart paste overwrote ordinary cell")
	}
}

func TestChartSourceAndPlacementMoveIndependently(t *testing.T) {
	book := formulaChain(0)
	book.Sheets[0].Rows = [][]Cell{{{Value: "3"}}, {{Value: "4"}}}
	if err := AddSheetToBook(&book, "Charts"); err != nil {
		t.Fatal(err)
	}
	_, err := AddChartToBook(&book, Chart{Type: "bar", Sheet: "Sheet1", FirstRow: 1, LastRow: 2,
		FirstColumn: 1, LastColumn: 1, PlaceSheet: "Charts", PlaceRow: 1, PlaceColumn: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := InsertRowInBook(&book, "Sheet1", 1); err != nil {
		t.Fatal(err)
	}
	if err := InsertColumnInBook(&book, "Charts", 1); err != nil {
		t.Fatal(err)
	}
	chart := book.Charts[0]
	if chart.FirstRow != 2 || chart.LastRow != 3 || chart.PlaceRow != 1 || chart.PlaceColumn != 2 {
		t.Fatalf("source or placement moved incorrectly: %+v", chart)
	}
	if err := DeleteSheetFromBook(&book, "Sheet1"); err != nil {
		t.Fatal(err)
	}
	if len(book.Charts) != 1 || !book.Charts[0].Invalid {
		t.Fatalf("deleting source did not preserve invalid chart: %+v", book.Charts)
	}
}
