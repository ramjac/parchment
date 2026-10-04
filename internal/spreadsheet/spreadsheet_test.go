package spreadsheet

import (
	"context"
	"errors"
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
			Location:  "parchment/artifacts/s12345/content.md",
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
	if !Equal(book, decoded) {
		t.Fatalf("round trip changed workbook:\noriginal: %+v\ndecoded: %+v", book, decoded)
	}
	value, err := Evaluate(&decoded, 0, 2, 3)
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

func TestNormalizeRejectsInvalidUTF8Title(t *testing.T) {
	book := formulaChain(0)
	book.Title += string([]byte{0xff})
	if err := Normalize(&book); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("invalid UTF-8 workbook title returned %v", err)
	}
}

func TestFormulaArithmeticReferencesAndCycles(t *testing.T) {
	book := Spreadsheet{
		Artifact: artifact.Artifact{
			ID: "s12345", Kind: artifact.SpreadsheetKind,
			Title: "Formula test", FormatVersion: artifact.FormatVersion,
			Location:  "parchment/artifacts/s12345/content.md",
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
		got, err := Evaluate(&book, 0, test.row, test.column)
		if err != nil || got != test.want {
			t.Fatalf("cell %s = %v, %v; want %v", CellName(test.row, test.column), got, err, test.want)
		}
	}
	book.Sheets[0].Rows[0][0] = Cell{Formula: "=B1"}
	book.Sheets[0].Rows[0][1] = Cell{Formula: "=A1"}
	if _, err := Evaluate(&book, 0, 1, 1); err == nil {
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
	got, err := Evaluate(&book, 0, 1, 1)
	if err != nil || got != 3 {
		t.Fatalf("formula with non-breaking space = %v, %v; want 3", got, err)
	}
}

func TestSpreadsheetOperationsFormulaShiftsAndHistory(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryRepository()
	service := NewService(repository, 20)
	book, err := service.Create(ctx, "Values", [][]Cell{{{Value: "2"}, {Value: "3"}, {Formula: "=A1+B1"}}})
	if err != nil {
		t.Fatal(err)
	}
	value, err := Evaluate(&book, 0, 1, 3)
	if err != nil || value != 5 {
		t.Fatalf("initial formula = %v, %v", value, err)
	}
	updated, err := service.InsertRow(ctx, book.ID, 1)
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
	undone, err := service.Get(ctx, book.ID)
	if err != nil || len(undone.Sheets[0].Rows) != 1 || undone.Sheets[0].Rows[0][2].Formula != "=A1+B1" {
		t.Fatalf("undo did not restore sheet: %+v, %v", undone, err)
	}
	if _, err := service.Redo(ctx); err != nil {
		t.Fatal(err)
	}
	redone, err := service.Get(ctx, book.ID)
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

func TestFormulaReferenceShiftsPreserveDecimalExponents(t *testing.T) {
	for _, formula := range []string{"=1.e3", "=1.e+3", "=1.E-3"} {
		if got := shiftCellReferences(formula, 1, 1); got != formula {
			t.Errorf("shiftCellReferences(%q) = %q", formula, got)
		}
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
	value, err := Evaluate(&book, 0, len(book.Sheets[0].Rows), 1)
	if err != nil || value != 1 {
		t.Fatalf("Evaluate at the depth limit = %v, %v", value, err)
	}
	if err := Normalize(&book); err != nil {
		t.Fatalf("Normalize rejected a chain at the depth limit: %v", err)
	}
	value, err = Evaluate(&book, 0, len(book.Sheets[0].Rows), 1)
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
			Location:  "parchment/artifacts/s12345/content.md",
			CreatedAt: fixedTime, ModifiedAt: fixedTime,
		},
		Version: FileVersion, Sheets: []Sheet{{Name: "Sheet1", Rows: rows}},
	}
}

func TestEqualTreatsEmptyMetadataSlicesAsNil(t *testing.T) {
	left := Spreadsheet{
		Artifact: artifact.Artifact{
			ID: "s12345", Kind: artifact.SpreadsheetKind,
			Title: "Metadata", FormatVersion: artifact.FormatVersion,
			Location:  "parchment/artifacts/s12345/content.md",
			CreatedAt: fixedTime, ModifiedAt: fixedTime,
		},
		Version: FileVersion, Sheets: []Sheet{{Name: "Sheet1", Rows: [][]Cell{{{Value: "1"}}}}},
	}
	right := left
	right.Tags = []string{}
	right.Links = []string{}
	if !Equal(left, right) {
		t.Fatal("empty tags and links should equal nil slices")
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

func (*memoryRepository) ArtifactLocation(id string) string {
	return "parchment/artifacts/" + id + "/content.md"
}

func (r *memoryRepository) ListSpreadsheets(context.Context) ([]Spreadsheet, error) {
	var books []Spreadsheet
	for _, book := range r.books {
		books = append(books, cloneSpreadsheet(book))
	}
	return books, nil
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
