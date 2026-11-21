package spreadsheet

import (
	"bytes"
	"testing"
)

func TestEmbeddedWorkbookUsesSharedSheetAndChartOperations(t *testing.T) {
	book := Workbook{Sheets: []Sheet{
		{Name: "Sales", Rows: [][]Cell{{{Value: "3"}}, {{Value: "4"}}}},
		{Name: "Charts", Rows: [][]Cell{{{}}}},
	}}
	if err := NormalizeWorkbook(&book); err != nil {
		t.Fatal(err)
	}
	chart, err := book.AddChart(Chart{
		Type: "bar", Sheet: "Sales", FirstRow: 1, LastRow: 2, FirstColumn: 1, LastColumn: 1,
		PlaceSheet: "Charts", PlaceRow: 1, PlaceColumn: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := EvaluateChartInWorkbook(&book, chart)
	if err != nil || len(data.Series) != 1 || len(data.Series[0].Values) != 2 ||
		data.Series[0].Values[0] != 3 || data.Series[0].Values[1] != 4 {
		t.Fatalf("cross-sheet chart data = %+v, %v", data, err)
	}
	if err := book.InsertRow("Sales", 1); err != nil {
		t.Fatal(err)
	}
	if book.Charts[0].FirstRow != 2 || book.Charts[0].LastRow != 3 {
		t.Fatalf("source range did not follow row insertion: %+v", book.Charts[0])
	}
	data, err = EvaluateChartInWorkbook(&book, book.Charts[0])
	if err != nil || data.Series[0].Values[0] != 3 || data.Series[0].Values[1] != 4 {
		t.Fatalf("chart data after row insertion = %+v, %v", data, err)
	}
}

func TestNormalizeEmptyEmbeddedWorkbook(t *testing.T) {
	var book Workbook
	if err := NormalizeWorkbook(&book); err != nil {
		t.Fatalf("empty host workbook: %v", err)
	}
}

func TestEmbeddedWorkbookPayloadRoundTrip(t *testing.T) {
	sales, err := NewEmbeddedSheet("Sales")
	if err != nil {
		t.Fatal(err)
	}
	charts, err := NewEmbeddedSheet("Charts")
	if err != nil {
		t.Fatal(err)
	}
	book := Workbook{Sheets: []Sheet{sales, charts}}
	if err := book.SetCell(SetCellOptions{Sheet: "Sales", Row: 1, Column: 1, Cell: Cell{Value: "3"}}); err != nil {
		t.Fatal(err)
	}
	chart, err := book.AddChart(Chart{
		Type: "bar", Sheet: "Sales", FirstRow: 1, LastRow: 1, FirstColumn: 1, LastColumn: 1,
		PlaceSheet: "Charts", PlaceRow: 1, PlaceColumn: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := EncodeEmbeddedWorkbook(book)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeEmbeddedWorkbook(data)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := EncodeEmbeddedWorkbook(decoded)
	if err != nil || !bytes.Equal(data, reencoded) {
		t.Fatalf("embedded workbook payload is not stable: %v", err)
	}
	if decoded.Sheets[0].ID != sales.ID || decoded.Charts[0].ID != chart.ID {
		t.Fatalf("stable IDs lost: %+v", decoded)
	}
}

func TestEmbeddedReferenceSyntax(t *testing.T) {
	for _, reference := range []Reference{
		{Kind: SheetReferenceKind, ID: "sheet-1"},
		{Kind: ChartReferenceKind, ID: "chart_2"},
	} {
		formatted, err := FormatReference(reference)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := ParseReference(formatted); !ok || got != reference {
			t.Fatalf("ParseReference(%q) = %+v, %v", formatted, got, ok)
		}
	}
	for _, invalid := range []string{
		"![Spreadsheet](parchment-sheet:)",
		"![Spreadsheet](parchment-sheet:one two)",
		"![Spreadsheet](parchment-chart:chart)!",
		"![Spreadsheet](file.md)",
		"prose ![Spreadsheet](parchment-sheet:sheet-1)",
	} {
		if reference, ok := ParseReference(invalid); ok {
			t.Errorf("ParseReference(%q) = %+v, want no reference", invalid, reference)
		}
	}
}
