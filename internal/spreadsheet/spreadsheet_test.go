package spreadsheet

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"example.com/parchment/internal/artifact"
)

func TestSingleFileWorkbookEncodesMetadataCellsAndFormulas(t *testing.T) {
	book := Spreadsheet{
		Artifact: artifact.Artifact{
			ID: "s12345", Kind: artifact.SpreadsheetKind,
			Title: "Budget", FormatVersion: artifact.FormatVersion,
			CreatedAt: fixedTime, ModifiedAt: fixedTime,
		},
		Version: FileVersion,
		Body:    "# Budget\n\nA visible Markdown description.",
		Sheets: []Sheet{{Name: "Sheet1", Rows: [][]Cell{
			{{Value: "Income"}, {Value: "125"}, {Formula: "=B1*2"}},
			{{Value: "Total"}, {}, {Formula: "=(B1+C1)/3"}},
		}}},
	}
	data, err := Encode(book)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "```parchment-meta\n") ||
		!strings.Contains(string(data), "```parchment-spreadsheet\n") {
		t.Fatalf("workbook is not a Markdown artifact: %q", data[:min(len(data), 200)])
	}
	decoded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if book.Version != decoded.Version || book.Body != decoded.Body || !reflect.DeepEqual(book.Sheets, decoded.Sheets) {
		t.Fatalf("round trip changed persisted workbook content:\noriginal: %+v\ndecoded: %+v", book, decoded)
	}
	valueV, err := Evaluate(&decoded, 0, 2, 3)
	var value float64
	if err == nil {
		value, err = valueV.Float64()
	}
	if err != nil || value != 125 {
		t.Fatalf("formula result = %v, %v", value, err)
	}
}

func TestNormalizeRejectsNamesThatSheetIndexTreatsAsEqual(t *testing.T) {
	book := formulaChain(0)
	book.Sheets = []Sheet{
		{Name: "Σ", Rows: [][]Cell{{{Value: "first"}}}},
		{Name: "ς", Rows: [][]Cell{{{Value: "second"}}}},
	}
	if err := Normalize(&book); err == nil {
		t.Fatal("sheet names equivalent under EqualFold were accepted")
	}
}

func TestNormalizeRejectsInvalidUTF8SheetNames(t *testing.T) {
	book := formulaChain(0)
	book.Sheets[0].Name = string([]byte{0xff})
	if err := Normalize(&book); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("invalid UTF-8 sheet name returned %v", err)
	}
}

func TestNormalizeDoesNotValidateTransientTitle(t *testing.T) {
	book := formulaChain(0)
	book.Title += string([]byte{0xff})
	if err := Normalize(&book); err != nil {
		t.Fatalf("transient title affected workbook validation: %v", err)
	}
}

func TestFormulaArithmeticReferencesAndCycles(t *testing.T) {
	book := Spreadsheet{
		Artifact: artifact.Artifact{
			ID: "s12345", Kind: artifact.SpreadsheetKind,
			Title: "Formula test", FormatVersion: artifact.FormatVersion,
			CreatedAt: fixedTime, ModifiedAt: fixedTime,
		},
		Version: FileVersion,
		Sheets: []Sheet{{Name: "Sheet1", Rows: [][]Cell{
			{{Value: "4"}, {Value: "3"}, {Formula: "=A1+B1*2"}},
			{{}, {}, {Formula: "=(C1-2)/2"}},
			{{Formula: "=-2+1.5e1"}},
		}}},
	}
	if err := Normalize(&book); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		row, column int
		want        float64
	}{{1, 3, 10}, {2, 3, 4}, {3, 1, 13}} {
		gotV, err := Evaluate(&book, 0, test.row, test.column)
		var got float64
		if err == nil {
			got, err = gotV.Float64()
		}
		if err != nil || got != test.want {
			t.Fatalf("cell %s = %v, %v; want %v", CellName(test.row, test.column), got, err, test.want)
		}
	}
	book.Sheets[0].Rows[0][0] = Cell{Formula: "=B1"}
	book.Sheets[0].Rows[0][1] = Cell{Formula: "=A1"}
	valV, err := Evaluate(&book, 0, 1, 1)
	if err == nil {
		_, err = valV.Float64()
	}
	if err == nil {
		t.Fatal("circular reference was accepted")
	}
	if err := Normalize(&book); err == nil {
		t.Fatal("workbook with a circular formula was accepted")
	}
}

