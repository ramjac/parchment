package spreadsheet

import "fmt"

// CellClipboard is an immutable snapshot of a rectangular source range.
type CellClipboard struct {
	Row, Column int
	Cells       [][]Cell
}

// CopyRange snapshots a rectangular range, including empty cells.
func CopyRange(book Spreadsheet, sheet string, firstRow, firstColumn, lastRow, lastColumn int) (CellClipboard, error) {
	if firstRow < 1 || firstColumn < 1 || lastRow < firstRow || lastColumn < firstColumn ||
		lastRow > MaxRows || lastColumn > MaxColumns {
		return CellClipboard{}, fmt.Errorf("copy range is outside the workbook")
	}
	index, err := SheetIndex(book, sheet)
	if err != nil {
		return CellClipboard{}, err
	}
	snapshot := CellClipboard{Row: firstRow, Column: firstColumn, Cells: make([][]Cell, lastRow-firstRow+1)}
	rows := book.Sheets[index].Rows
	evaluator := NewEvaluator(&book)
	spills, err := evaluator.SpillCells(index)
	if err != nil {
		return CellClipboard{}, err
	}
	for row := range snapshot.Cells {
		snapshot.Cells[row] = make([]Cell, lastColumn-firstColumn+1)
		sourceRow := firstRow + row - 1
		for column := range snapshot.Cells[row] {
			sourceColumn := firstColumn + column - 1
			if value, ok := spills[[2]int{sourceRow + 1, sourceColumn + 1}]; ok {
				spill := evaluator.evaluator.spills[cellCoordinate{index, sourceRow + 1, sourceColumn + 1}]
				if spill.owner.row < firstRow || spill.owner.row > lastRow ||
					spill.owner.column < firstColumn || spill.owner.column > lastColumn {
					snapshot.Cells[row][column].Value = value.Display()
				}
			} else if sourceRow < len(rows) && sourceColumn < len(rows[sourceRow]) {
				snapshot.Cells[row][column] = rows[sourceRow][sourceColumn]
			}
		}
	}
	return snapshot, nil
}

// PasteRange copies a clipboard snapshot to the destination as one atomic edit.
func PasteRange(book *Spreadsheet, sheet string, row, column int, clipboard CellClipboard) error {
	if clipboard.Row < 1 || clipboard.Column < 1 || row < 1 || column < 1 || len(clipboard.Cells) == 0 ||
		len(clipboard.Cells) > MaxRows-row+1 || len(clipboard.Cells[0]) == 0 ||
		len(clipboard.Cells[0]) > MaxColumns-column+1 {
		return fmt.Errorf("paste range is outside the workbook")
	}
	width := len(clipboard.Cells[0])
	if clipboard.Row > MaxRows-len(clipboard.Cells)+1 || clipboard.Column > MaxColumns-width+1 {
		return fmt.Errorf("copy range is outside the workbook")
	}
	index, err := SheetIndex(*book, sheet)
	if err != nil {
		return err
	}
	spills, err := NewEvaluator(book).SpillCells(index)
	if err != nil {
		return err
	}
	for r := range clipboard.Cells {
		for c := 0; c < width; c++ {
			if _, ok := spills[[2]int{row + r, column + c}]; ok {
				return fmt.Errorf("cannot paste into a spilled cell; edit its formula anchor")
			}
		}
	}
	updated := cloneSpreadsheet(*book)
	for r, cells := range clipboard.Cells {
		if len(cells) != width {
			return fmt.Errorf("clipboard range is not rectangular")
		}
		for c, cell := range cells {
			if cell.Formula != "" {
				var err error
				cell.Formula, err = TranslateFormula(cell.Formula, row-clipboard.Row, column-clipboard.Column)
				if err != nil {
					return fmt.Errorf("copy formula: %w", err)
				}
			}
			if err := validateCell(cell); err != nil {
				return err
			}
			if err := setCellInBook(&updated, index, SetCellOptions{Sheet: sheet, Row: row + r, Column: column + c, Cell: cell}); err != nil {
				return err
			}
		}
	}
	*book = updated
	return nil
}
