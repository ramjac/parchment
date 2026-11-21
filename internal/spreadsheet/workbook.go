package spreadsheet

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

const EmbeddedWorkbookVersion = 1

// EmbeddedWorkbookPayload is the versioned payload stored by document and
// presentation hosts.
type EmbeddedWorkbookPayload struct {
	Version int `json:"version"`
	Workbook
}

// Clone returns an independent copy of the workbook data.
func (book Workbook) Clone() Workbook { return cloneWorkbook(book) }

// Equal reports whether two workbooks contain the same sheets and charts.
func (book Workbook) Equal(other Workbook) bool {
	return reflect.DeepEqual(book, other)
}

// NewEmbeddedSheet creates a single blank sheet with a stable host reference ID.
func NewEmbeddedSheet(name string) (Sheet, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || !utf8.ValidString(name) {
		return Sheet{}, errors.New("sheet names must be non-empty, at most 128 characters, and unique")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Sheet{}, fmt.Errorf("create embedded sheet ID: %w", err)
	}
	return Sheet{ID: hex.EncodeToString(id[:]), Name: name, Rows: [][]Cell{{{}}}}, nil
}

// NormalizeEmbeddedWorkbook validates a host workbook and requires stable IDs
// for each embedded sheet.
func NormalizeEmbeddedWorkbook(book *Workbook) error {
	if err := NormalizeWorkbook(book); err != nil {
		return err
	}
	ids := make(map[string]bool, len(book.Sheets))
	for _, sheet := range book.Sheets {
		if sheet.ID == "" || !utf8.ValidString(sheet.ID) || len(sheet.ID) > 128 || ids[sheet.ID] {
			return errors.New("embedded sheets require unique, nonempty UTF-8 IDs")
		}
		ids[sheet.ID] = true
	}
	return nil
}

// SheetIndexByID finds a host sheet by its stable ID.
func SheetIndexByID(book Workbook, id string) (int, error) {
	for i, sheet := range book.Sheets {
		if sheet.ID == id {
			return i, nil
		}
	}
	return 0, fmt.Errorf("workbook has no sheet with ID %q", id)
}

// EncodeEmbeddedWorkbook serializes a validated host workbook payload.
func EncodeEmbeddedWorkbook(book Workbook) ([]byte, error) {
	book = book.Clone()
	if err := NormalizeEmbeddedWorkbook(&book); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(EmbeddedWorkbookPayload{
		Version: EmbeddedWorkbookVersion, Workbook: book,
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode embedded workbook: %w", err)
	}
	return data, nil
}

// DecodeEmbeddedWorkbook decodes and validates a host workbook payload.
func DecodeEmbeddedWorkbook(data []byte) (Workbook, error) {
	var payload EmbeddedWorkbookPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return Workbook{}, fmt.Errorf("decode embedded workbook: %w", err)
	}
	if payload.Version != EmbeddedWorkbookVersion {
		return Workbook{}, fmt.Errorf("unsupported embedded workbook version %d", payload.Version)
	}
	if err := NormalizeEmbeddedWorkbook(&payload.Workbook); err != nil {
		return Workbook{}, fmt.Errorf("validate embedded workbook: %w", err)
	}
	return payload.Workbook, nil
}

func (book *Workbook) edit(edit func(*Spreadsheet) error) error {
	view := book.spreadsheetView()
	err := edit(view)
	book.Sheets, book.Charts = view.Sheets, view.Charts
	return err
}

// SetCell updates a cell in workbook data without artifact metadata.
func (book *Workbook) SetCell(opts SetCellOptions) error {
	return book.edit(func(view *Spreadsheet) error { return SetCellInBook(view, opts) })
}

// InsertRow inserts a row and adjusts formulas and chart ranges.
func (book *Workbook) InsertRow(sheet string, row int) error {
	return book.edit(func(view *Spreadsheet) error { return InsertRowInBook(view, sheet, row) })
}

