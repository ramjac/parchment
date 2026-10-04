package spreadsheet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/history"
)

const (
	FileVersion      = 1
	MaxRows          = 1000
	MaxColumns       = 256
	MaxSheets        = 32
	MaxFormulaLength = 4096
)

var ErrNotFound = errors.New("spreadsheet not found")

// Cell stores either a literal value or a formula. Formulas are explicit JSON
// fields so text beginning with '=' is never silently reinterpreted.
type Cell struct {
	Value   string `json:"value,omitempty"`
	Formula string `json:"formula,omitempty"`
}

func cloneBlocks(blocks map[string]json.RawMessage) map[string]json.RawMessage {
	return cloneBlocksExcept(blocks)
}

func cloneBlocksExcept(blocks map[string]json.RawMessage, excluded ...string) map[string]json.RawMessage {
	if len(blocks) == 0 {
		return nil
	}
	exclude := make(map[string]bool, len(excluded))
	for _, name := range excluded {
		exclude[name] = true
	}
	clone := make(map[string]json.RawMessage, len(blocks))
	for name, payload := range blocks {
		if exclude[name] {
			continue
		}
		clone[name] = append(json.RawMessage(nil), payload...)
	}
	if len(clone) == 0 {
		return nil
	}
	return clone
}

// Sheet is a named two-dimensional array of cells.
type Sheet struct {
	Name string   `json:"name"`
	Rows [][]Cell `json:"rows"`
}

// Spreadsheet is a complete, self-contained text workbook.
type Spreadsheet struct {
	artifact.Artifact `json:"artifact"`
	Version           int                        `json:"version"`
	Sheets            []Sheet                    `json:"sheets"`
	Body              string                     `json:"-"`
	Blocks            map[string]json.RawMessage `json:"-"`
}

type fileContent struct {
	Version int     `json:"version"`
	Sheets  []Sheet `json:"sheets"`
}

// Repository persists spreadsheets as individual artifacts.
type Repository interface {
	ArtifactLocation(string) string
	ListSpreadsheets(context.Context) ([]Spreadsheet, error)
	GetSpreadsheet(context.Context, string) (Spreadsheet, error)
	TransitionSpreadsheet(context.Context, string, *Spreadsheet, *Spreadsheet) error
}

// Service applies spreadsheet edits and records successful operations in history.
type Service struct {
	repository Repository
	history    *history.Stack
	now        func() time.Time
}