func TestFormulaParserSkipsUnicodeWhitespace(t *testing.T) {
	book := formulaChain(0)
	book.Sheets[0].Rows[0][0] = Cell{Formula: "=1\u00a0+2"}
	if err := Normalize(&book); err != nil {
		t.Fatal(err)
	}
	gotV, err := Evaluate(&book, 0, 1, 1)
	var got float64
	if err == nil {
		got, err = gotV.Float64()
	}
	if err != nil || got != 3 {
		t.Fatalf("formula with non-breaking space = %v, %v; want 3", got, err)
	}
}

func TestSpreadsheetOperationsFormulaShiftsAndHistory(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryRepository()
	service := NewService(repository, 20)
	book, err := service.Create(ctx, "values.md", [][]Cell{{{Value: "2"}, {Value: "3"}, {Formula: "=A1+B1"}}})
	if err != nil {
		t.Fatal(err)
	}
	valueV, err := Evaluate(&book, 0, 1, 3)
	var value float64
	if err == nil {
		value, err = valueV.Float64()
	}
	if err != nil || value != 5 {
		t.Fatalf("initial formula = %v, %v", value, err)
	}
	updated, err := service.InsertRow(ctx, book.Path, 1)
	if err != nil {
		t.Fatal(err)
	}
	formula := updated.Sheets[0].Rows[1][2].Formula
	if formula != "=A2+B2" {
		t.Fatalf("formula after row insertion = %q", formula)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	undone, err := service.Get(ctx, book.Path)
	if err != nil || len(undone.Sheets[0].Rows) != 1 || undone.Sheets[0].Rows[0][2].Formula != "=A1+B1" {
		t.Fatalf("undo did not restore sheet: %+v, %v", undone, err)
	}
	if _, err := service.Redo(ctx); err != nil {
		t.Fatal(err)
	}
	redone, err := service.Get(ctx, book.Path)
	if err != nil || len(redone.Sheets[0].Rows) != 2 || redone.Sheets[0].Rows[1][2].Formula != "=A2+B2" {
		t.Fatalf("redo did not restore inserted row: %+v, %v", redone, err)
	}
}

func TestCellCoordinates(t *testing.T) {
	for _, test := range []struct {
		name        string
		row, column int
	}{
		{"A1", 1, 1}, {"Z9", 9, 26}, {"AA12", 12, 27}, {"IV1000", 1000, 256},
	} {
		row, column, err := CellCoordinates(test.name)
		if err != nil || row != test.row || column != test.column {
			t.Errorf("CellCoordinates(%q) = %d,%d,%v", test.name, row, column, err)
		}
	}
	for _, invalid := range []string{"", "A", "A0", "IW1", "A1001", "A-1"} {
		if _, _, err := CellCoordinates(invalid); err == nil {
			t.Errorf("CellCoordinates(%q) accepted invalid reference", invalid)
		}
	}
}

func TestCopyPasteRange(t *testing.T) {
	book := Spreadsheet{Sheets: []Sheet{{Name: "Sheet1", Rows: [][]Cell{
		{{Value: "=literal"}, {Formula: "=A1+$A$1"}},
		{{Value: "next"}, {}},
	}}}}
	copied, err := CopyRange(book, "Sheet1", 1, 1, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := PasteRange(&book, "Sheet1", 2, 2, copied); err != nil {
		t.Fatal(err)
	}
	rows := book.Sheets[0].Rows
	if rows[1][1].Value != "=literal" || rows[1][2].Formula != "=B2+$A$1" || rows[2][1].Value != "next" {
		t.Fatalf("overlapping paste = %#v", rows)
	}
	before := cloneSpreadsheet(book)
	if err := PasteRange(&book, "Sheet1", MaxRows, MaxColumns, copied); err == nil {
		t.Fatal("out-of-bounds paste succeeded")
	}
	if !Equal(before, book) {
		t.Fatal("failed paste modified the workbook")
	}
}

func TestSpillCellsAreReadOnlyAndCopyValues(t *testing.T) {
	book := formulaChain(0)
	book.Sheets[0].Rows = [][]Cell{{{Formula: "=SEQUENCE(3)"}}}
	if err := SetCellInBook(&book, SetCellOptions{Sheet: book.Sheets[0].Name, Row: 2, Column: 1, Cell: Cell{Value: "overwrite"}}); err == nil {
		t.Fatal("editing spill cell succeeded")
	}
	copied, err := CopyRange(book, book.Sheets[0].Name, 2, 1, 3, 1)
	if err != nil || copied.Cells[0][0].Value != "2" || copied.Cells[1][0].Value != "3" {
		t.Fatalf("copied spill = %#v, %v", copied, err)
	}
	if err := PasteRange(&book, book.Sheets[0].Name, 3, 1, copied); err == nil {
		t.Fatal("pasting into spill succeeded")
	}
	copied, err = CopyRange(book, book.Sheets[0].Name, 1, 1, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := PasteRange(&book, book.Sheets[0].Name, 1, 2, copied); err != nil {
		t.Fatal(err)
	}
	value, err := Evaluate(&book, 0, 3, 2)
	if err != nil || value.Display() != "3" {
		t.Fatalf("copied anchor spill = %#v, %v", value, err)
	}
}

func TestChartPersistenceAndRangeBinding(t *testing.T) {
	book := formulaChain(0)
	book.Sheets[0].Rows = [][]Cell{
		{{Value: "Month"}, {Value: "Sales"}},
		{{Value: "Jan"}, {Value: "2"}},
		{{Value: "Feb"}, {Formula: "=B2*2"}},
	}
	chart, err := AddChartToBook(&book, Chart{Title: "Sales", Type: "bar", Sheet: book.Sheets[0].Name,
		PlaceSheet: book.Sheets[0].Name, PlaceRow: 1, PlaceColumn: 3,
		FirstRow: 1, FirstColumn: 1, LastRow: 3, LastColumn: 2, Header: true, Labels: true})
	if err != nil {
		t.Fatal(err)
	}
	data, err := EvaluateChart(&book, chart)
	if err != nil || !reflect.DeepEqual(data.Labels, []string{"Jan", "Feb"}) ||
		!reflect.DeepEqual(data.Series[0].Values, []float64{2, 4}) {
		t.Fatalf("chart data = %#v, %v", data, err)
	}
	encoded, err := Encode(book)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := Decode(encoded)
	if err != nil || !reflect.DeepEqual(book.Charts, reloaded.Charts) {
		t.Fatalf("chart round trip = %#v, %v", reloaded.Charts, err)
	}
	if err := InsertRowInBook(&book, chart.Sheet, 2); err != nil {
		t.Fatal(err)
	}
	if book.Charts[0].LastRow != 4 {
		t.Fatalf("chart range not expanded: %#v", book.Charts[0])
	}
}

func TestChartTypesAndBarSeriesLimit(t *testing.T) {
	book := formulaChain(0)
	book.Sheets[0].Rows = [][]Cell{
		{{Value: "Month"}, {Value: "North"}, {Value: "South"}},
		{{Value: "Jan"}, {Value: "2"}, {Value: "3"}},
		{{Value: "Feb"}, {Value: "4"}, {Value: "5"}},
	}
	if _, err := AddChartToBook(&book, Chart{Type: "pie", Sheet: book.Sheets[0].Name,
		FirstRow: 1, FirstColumn: 1, LastRow: 3, LastColumn: 2}); err == nil {
		t.Fatal("unsupported pie chart accepted")
	}
	if _, err := AddChartToBook(&book, Chart{Type: "bar", Sheet: book.Sheets[0].Name,
		FirstRow: 1, FirstColumn: 2, LastRow: 3, LastColumn: 3, Header: true}); err == nil {
		t.Fatal("multi-series bar chart accepted")
	}
	line, err := AddChartToBook(&book, Chart{Type: "line", Sheet: book.Sheets[0].Name,
		PlaceSheet: book.Sheets[0].Name, PlaceRow: 1, PlaceColumn: 4,
		FirstRow: 1, FirstColumn: 1, LastRow: 3, LastColumn: 3, Header: true, Labels: true})
	if err != nil {
		t.Fatal(err)
	}
	data, err := EvaluateChart(&book, line)
	if err != nil || len(data.Series) != 2 || len(data.Labels) != 2 {
		t.Fatalf("multi-series line chart = %#v, %v", data, err)
	}
}

func TestChartReadsLiveSpillsAfterReopen(t *testing.T) {
	book := formulaChain(0)
	book.Sheets[0].Rows = [][]Cell{{{Formula: "=SEQUENCE(3,1,2)"}}}
	chart, err := AddChartToBook(&book, Chart{Type: "bar", Sheet: book.Sheets[0].Name,
		PlaceSheet: book.Sheets[0].Name, PlaceRow: 1, PlaceColumn: 2,
		FirstRow: 1, FirstColumn: 1, LastRow: 3, LastColumn: 1})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(book)
	if err != nil {
		t.Fatal(err)
	}
	book, err = Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	data, err := EvaluateChart(&book, chart)
	if err != nil || !reflect.DeepEqual(data.Series[0].Values, []float64{2, 3, 4}) {
		t.Fatalf("reopened spill chart: %+v, %v", data, err)
	}
	book.Sheets[0].Rows[0][0].Formula = "=SEQUENCE(3,1,5)"
	data, err = EvaluateChart(&book, chart)
	if err != nil || !reflect.DeepEqual(data.Series[0].Values, []float64{5, 6, 7}) {
		t.Fatalf("updated spill chart: %+v, %v", data, err)
	}
}

func TestFormulaReferenceShiftsPreserveDecimalExponents(t *testing.T) {
	for _, formula := range []string{"=1.e3", "=1.e+3", "=1.E-3"} {
		if got := shiftCellReferences(formula, 1, 1, false); got != formula {
			t.Errorf("shiftCellReferences(%q) = %q", formula, got)
		}
	}
}

func TestFormulaReferenceShiftsPreserveSheetQualifiersAndBounds(t *testing.T) {
	book := &Spreadsheet{Sheets: []Sheet{{Name: "Summary"}, {Name: "Data"}}}
	for _, test := range []struct {
		name        string
		formula     string
		row, column int
		want        string
	}{
		{"qualified scalar", "=Data!A2", 1, 0, "=Data!A3"},
		{"second endpoint qualifier", "=SUM(A1:Data!A2)", 1, 0, "=SUM(Data!A2:A3)"},
		{"range exceeds row limit", "=SUM(Data!A1:A1000)", 1, 0, "=SUM(#REF!)"},
		{"scalar exceeds row limit", "=Data!A1000", 999, 0, "=#REF!"},
		{"range exceeds column limit", "=SUM(Data!IV1:IV2)", 0, MaxColumns, "=SUM(#REF!)"},
		{"scalar exceeds column limit", "=Data!IV1", 0, MaxColumns, "=#REF!"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := shiftCellReferencesForSheet(book, 0, 1, test.formula, test.row, test.column, false)
			if got != test.want {
				t.Errorf("shifted formula = %q, want %q", got, test.want)
			}
		})
	}
}

func TestInsertRowAtSheetLimitKeepsOutOfBoundsRangesValid(t *testing.T) {
	book := formulaChain(0)
	book.Sheets = []Sheet{
		{Name: "Summary", Rows: [][]Cell{{{Formula: "=SUM(Data!A1:A1000)"}}}},
		{Name: "Data", Rows: [][]Cell{{{Value: "1"}}}},
	}

	if err := InsertRowInBook(&book, "Data", 1); err != nil {
		t.Fatal(err)
	}
	if got, want := book.Sheets[0].Rows[0][0].Formula, "=SUM(#REF!)"; got != want {
		t.Fatalf("range formula after insertion = %q, want %q", got, want)
	}
	if err := Normalize(&book); err != nil {
		t.Fatalf("Normalize rejected the inserted row: %v", err)
	}
}

func TestNormalizeRejectsDeepFormulaChainsRegardlessOfCacheOrder(t *testing.T) {
	book := formulaChain(maxFormulaDepth + 1)
	if err := Normalize(&book); err == nil || !strings.Contains(err.Error(), "dependency depth") {
		t.Fatalf("Normalize accepted an over-deep formula chain: %v", err)
	}
}

func TestFormulaDepthBoundaryAgreesBetweenNormalizeAndEvaluate(t *testing.T) {
	book := formulaChain(maxFormulaDepth)
	valueV, err := Evaluate(&book, 0, len(book.Sheets[0].Rows), 1)
	var value float64
	if err == nil {
		value, err = valueV.Float64()
	}
	if err != nil || value != 1 {
		t.Fatalf("Evaluate at the depth limit = %v, %v", value, err)
	}
	if err := Normalize(&book); err != nil {
		t.Fatalf("Normalize rejected a chain at the depth limit: %v", err)
	}
	valueV, err = Evaluate(&book, 0, len(book.Sheets[0].Rows), 1)
	if err == nil {
		value, err = valueV.Float64()
	}
	if err != nil || value != 1 {
		t.Fatalf("Evaluate after Normalize = %v, %v", value, err)
	}
}

func formulaChain(formulas int) Spreadsheet {
	rows := make([][]Cell, formulas+1)
	rows[0] = []Cell{{Value: "1"}}
	for row := 2; row <= len(rows); row++ {
		rows[row-1] = []Cell{{Formula: "=" + CellName(row-1, 1)}}
	}
	return Spreadsheet{
		Artifact: artifact.Artifact{
			ID: "s12345", Kind: artifact.SpreadsheetKind,
			Title: "Deep formulas", FormatVersion: artifact.FormatVersion,
			CreatedAt: fixedTime, ModifiedAt: fixedTime,
		},
		Version: FileVersion, Sheets: []Sheet{{Name: "Sheet1", Rows: rows}},
	}
}

var fixedTime = mustTime()

func mustTime() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

type memoryRepository struct {
	books map[string]Spreadsheet
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{books: make(map[string]Spreadsheet)}
}

func (r *memoryRepository) GetSpreadsheet(_ context.Context, id string) (Spreadsheet, error) {
	book, ok := r.books[id]
	if !ok {
		return Spreadsheet{}, ErrNotFound
	}
	return cloneSpreadsheet(book), nil
}

func (r *memoryRepository) TransitionSpreadsheet(_ context.Context, id string, expected, target *Spreadsheet) error {
	current, exists := r.books[id]
	if expected == nil {
		if exists {
			return errors.New("already exists")
		}
	} else if !exists || !Equal(current, *expected) {
		return errors.New("stale workbook")
	}
	if target == nil {
		delete(r.books, id)
		return nil
	}
	r.books[id] = cloneSpreadsheet(*target)
	return nil
}

func TestUpdateSavesWorkbookAsSingleHistoryEntry(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryRepository()
	service := NewService(repository, 10)
	before, err := service.Create(ctx, "budget.md", [][]Cell{{{Value: "Rent"}}})
	if err != nil {
		t.Fatal(err)
	}

	updated := cloneSpreadsheet(before)
	for _, edit := range []SetCellOptions{
		{Sheet: "Sheet1", Row: 1, Column: 2, Cell: Cell{Value: "1200"}},
		{Sheet: "Sheet1", Row: 2, Column: 1, Cell: Cell{Formula: "=B1*2"}},
	} {
		if err := SetCellInBook(&updated, edit); err != nil {
			t.Fatal(err)
		}
	}
	if err := Normalize(&updated); err != nil {
		t.Fatal(err)
	}
	got, err := service.Update(ctx, before, updated)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Sheets, updated.Sheets) || !service.CanUndo() {
		t.Fatalf("updated workbook = %#v; CanUndo = %t", got.Sheets, service.CanUndo())
	}
	stored, err := service.Get(ctx, before.Path)
	if err != nil || !reflect.DeepEqual(stored.Sheets, updated.Sheets) {
		t.Fatalf("stored workbook = %#v, %v", stored.Sheets, err)
	}

	description, err := service.Undo(ctx)
	if err != nil || description != "Update spreadsheet" {
		t.Fatalf("Undo = %q, %v", description, err)
	}
	stored, err = service.Get(ctx, before.Path)
	if err != nil || !Equal(stored, before) {
		t.Fatalf("undo did not restore baseline: %#v, %v", stored, err)
	}
	description, err = service.Redo(ctx)
	if err != nil || description != "Update spreadsheet" {
		t.Fatalf("Redo = %q, %v", description, err)
	}
	stored, err = service.Get(ctx, before.Path)
	if err != nil || !reflect.DeepEqual(stored.Sheets, updated.Sheets) {
		t.Fatalf("redo did not restore saved workbook: %#v, %v", stored.Sheets, err)
	}
}

func TestUpdateNoopDoesNotCreateHistoryEntry(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryRepository()
	service := NewService(repository, 10)
	before, err := service.Create(ctx, "budget.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, before, before); err != nil {
		t.Fatal(err)
	}
	if service.CanUndo() {
		t.Fatal("no-op update created a history entry")
	}
}

func TestUpdateRejectsStaleBaseline(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryRepository()
	service := NewService(repository, 10)
	before, err := service.Create(ctx, "budget.md", [][]Cell{{{Value: "baseline"}}})
	if err != nil {
		t.Fatal(err)
	}
	updated := cloneSpreadsheet(before)
	if err := SetCellInBook(&updated, SetCellOptions{
		Sheet: "Sheet1", Row: 1, Column: 1, Cell: Cell{Value: "buffered"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetCell(ctx, before.Path, 1, 1, Cell{Value: "external"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, before, updated); err == nil {
		t.Fatal("stale buffered update overwrote an external change")
	}
	stored, err := service.Get(ctx, before.Path)
	if err != nil || stored.Sheets[0].Rows[0][0].Value != "external" {
		t.Fatalf("external change was not preserved: %#v, %v", stored.Sheets, err)
	}
}

func TestDecode(t *testing.T) {
	validItem := Spreadsheet{
		Artifact: artifact.Artifact{
			ID: "s12345", Kind: artifact.SpreadsheetKind,
			Title: "Budget", FormatVersion: artifact.FormatVersion,
			CreatedAt: fixedTime, ModifiedAt: fixedTime,
		},
		Version: FileVersion,
		Body:    "# Budget\n\nA visible Markdown description.",
		Sheets: []Sheet{{Name: "Sheet1", Rows: [][]Cell{
			{{Value: "Income"}, {Value: "125"}, {Formula: "=B1*2"}},
			{{Value: "Total"}, {}, {Formula: "=(B1+C1)/3"}},
		}}},
	}
	validItem.Blocks = map[string]json.RawMessage{
		"parchment-custom": json.RawMessage(`{"custom": "data"}`),
	}
	validData, err := Encode(validItem)
	if err != nil {
		t.Fatalf("failed to encode valid item: %v", err)
	}

	predefinedData := []byte("```parchment-meta\n{\n  \"parchment_format\": \"parchment-single-file-v2\",\n  \"kind\": \"spreadsheet\",\n  \"format_version\": 2,\n  \"created_at\": \"2026-10-28T14:10:36Z\",\n  \"modified_at\": \"2026-10-28T14:10:36Z\"\n}\n```\n\n<!-- parchment-body -->\n\nThis is the markdown body.\n\n<!-- parchment-blocks -->\n\n```parchment-custom-block\n{\n  \"custom\": \"data\"\n}\n```\n\n```parchment-spreadsheet\n{\n  \"version\": 1,\n  \"sheets\": [\n    {\n      \"name\": \"Sheet1\",\n      \"rows\": [\n        [\n          {\n            \"value\": \"125\"\n          }\n        ]\n      ]\n    }\n  ]\n}\n```\n")
	predefinedCreatedAt, _ := time.Parse(time.RFC3339, "2026-10-28T14:10:36Z")
	predefinedModifiedAt, _ := time.Parse(time.RFC3339, "2026-10-28T14:10:36Z")
	predefinedExpected := Spreadsheet{
		Artifact: artifact.Artifact{
			Kind:          artifact.SpreadsheetKind,
			FormatVersion: 2,
			CreatedAt:     predefinedCreatedAt.UTC(),
			ModifiedAt:    predefinedModifiedAt.UTC(),
		},
		Version: 1,
		Sheets: []Sheet{
			{
				Name: "Sheet1",
				Rows: [][]Cell{
					{
						{Value: "125"},
					},
				},
			},
		},
		Body: "This is the markdown body.\n",
		Blocks: map[string]json.RawMessage{
			"parchment-custom-block": json.RawMessage("{\n  \"custom\": \"data\"\n}"),
		},
	}

	tests := []struct {
		name         string
		dataFn       func() []byte
		wantErr      string
		wantNotFound bool
		expected     *Spreadsheet
	}{
		{
			name:   "valid generated",
			dataFn: func() []byte { return validData },
		},
		{
			name:     "valid predefined",
			dataFn:   func() []byte { return predefinedData },
			expected: &predefinedExpected,
		},
		{
			name:    "malformed artifactfile",
			dataFn:  func() []byte { return []byte("invalid data") },
			wantErr: "decode spreadsheet file",
		},
		{
			name: "wrong kind",
			dataFn: func() []byte {
				return []byte(strings.Replace(string(validData), `"kind": "spreadsheet"`, `"kind": "document"`, 1))
			},
			wantNotFound: true,
		},
		{
			name: "missing data block",
			dataFn: func() []byte {
				return []byte(strings.Replace(string(validData), "parchment-spreadsheet", "parchment-somethingelse", 1))
			},
			wantErr: "spreadsheet payload block is missing",
		},
		{
			name: "malformed data block",
			dataFn: func() []byte {
				return []byte(strings.Replace(string(validData), `"version": 1`, `"version": "abc"`, 1))
			},
			wantErr: "decode spreadsheet payload",
		},
		{
			name: "unsupported version",
			dataFn: func() []byte {
				return []byte(strings.Replace(string(validData), `"version": 1`, `"version": 999`, 1))
			},
			wantErr: "validate spreadsheet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decode(tt.dataFn())
			if tt.wantNotFound {
				if err != ErrNotFound {
					t.Errorf("got error %v, want ErrNotFound", err)
				}
				return
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("got error %v, want error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.expected != nil {
				if got.Artifact.Kind != tt.expected.Artifact.Kind {
					t.Errorf("got kind %v, want %v", got.Artifact.Kind, tt.expected.Artifact.Kind)
				}
				if got.Version != tt.expected.Version {
					t.Errorf("got version %v, want %v", got.Version, tt.expected.Version)
				}
				if !got.CreatedAt.Equal(tt.expected.CreatedAt) {
					t.Errorf("got CreatedAt %v, want %v", got.CreatedAt, tt.expected.CreatedAt)
				}
				if !got.ModifiedAt.Equal(tt.expected.ModifiedAt) {
					t.Errorf("got ModifiedAt %v, want %v", got.ModifiedAt, tt.expected.ModifiedAt)
				}
				if strings.TrimSpace(got.Body) != strings.TrimSpace(tt.expected.Body) {
					t.Errorf("got body %q, want %q", got.Body, tt.expected.Body)
				}
				if !reflect.DeepEqual(got.Sheets, tt.expected.Sheets) {
					t.Errorf("got sheets %+v, want %+v", got.Sheets, tt.expected.Sheets)
				}
				if !reflect.DeepEqual(got.Blocks, tt.expected.Blocks) {
					t.Errorf("got blocks %+v, want %+v", got.Blocks, tt.expected.Blocks)
				}
			} else {
				expected := cloneSpreadsheet(validItem)
				expected.ID = got.ID
				expected.Title = ""
				expected.Path = ""

				if !Equal(got, expected) {
					t.Errorf("got spreadsheet %+v, want %+v", got, expected)
				}
			}
		})
	}
}

func TestCellName(t *testing.T) {
	for _, test := range []struct {
		name        string
		row, column int
		want        string
	}{
		{"first cell", 1, 1, "A1"},
		{"Z boundary", 9, 26, "Z9"},
		{"AA boundary", 12, 27, "AA12"},
		{"IV boundary", 1000, 256, "IV1000"},
		{"AZ boundary", 1, 52, "AZ1"},
		{"BA boundary", 1, 53, "BA1"},
		{"ZZ boundary", 1, 702, "ZZ1"},
		{"AAA boundary", 1, 703, "AAA1"},
		{"Excel max limits", 1048576, 16384, "XFD1048576"},
		{"zero row", 0, 1, "A0"},
		{"zero column", 1, 0, "1"},
		{"zero both", 0, 0, "0"},
		{"negative row", -1, 1, "A-1"},
		{"negative column", 1, -1, "1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := CellName(test.row, test.column); got != test.want {
				t.Errorf("CellName(%d, %d) = %q, want %q", test.row, test.column, got, test.want)
			}
		})
	}
}

func TestSpreadsheetDeletions(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryRepository()
	service := NewService(repository, 10)
	book, err := service.Create(ctx, "deletions.md", [][]Cell{
		{{Value: "1"}, {Value: "2"}, {Value: "3"}},
		{{Value: "4"}, {Value: "5"}, {Formula: "=B1+1"}},
		{{Value: "7"}, {Value: "8"}, {Formula: "=C2+1"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	book, err = service.DeleteRow(ctx, book.Path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(book.Sheets[0].Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(book.Sheets[0].Rows))
	}
	if book.Sheets[0].Rows[1][2].Formula != "=#REF!+1" {
		t.Fatalf("expected formula to shift to #REF!+1, got %q", book.Sheets[0].Rows[1][2].Formula)
	}

	book, err = service.DeleteColumn(ctx, book.Path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(book.Sheets[0].Rows[0]) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(book.Sheets[0].Rows[0]))
	}

	_, err = service.AddSheet(ctx, book.Path, "Sheet2")
	if err != nil {
		t.Fatal(err)
	}
	book, err = service.DeleteSheet(ctx, book.Path, "Sheet2")
	if err != nil {
		t.Fatal(err)
	}
	if len(book.Sheets) != 1 {
		t.Fatalf("expected 1 sheet, got %d", len(book.Sheets))
	}
}

func TestDeleteRowShiftsRangesReferencingChangedSheet(t *testing.T) {
	book := &Spreadsheet{Sheets: []Sheet{
		{Name: "Summary", Rows: [][]Cell{{
			{Formula: "=SUM('Data Sheet'!A1:A4)"},
			{Formula: "=SUM(Other!A1:A4)"},
		}}},
		{Name: "Data Sheet", Rows: [][]Cell{
			{{Value: "1"}, {Formula: "=SUM(A1:A4)"}},
			{{Value: "2"}},
			{{Value: "3"}},
			{{Value: "4"}},
		}},
		{Name: "Other", Rows: [][]Cell{
			{{Value: "10"}},
			{{Value: "20"}},
			{{Value: "30"}},
			{{Value: "40"}},
		}},
	}}

	if err := DeleteRowFromBook(book, "Data Sheet", 2); err != nil {
		t.Fatal(err)
	}

	if got, want := book.Sheets[0].Rows[0][0].Formula, "=SUM('Data Sheet'!A1:A3)"; got != want {
		t.Errorf("range referencing deleted row = %q, want %q", got, want)
	}
	if got, want := book.Sheets[0].Rows[0][1].Formula, "=SUM(Other!A1:A4)"; got != want {
		t.Errorf("range referencing another sheet = %q, want unchanged %q", got, want)
	}
	if got, want := book.Sheets[1].Rows[0][1].Formula, "=SUM(A1:A3)"; got != want {
		t.Errorf("unqualified range on changed sheet = %q, want %q", got, want)
	}

	for _, test := range []struct {
		sheet, cell string
		want        float64
	}{
		{"Summary", "A1", 8},
		{"Summary", "B1", 100},
		{"Data Sheet", "B1", 8},
	} {
		sheet, err := SheetIndex(*book, test.sheet)
		if err != nil {
			t.Fatal(err)
		}
		row, column, err := CellCoordinates(test.cell)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Evaluate(book, sheet, row, column)
		if err != nil || got.Kind != NumKind || got.Num != test.want {
			t.Errorf("%s!%s = %+v, %v; want %v", test.sheet, test.cell, got, err, test.want)
		}
	}
}