// InsertColumn inserts a column and adjusts formulas and chart ranges.
func (book *Workbook) InsertColumn(sheet string, column int) error {
	return book.edit(func(view *Spreadsheet) error { return InsertColumnInBook(view, sheet, column) })
}

// DeleteRow deletes a row and adjusts formulas and chart ranges.
func (book *Workbook) DeleteRow(sheet string, row int) error {
	return book.edit(func(view *Spreadsheet) error { return DeleteRowFromBook(view, sheet, row) })
}

// DeleteColumn deletes a column and adjusts formulas and chart ranges.
func (book *Workbook) DeleteColumn(sheet string, column int) error {
	return book.edit(func(view *Spreadsheet) error { return DeleteColumnFromBook(view, sheet, column) })
}

// AddSheet appends a named sheet.
func (book *Workbook) AddSheet(name string) error {
	return book.edit(func(view *Spreadsheet) error { return AddSheetToBook(view, name) })
}

// DeleteSheet removes a sheet and invalidates charts whose source used it.
func (book *Workbook) DeleteSheet(name string) error {
	return book.edit(func(view *Spreadsheet) error { return DeleteSheetFromBook(view, name) })
}

// SpanCells merges an empty rectangle around its top-left cell.
func (book *Workbook) SpanCells(sheet string, span CellSpan) error {
	return book.edit(func(view *Spreadsheet) error { return SpanCellsInBook(view, sheet, span) })
}

// SplitCell removes a span without changing its anchor value.
func (book *Workbook) SplitCell(sheet string, row, column int) error {
	return book.edit(func(view *Spreadsheet) error { return SplitCellInBook(view, sheet, row, column) })
}

// AddChart creates a grid-anchored chart.
func (book *Workbook) AddChart(chart Chart) (Chart, error) {
	var added Chart
	err := book.edit(func(view *Spreadsheet) error {
		var err error
		added, err = AddChartToBook(view, chart)
		return err
	})
	return added, err
}

// UpdateChart updates a chart without changing its grid placement.
func (book *Workbook) UpdateChart(chart Chart) error {
	return book.edit(func(view *Spreadsheet) error { return UpdateChartInBook(view, chart) })
}

// DeleteChart removes a chart by ID.
func (book *Workbook) DeleteChart(id string) error {
	return book.edit(func(view *Spreadsheet) error { return DeleteChartFromBook(view, id) })
}

// ChartAtInWorkbook returns the chart anchored at a physical cell.
func ChartAtInWorkbook(book *Workbook, sheet string, row, column int) (Chart, bool) {
	return ChartAt(book.spreadsheetView(), sheet, row, column)
}

// NewWorkbookEvaluator creates an evaluator for workbook data.
func NewWorkbookEvaluator(book *Workbook) *Evaluator {
	return NewEvaluator(book.spreadsheetView())
}

// NewWorkbookEvaluatorAt evaluates volatile time functions against a fixed UTC
// instant.
func NewWorkbookEvaluatorAt(book *Workbook, now time.Time) *Evaluator {
	return NewEvaluatorAt(book.spreadsheetView(), now)
}

// EvaluateWorkbook calculates one cell in workbook data.
func EvaluateWorkbook(book *Workbook, sheetIndex, row, column int) (Value, error) {
	return NewWorkbookEvaluator(book).Evaluate(sheetIndex, row, column)
}

// EvaluateChartInWorkbook evaluates a grid-anchored chart against workbook
// data, including source ranges on other sheets in the same workbook.
func EvaluateChartInWorkbook(book *Workbook, chart Chart) (ChartData, error) {
	return EvaluateChart(book.spreadsheetView(), chart)
}

// SpillCellsInWorkbook returns the array cells produced on one sheet.
func SpillCellsInWorkbook(book *Workbook, sheetIndex int) (map[[2]int]Value, error) {
	return NewWorkbookEvaluator(book).SpillCells(sheetIndex)
}
