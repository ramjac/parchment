package spreadsheet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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
	Charts            []Chart                    `json:"charts,omitempty"`
	Body              string                     `json:"-"`
	Blocks            map[string]json.RawMessage `json:"-"`
}

type fileContent struct {
	Version int     `json:"version"`
	Sheets  []Sheet `json:"sheets"`
	Charts  []Chart `json:"charts,omitempty"`
}

// Repository persists spreadsheets as individual artifacts.
type Repository interface {
	GetSpreadsheet(context.Context, string) (Spreadsheet, error)
	TransitionSpreadsheet(context.Context, string, *Spreadsheet, *Spreadsheet) error
}

// Service applies spreadsheet edits and records successful operations in history.
type Service struct {
	repository Repository
	history    *history.Stack
	store      history.Store
	activePath string
	now        func() time.Time
}

func NewService(repository Repository, undoLimit int) *Service {
	service := &Service{
		repository: repository, history: history.New(undoLimit),
		now: func() time.Time { return time.Now().UTC() },
	}
	service.store, _ = repository.(history.Store)
	return service
}

// Get loads the spreadsheet stored at path.
func (s *Service) Get(ctx context.Context, path string) (Spreadsheet, error) {
	s.activePath = path
	return s.repository.GetSpreadsheet(ctx, path)
}

// Create writes a new spreadsheet file at path. Creation is not undoable.
func (s *Service) Create(ctx context.Context, path string, rows [][]Cell) (Spreadsheet, error) {
	id, err := artifact.NewID(artifact.SpreadsheetKind)
	if err != nil {
		return Spreadsheet{}, err
	}
	now := s.now().UTC()
	sheet := Spreadsheet{
		Artifact: artifact.Artifact{
			ID: id, Kind: artifact.SpreadsheetKind,
			CreatedAt: now, ModifiedAt: now, FormatVersion: artifact.FormatVersion,
			Path: path,
		},
		Version: FileVersion, Sheets: []Sheet{{Name: "Sheet1", Rows: cloneRows(rows)}},
	}
	if err := Normalize(&sheet); err != nil {
		return Spreadsheet{}, err
	}
	if err := s.repository.TransitionSpreadsheet(ctx, path, nil, &sheet); err != nil {
		return Spreadsheet{}, err
	}
	return s.repository.GetSpreadsheet(ctx, path)
}

// SetCellOptions contains options for setting a cell in a sheet.
type SetCellOptions struct {
	Sheet  string
	Row    int
	Column int
	Cell   Cell
}

func (s *Service) SetCell(ctx context.Context, path string, row, column int, cell Cell) (Spreadsheet, error) {
	return s.SetCellInSheet(ctx, path, SetCellOptions{
		Sheet:  "Sheet1",
		Row:    row,
		Column: column,
		Cell:   cell,
	})
}

func (s *Service) SetCellInSheet(ctx context.Context, path string, opts SetCellOptions) (Spreadsheet, error) {
	if opts.Row < 1 || opts.Row > MaxRows || opts.Column < 1 || opts.Column > MaxColumns {
		return Spreadsheet{}, fmt.Errorf("cell coordinates must be within %d rows and %d columns", MaxRows, MaxColumns)
	}
	if err := validateCell(opts.Cell); err != nil {
		return Spreadsheet{}, err
	}
	return s.modify(ctx, path, "Set cell", func(book *Spreadsheet) error {
		return SetCellInBook(book, opts)
	})
}

func (s *Service) InsertRow(ctx context.Context, path string, row int) (Spreadsheet, error) {
	return s.InsertRowInSheet(ctx, path, "Sheet1", row)
}

func (s *Service) InsertRowInSheet(ctx context.Context, path, name string, row int) (Spreadsheet, error) {
	return s.modify(ctx, path, "Insert row", func(book *Spreadsheet) error {
		return InsertRowInBook(book, name, row)
	})
}

func (s *Service) InsertColumn(ctx context.Context, path string, column int) (Spreadsheet, error) {
	return s.InsertColumnInSheet(ctx, path, "Sheet1", column)
}

func (s *Service) InsertColumnInSheet(ctx context.Context, path, name string, column int) (Spreadsheet, error) {
	return s.modify(ctx, path, "Insert column", func(book *Spreadsheet) error {
		return InsertColumnInBook(book, name, column)
	})
}