func NewService(repository Repository, undoLimit int) *Service {
	return &Service{
		repository: repository, history: history.New(undoLimit),
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) List(ctx context.Context) ([]Spreadsheet, error) {
	sheets, err := s.repository.ListSpreadsheets(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(sheets, func(i, j int) bool {
		return sheets[i].ModifiedAt.After(sheets[j].ModifiedAt)
	})
	return sheets, nil
}

func (s *Service) Get(ctx context.Context, id string) (Spreadsheet, error) {
	return s.repository.GetSpreadsheet(ctx, id)
}

func (s *Service) Create(ctx context.Context, title string, rows [][]Cell) (Spreadsheet, error) {
	title = strings.TrimSpace(title)
	id, err := artifact.NewID(artifact.SpreadsheetKind)
	if err != nil {
		return Spreadsheet{}, err
	}
	now := s.now().UTC()
	sheet := Spreadsheet{
		Artifact: artifact.Artifact{
			ID: id, Kind: artifact.SpreadsheetKind, Title: title,
			CreatedAt: now, ModifiedAt: now, FormatVersion: artifact.FormatVersion,
			Location: s.repository.ArtifactLocation(id),
		},
		Version: FileVersion, Sheets: []Sheet{{Name: "Sheet1", Rows: cloneRows(rows)}},
	}
	if err := Normalize(&sheet); err != nil {
		return Spreadsheet{}, err
	}
	if err := s.change(ctx, nil, &sheet, "Create spreadsheet"); err != nil {
		return Spreadsheet{}, err
	}
	return cloneSpreadsheet(sheet), nil
}

func (s *Service) SetCell(ctx context.Context, id string, row, column int, cell Cell) (Spreadsheet, error) {
	return s.SetCellInSheet(ctx, id, "Sheet1", row, column, cell)
}

func (s *Service) SetCellInSheet(ctx context.Context, id, name string, row, column int, cell Cell) (Spreadsheet, error) {
	if row < 1 || row > MaxRows || column < 1 || column > MaxColumns {
		return Spreadsheet{}, fmt.Errorf("cell coordinates must be within %d rows and %d columns", MaxRows, MaxColumns)
	}
	if err := validateCell(cell); err != nil {
		return Spreadsheet{}, err
	}
	return s.modify(ctx, id, "Set cell", func(sheet *Spreadsheet) error {
		index, err := SheetIndex(*sheet, name)
		if err != nil {
			return err
		}
		grid := &sheet.Sheets[index].Rows
		if row > len(*grid) {
			*grid = append(*grid, make([][]Cell, row-len(*grid))...)
		}
		for i := range *grid {
			if len((*grid)[i]) < column {
				(*grid)[i] = append((*grid)[i], make([]Cell, column-len((*grid)[i]))...)
			}
		}
		(*grid)[row-1][column-1] = cell
		return nil
	})
}

func (s *Service) InsertRow(ctx context.Context, id string, row int) (Spreadsheet, error) {
	return s.InsertRowInSheet(ctx, id, "Sheet1", row)
}

func (s *Service) InsertRowInSheet(ctx context.Context, id, name string, row int) (Spreadsheet, error) {
	return s.modify(ctx, id, "Insert row", func(sheet *Spreadsheet) error {
		index, err := SheetIndex(*sheet, name)
		if err != nil {
			return err
		}
		grid := &sheet.Sheets[index].Rows
		if row < 1 || row > len(*grid)+1 || len(*grid) >= MaxRows {
			return fmt.Errorf("row must be between 1 and %d", min(len(*grid)+1, MaxRows))
		}
		inserted := make([]Cell, rowWidth(*grid))
		*grid = append(*grid, nil)
		copy((*grid)[row:], (*grid)[row-1:])
		(*grid)[row-1] = inserted
		shiftFormulas(sheet, index, row, 0)
		return nil
	})
}

func (s *Service) InsertColumn(ctx context.Context, id string, column int) (Spreadsheet, error) {
	return s.InsertColumnInSheet(ctx, id, "Sheet1", column)
}

func (s *Service) InsertColumnInSheet(ctx context.Context, id, name string, column int) (Spreadsheet, error) {
	return s.modify(ctx, id, "Insert column", func(sheet *Spreadsheet) error {
		index, err := SheetIndex(*sheet, name)
		if err != nil {
			return err
		}
		grid := &sheet.Sheets[index].Rows
		width := rowWidth(*grid)
		if column < 1 || column > width+1 || width >= MaxColumns {
			return fmt.Errorf("column must be between 1 and %d", min(width+1, MaxColumns))
		}
		for i := range *grid {
			(*grid)[i] = append((*grid)[i], Cell{})
			copy((*grid)[i][column:], (*grid)[i][column-1:])
			(*grid)[i][column-1] = Cell{}
		}
		shiftFormulas(sheet, index, 0, column)
		return nil
	})
}

func (s *Service) AddSheet(ctx context.Context, id, name string) (Spreadsheet, error) {
	return s.modify(ctx, id, "Add sheet", func(book *Spreadsheet) error {
		book.Sheets = append(book.Sheets, Sheet{Name: name, Rows: [][]Cell{{{}}}})
		return nil
	})
}

func (s *Service) Delete(ctx context.Context, id string) error {
	before, err := s.repository.GetSpreadsheet(ctx, id)
	if err != nil {
		return err
	}
	return s.change(ctx, &before, nil, "Delete spreadsheet")
}

func (s *Service) Undo(ctx context.Context) (string, error) { return s.history.Undo(ctx) }
func (s *Service) Redo(ctx context.Context) (string, error) { return s.history.Redo(ctx) }
func (s *Service) CanUndo() bool                            { return s.history.CanUndo() }
func (s *Service) CanRedo() bool                            { return s.history.CanRedo() }

func (s *Service) modify(ctx context.Context, id, description string, edit func(*Spreadsheet) error) (Spreadsheet, error) {
	before, err := s.repository.GetSpreadsheet(ctx, id)
	if err != nil {
		return Spreadsheet{}, err
	}
	after := cloneSpreadsheet(before)
	if err := edit(&after); err != nil {
		return Spreadsheet{}, err
	}
	if reflect.DeepEqual(before.Sheets, after.Sheets) {
		return before, nil
	}
	after.ModifiedAt = s.now().UTC()
	if err := Normalize(&after); err != nil {
		return Spreadsheet{}, err
	}
	if err := s.change(ctx, &before, &after, description); err != nil {
		return Spreadsheet{}, err
	}
	return cloneSpreadsheet(after), nil
}

// Normalize validates workbook metadata, sheets, dimensions, and formulas.
func Normalize(book *Spreadsheet) error {
	if book.Version != FileVersion {
		return fmt.Errorf("unsupported spreadsheet version %d", book.Version)
	}
	if err := book.Artifact.Validate(); err != nil {
		return err
	}
	if book.Kind != artifact.SpreadsheetKind {
		return errors.New("artifact is not a spreadsheet")
	}
	book.CreatedAt = book.CreatedAt.UTC()
	book.ModifiedAt = book.ModifiedAt.UTC()
	if len(book.Sheets) == 0 || len(book.Sheets) > MaxSheets {
		return fmt.Errorf("spreadsheet must have between 1 and %d sheets", MaxSheets)
	}
	var names []string
	for i := range book.Sheets {
		sheet := &book.Sheets[i]
		sheet.Name = strings.TrimSpace(sheet.Name)
		if !utf8.ValidString(sheet.Name) {
			return errors.New("sheet names must be valid UTF-8")
		}
		duplicate := false
		for _, name := range names {
			if strings.EqualFold(name, sheet.Name) {
				duplicate = true
				break
			}
		}
		if sheet.Name == "" || len(sheet.Name) > 128 || duplicate {
			return errors.New("sheet names must be non-empty, at most 128 characters, and unique")
		}
		names = append(names, sheet.Name)
		if len(sheet.Rows) == 0 {
			sheet.Rows = [][]Cell{{{}}}
		}
		width := rowWidth(sheet.Rows)
		if len(sheet.Rows) > MaxRows || width > MaxColumns {
			return fmt.Errorf("sheet %q cannot exceed %d rows or %d columns", sheet.Name, MaxRows, MaxColumns)
		}
		for row := range sheet.Rows {
			if len(sheet.Rows[row]) < width {
				sheet.Rows[row] = append(sheet.Rows[row], make([]Cell, width-len(sheet.Rows[row]))...)
			}
			for column, cell := range sheet.Rows[row] {
				if err := validateCell(cell); err != nil {
					return fmt.Errorf("sheet %q cell %s: %w", sheet.Name, CellName(row+1, column+1), err)
				}
			}
		}
	}
	evaluator := evaluator{
		book: book, visiting: make(map[cellCoordinate]bool),
		cache: make(map[cellCoordinate]float64), depth: make(map[cellCoordinate]int),
	}
	for sheetIndex := range book.Sheets {
		for row := range book.Sheets[sheetIndex].Rows {
			for column, cell := range book.Sheets[sheetIndex].Rows[row] {
				if cell.Formula == "" {
					continue
				}
				if _, err := evaluator.cell(sheetIndex, row+1, column+1); err != nil {
					return fmt.Errorf("sheet %q cell %s formula: %w", book.Sheets[sheetIndex].Name,
						CellName(row+1, column+1), err)
				}
			}
		}
	}
	return nil
}

func validateCell(cell Cell) error {
	if !utf8.ValidString(cell.Value) || !utf8.ValidString(cell.Formula) {
		return errors.New("cell text must be valid UTF-8")
	}
	if cell.Formula != "" && cell.Value != "" {
		return errors.New("cell cannot contain both a value and a formula")
	}
	if cell.Formula != "" && !strings.HasPrefix(cell.Formula, "=") {
		return errors.New("formula must begin with '='")
	}
	if len(cell.Formula) > MaxFormulaLength {
		return fmt.Errorf("formula is longer than %d bytes", MaxFormulaLength)
	}
	return nil
}

func rowWidth(rows [][]Cell) int {
	width := 1
	for _, row := range rows {
		if len(row) > width {
			width = len(row)
		}
	}
	return width
}

func cloneRows(rows [][]Cell) [][]Cell {
	clone := make([][]Cell, len(rows))
	for i := range rows {
		clone[i] = append([]Cell(nil), rows[i]...)
	}
	return clone
}

func cloneSpreadsheet(book Spreadsheet) Spreadsheet {
	book.Tags = append([]string(nil), book.Tags...)
	book.Links = append([]string(nil), book.Links...)
	book.Blocks = cloneBlocks(book.Blocks)
	book.Sheets = append([]Sheet(nil), book.Sheets...)
	for i := range book.Sheets {
		book.Sheets[i].Rows = cloneRows(book.Sheets[i].Rows)
	}
	return book
}

func (s *Service) change(ctx context.Context, before, after *Spreadsheet, description string) error {
	return s.history.Execute(ctx, spreadsheetOperation{
		repository: s.repository, before: cloneSpreadsheetPointer(before),
		after: cloneSpreadsheetPointer(after), description: description,
	})
}

func cloneSpreadsheetPointer(book *Spreadsheet) *Spreadsheet {
	if book == nil {
		return nil
	}
	clone := cloneSpreadsheet(*book)
	return &clone
}

func shiftFormulas(book *Spreadsheet, sheetIndex, fromRow, fromColumn int) {
	for row := range book.Sheets[sheetIndex].Rows {
		for column := range book.Sheets[sheetIndex].Rows[row] {
			cell := &book.Sheets[sheetIndex].Rows[row][column]
			if cell.Formula != "" {
				cell.Formula = shiftCellReferences(cell.Formula, fromRow, fromColumn)
			}
		}
	}
}

// SheetIndex finds a sheet name case-insensitively.
func SheetIndex(book Spreadsheet, name string) (int, error) {
	for i := range book.Sheets {
		if strings.EqualFold(book.Sheets[i].Name, name) {
			return i, nil
		}
	}
	return 0, fmt.Errorf("spreadsheet has no sheet named %q", name)
}

type spreadsheetOperation struct {
	repository  Repository
	before      *Spreadsheet
	after       *Spreadsheet
	description string
}

func (o spreadsheetOperation) Apply(ctx context.Context) error {
	return o.transition(ctx, o.before, o.after)
}
func (o spreadsheetOperation) Undo(ctx context.Context) error {
	return o.transition(ctx, o.after, o.before)
}
func (o spreadsheetOperation) Description() string { return o.description }
func (o spreadsheetOperation) transition(ctx context.Context, expected, target *Spreadsheet) error {
	id := ""
	if o.before != nil {
		id = o.before.ID
	} else if o.after != nil {
		id = o.after.ID
	} else {
		return errors.New("spreadsheet operation has no artifact")
	}
	return o.repository.TransitionSpreadsheet(ctx, id, expected, target)
}

// Equal reports whether two spreadsheets have the same persisted value.
func Equal(left, right Spreadsheet) bool {
	left.Title, right.Title = "", ""
	if len(left.Tags) == 0 {
		left.Tags = nil
	}
	if len(right.Tags) == 0 {
		right.Tags = nil
	}
	if len(left.Links) == 0 {
		left.Links = nil
	}
	if len(right.Links) == 0 {
		right.Links = nil
	}
	return reflect.DeepEqual(left, right)
}

// Encode serializes the workbook as one Markdown artifact with a hidden
// parchment-spreadsheet JSON block.
func Encode(book Spreadsheet) ([]byte, error) {
	book = cloneSpreadsheet(book)
	if err := Normalize(&book); err != nil {
		return nil, err
	}
	content, err := json.MarshalIndent(fileContent{Version: book.Version, Sheets: book.Sheets}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode spreadsheet: %w", err)
	}
	blocks := make(map[string]any, len(book.Blocks)+1)
	for name, payload := range book.Blocks {
		blocks[name] = payload
	}
	blocks["parchment-spreadsheet"] = json.RawMessage(content)
	return artifactfile.Encode(book.Artifact, book.Body, blocks)
}

// Decode parses and validates a Markdown workbook artifact.
func Decode(data []byte) (Spreadsheet, error) {
	file, err := artifactfile.Decode(data)
	if err != nil {
		return Spreadsheet{}, fmt.Errorf("decode spreadsheet file: %w", err)
	}
	if file.Artifact.Kind != artifact.SpreadsheetKind {
		return Spreadsheet{}, ErrNotFound
	}
	content, ok := file.Blocks["parchment-spreadsheet"]
	if !ok {
		return Spreadsheet{}, errors.New("spreadsheet payload block is missing")
	}
	var payload fileContent
	if err := json.Unmarshal(content, &payload); err != nil {
		return Spreadsheet{}, fmt.Errorf("decode spreadsheet payload: %w", err)
	}
	book := Spreadsheet{
		Artifact: file.Artifact, Version: payload.Version, Sheets: payload.Sheets,
		Body: file.Body, Blocks: cloneBlocksExcept(file.Blocks, "parchment-spreadsheet"),
	}
	if err := Normalize(&book); err != nil {
		return Spreadsheet{}, fmt.Errorf("validate spreadsheet: %w", err)
	}
	return book, nil
}

// CellName converts one-based row and column coordinates to A1 notation.
func CellName(row, column int) string {
	var name []byte
	for column > 0 {
		column--
		name = append([]byte{byte('A' + column%26)}, name...)
		column /= 26
	}
	return fmt.Sprintf("%s%d", name, row)
}

// CellCoordinates parses an A1 cell reference.
func CellCoordinates(reference string) (row, column int, err error) {
	i := 0
	for i < len(reference) && ((reference[i] >= 'A' && reference[i] <= 'Z') ||
		(reference[i] >= 'a' && reference[i] <= 'z')) {
		value := reference[i]
		if value >= 'a' {
			value -= 'a' - 'A'
		}
		column = column*26 + int(value-'A'+1)
		if column > MaxColumns {
			return 0, 0, fmt.Errorf("cell reference %q is outside supported dimensions", reference)
		}
		i++
	}
	if i == 0 || i == len(reference) {
		return 0, 0, fmt.Errorf("invalid cell reference %q", reference)
	}
	for ; i < len(reference); i++ {
		if reference[i] < '0' || reference[i] > '9' {
			return 0, 0, fmt.Errorf("invalid cell reference %q", reference)
		}
		row = row*10 + int(reference[i]-'0')
		if row > MaxRows {
			return 0, 0, fmt.Errorf("cell reference %q is outside supported dimensions", reference)
		}
	}
	if row < 1 || row > MaxRows || column < 1 || column > MaxColumns {
		return 0, 0, fmt.Errorf("cell reference %q is outside supported dimensions", reference)
	}
	return row, column, nil
}