func (s *Service) AddSheet(ctx context.Context, path, name string) (Spreadsheet, error) {
	return s.modify(ctx, path, "Add sheet", func(book *Spreadsheet) error {
		return AddSheetToBook(book, name)
	})
}

// Update saves the edited sheets if expected still matches storage. All edits
// in one workbook buffer become a single undoable "Update spreadsheet" change.
func (s *Service) Update(ctx context.Context, expected, updated Spreadsheet) (Spreadsheet, error) {
	before := cloneSpreadsheet(expected)
	after := cloneSpreadsheet(expected)
	after.Sheets = cloneSpreadsheet(updated).Sheets
	after.Charts = append([]Chart(nil), updated.Charts...)
	if reflect.DeepEqual(before.Sheets, after.Sheets) && reflect.DeepEqual(before.Charts, after.Charts) {
		return before, nil
	}
	after.ModifiedAt = s.now().UTC()
	if err := Normalize(&after); err != nil {
		return Spreadsheet{}, err
	}
	if err := s.change(ctx, &before, &after, "Update spreadsheet"); err != nil {
		return Spreadsheet{}, err
	}
	return cloneSpreadsheet(after), nil
}

func (s *Service) Undo(ctx context.Context) (string, error) {
	if s.store != nil {
		return s.store.Undo(ctx, s.activePath)
	}
	return s.history.Undo(ctx)
}
func (s *Service) Redo(ctx context.Context) (string, error) {
	if s.store != nil {
		return s.store.Redo(ctx, s.activePath)
	}
	return s.history.Redo(ctx)
}
func (s *Service) CanUndo() bool {
	if s.store != nil && s.activePath != "" {
		canUndo, err := s.store.CanUndo(context.Background(), s.activePath)
		return err == nil && canUndo
	}
	return s.history.CanUndo()
}
func (s *Service) CanRedo() bool {
	if s.store != nil && s.activePath != "" {
		canRedo, err := s.store.CanRedo(context.Background(), s.activePath)
		return err == nil && canRedo
	}
	return s.history.CanRedo()
}

func (s *Service) modify(ctx context.Context, path, description string, edit func(*Spreadsheet) error) (Spreadsheet, error) {
	before, err := s.repository.GetSpreadsheet(ctx, path)
	if err != nil {
		return Spreadsheet{}, err
	}
	after := cloneSpreadsheet(before)
	if err := edit(&after); err != nil {
		return Spreadsheet{}, err
	}
	if reflect.DeepEqual(before.Sheets, after.Sheets) && reflect.DeepEqual(before.Charts, after.Charts) {
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
		if err := validateSheetName(sheet, names); err != nil {
			return err
		}
		names = append(names, sheet.Name)
		if err := normalizeSheetGrid(sheet); err != nil {
			return err
		}
	}
	if err := validateCharts(book.Charts); err != nil {
		return err
	}
	return validateFormulas(book)
}

func validateSheetName(sheet *Sheet, seen []string) error {
	sheet.Name = strings.TrimSpace(sheet.Name)
	if !utf8.ValidString(sheet.Name) {
		return errors.New("sheet names must be valid UTF-8")
	}
	for _, name := range seen {
		if strings.EqualFold(name, sheet.Name) {
			return errors.New("sheet names must be non-empty, at most 128 characters, and unique")
		}
	}
	if sheet.Name == "" || len(sheet.Name) > 128 {
		return errors.New("sheet names must be non-empty, at most 128 characters, and unique")
	}
	return nil
}

func normalizeSheetGrid(sheet *Sheet) error {
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
	return nil
}

func validateFormulas(book *Spreadsheet) error {
	evaluator := NewEvaluator(book)
	for sheetIndex := range book.Sheets {
		for row := range book.Sheets[sheetIndex].Rows {
			for column, cell := range book.Sheets[sheetIndex].Rows[row] {
				if cell.Formula == "" {
					continue
				}
				if val, err := evaluator.Evaluate(sheetIndex, row+1, column+1); err != nil {
					return fmt.Errorf("sheet %q cell %s formula: %w", book.Sheets[sheetIndex].Name,
						CellName(row+1, column+1), err)
				} else if val.Kind == ErrKind {
					continue
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
	book.Blocks = cloneBlocks(book.Blocks)
	book.Charts = append([]Chart(nil), book.Charts...)
	book.Sheets = append([]Sheet(nil), book.Sheets...)
	for i := range book.Sheets {
		book.Sheets[i].Rows = cloneRows(book.Sheets[i].Rows)
	}
	return book
}

func (s *Service) change(ctx context.Context, before, after *Spreadsheet, description string) error {
	s.activePath = before.Path
	if s.store != nil {
		return s.repository.TransitionSpreadsheet(history.WithDescription(ctx, description), before.Path, before, after)
	}
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

func shiftFormulas(book *Spreadsheet, sheetIndex, fromRow, fromColumn int, delete bool) {
	for formulaSheet := range book.Sheets {
		for row := range book.Sheets[formulaSheet].Rows {
			for column := range book.Sheets[formulaSheet].Rows[row] {
				cell := &book.Sheets[formulaSheet].Rows[row][column]
				if cell.Formula != "" {
					cell.Formula = shiftCellReferencesForSheet(book, formulaSheet, sheetIndex,
						cell.Formula, fromRow, fromColumn, delete)
				}
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
	if o.before == nil || o.after == nil {
		return errors.New("spreadsheet operation must have a before and after value")
	}
	return o.repository.TransitionSpreadsheet(ctx, o.before.Path, expected, target)
}

// Equal reports whether two spreadsheets have the same persisted value.
func Equal(left, right Spreadsheet) bool {
	left.Title, right.Title = "", ""
	return reflect.DeepEqual(left, right)
}

// Encode serializes the workbook as one Markdown artifact with a hidden
// parchment-spreadsheet JSON block.
func Encode(book Spreadsheet) ([]byte, error) {
	book = cloneSpreadsheet(book)
	if err := Normalize(&book); err != nil {
		return nil, err
	}
	content, err := json.MarshalIndent(fileContent{Version: book.Version, Sheets: book.Sheets, Charts: book.Charts}, "", "  ")
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
		Artifact: file.Artifact, Version: payload.Version, Sheets: payload.Sheets, Charts: payload.Charts,
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

func (s *Service) DeleteRow(ctx context.Context, path string, row int) (Spreadsheet, error) {
	return s.DeleteRowInSheet(ctx, path, "Sheet1", row)
}

func (s *Service) DeleteRowInSheet(ctx context.Context, path, name string, row int) (Spreadsheet, error) {
	return s.modify(ctx, path, "Delete row", func(book *Spreadsheet) error {
		return DeleteRowFromBook(book, name, row)
	})
}

func (s *Service) DeleteColumn(ctx context.Context, path string, column int) (Spreadsheet, error) {
	return s.DeleteColumnInSheet(ctx, path, "Sheet1", column)
}

func (s *Service) DeleteColumnInSheet(ctx context.Context, path, name string, column int) (Spreadsheet, error) {
	return s.modify(ctx, path, "Delete column", func(book *Spreadsheet) error {
		return DeleteColumnFromBook(book, name, column)
	})
}

func (s *Service) DeleteSheet(ctx context.Context, path, name string) (Spreadsheet, error) {
	return s.modify(ctx, path, "Delete sheet", func(sheet *Spreadsheet) error {
		return DeleteSheetFromBook(sheet, name)
	})
}

// SetCellInBook updates a cell in an in-memory workbook.
func SetCellInBook(book *Spreadsheet, opts SetCellOptions) error {
	if opts.Row < 1 || opts.Row > MaxRows || opts.Column < 1 || opts.Column > MaxColumns {
		return fmt.Errorf("cell coordinates must be within %d rows and %d columns", MaxRows, MaxColumns)
	}
	if err := validateCell(opts.Cell); err != nil {
		return err
	}
	index, err := SheetIndex(*book, opts.Sheet)
	if err != nil {
		return err
	}
	spills, err := NewEvaluator(book).SpillCells(index)
	if err != nil {
		return err
	}
	if _, ok := spills[[2]int{opts.Row, opts.Column}]; ok {
		return errors.New("cannot edit a spilled cell; edit its formula anchor")
	}
	return setCellInBook(book, index, opts)
}

func setCellInBook(book *Spreadsheet, index int, opts SetCellOptions) error {
	grid := &book.Sheets[index].Rows
	if opts.Row > len(*grid) {
		*grid = append(*grid, make([][]Cell, opts.Row-len(*grid))...)
	}
	for i := range *grid {
		if len((*grid)[i]) < opts.Column {
			(*grid)[i] = append((*grid)[i], make([]Cell, opts.Column-len((*grid)[i]))...)
		}
	}
	(*grid)[opts.Row-1][opts.Column-1] = opts.Cell
	return nil
}

// InsertRowInBook inserts a row into an in-memory workbook.
func InsertRowInBook(book *Spreadsheet, name string, row int) error {
	index, err := SheetIndex(*book, name)
	if err != nil {
		return err
	}
	grid := &book.Sheets[index].Rows
	if row < 1 || row > len(*grid)+1 || len(*grid) >= MaxRows {
		return fmt.Errorf("row must be between 1 and %d", min(len(*grid)+1, MaxRows))
	}
	inserted := make([]Cell, rowWidth(*grid))
	*grid = append(*grid, nil)
	copy((*grid)[row:], (*grid)[row-1:])
	(*grid)[row-1] = inserted
	shiftFormulas(book, index, row, 0, false)
	shiftCharts(book, name, row, 0, false)
	return nil
}

// InsertColumnInBook inserts a column into an in-memory workbook.
func InsertColumnInBook(book *Spreadsheet, name string, column int) error {
	index, err := SheetIndex(*book, name)
	if err != nil {
		return err
	}
	grid := &book.Sheets[index].Rows
	width := rowWidth(*grid)
	if column < 1 || column > width+1 || width >= MaxColumns {
		return fmt.Errorf("column must be between 1 and %d", min(width+1, MaxColumns))
	}
	for i := range *grid {
		(*grid)[i] = append((*grid)[i], Cell{})
		copy((*grid)[i][column:], (*grid)[i][column-1:])
		(*grid)[i][column-1] = Cell{}
	}
	shiftFormulas(book, index, 0, column, false)
	shiftCharts(book, name, 0, column, false)
	return nil
}

// AddSheetToBook appends a named sheet to an in-memory workbook.
func AddSheetToBook(book *Spreadsheet, name string) error {
	book.Sheets = append(book.Sheets, Sheet{Name: name, Rows: [][]Cell{{{}}}})
	return nil
}

// DeleteRowFromBook deletes a row from an in-memory workbook.
func DeleteRowFromBook(book *Spreadsheet, name string, row int) error {
	index, err := SheetIndex(*book, name)
	if err != nil {
		return err
	}
	grid := &book.Sheets[index].Rows
	if row < 1 || row > len(*grid) {
		return fmt.Errorf("row must be between 1 and %d", len(*grid))
	}
	shiftFormulas(book, index, row, 0, true)
	shiftCharts(book, name, row, 0, true)
	copy((*grid)[row-1:], (*grid)[row:])
	(*grid)[len(*grid)-1] = nil
	*grid = (*grid)[:len(*grid)-1]
	return nil
}

// DeleteColumnFromBook deletes a column from an in-memory workbook.
func DeleteColumnFromBook(book *Spreadsheet, name string, column int) error {
	index, err := SheetIndex(*book, name)
	if err != nil {
		return err
	}
	grid := &book.Sheets[index].Rows
	width := rowWidth(*grid)
	if column < 1 || column > width {
		return fmt.Errorf("column must be between 1 and %d", width)
	}
	shiftFormulas(book, index, 0, column, true)
	shiftCharts(book, name, 0, column, true)
	for i := range *grid {
		if column <= len((*grid)[i]) {
			copy((*grid)[i][column-1:], (*grid)[i][column:])
			(*grid)[i][len((*grid)[i])-1] = Cell{}
			(*grid)[i] = (*grid)[i][:len((*grid)[i])-1]
		}
	}
	return nil
}

// DeleteSheetFromBook deletes a sheet from an in-memory workbook.
func DeleteSheetFromBook(book *Spreadsheet, name string) error {
	if len(book.Sheets) <= 1 {
		return errors.New("cannot delete the only sheet")
	}
	index, err := SheetIndex(*book, name)
	if err != nil {
		return err
	}
	for i := range book.Charts {
		if strings.EqualFold(book.Charts[i].Sheet, name) {
			book.Charts[i].Invalid = true
		}
	}
	copy(book.Sheets[index:], book.Sheets[index+1:])
	book.Sheets = book.Sheets[:len(book.Sheets)-1]
	return nil
}
