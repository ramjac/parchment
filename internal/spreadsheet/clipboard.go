package spreadsheet

import (
	"fmt"
	"strings"
)

// CellClipboard is an immutable snapshot of a rectangular source range.
type CellClipboard struct {
	Row, Column int
	Cells       [][]Cell
	Spans       []CellSpan
	Charts      []Chart
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
	selection := CellSpan{FirstRow: firstRow, FirstColumn: firstColumn, LastRow: lastRow, LastColumn: lastColumn}
	for _, span := range book.Sheets[index].Spans {
		if !span.overlaps(selection) {
			continue
		}
		if span.FirstRow < firstRow || span.FirstColumn < firstColumn ||
			span.LastRow > lastRow || span.LastColumn > lastColumn {
			return CellClipboard{}, fmt.Errorf("copy must include the entire span at %s", CellName(span.FirstRow, span.FirstColumn))
		}
		snapshot.Spans = append(snapshot.Spans, span)
	}
	for _, chart := range book.Charts {
		if strings.EqualFold(chart.PlaceSheet, book.Sheets[index].Name) && chart.PlaceRow >= firstRow &&
			chart.PlaceRow <= lastRow && chart.PlaceColumn >= firstColumn && chart.PlaceColumn <= lastColumn {
			snapshot.Charts = append(snapshot.Charts, chart)
		}
	}
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
	destination := CellSpan{FirstRow: row, FirstColumn: column, LastRow: row + len(clipboard.Cells) - 1, LastColumn: column + width - 1}
	for _, span := range book.Sheets[index].Spans {
		if span.overlaps(destination) {
			return fmt.Errorf("cannot paste over a spanned cell at %s", CellName(span.FirstRow, span.FirstColumn))
		}
	}
	for _, chart := range book.Charts {
		if strings.EqualFold(chart.PlaceSheet, book.Sheets[index].Name) && chart.PlaceRow >= destination.FirstRow &&
			chart.PlaceRow <= destination.LastRow && chart.PlaceColumn >= destination.FirstColumn &&
			chart.PlaceColumn <= destination.LastColumn {
			return fmt.Errorf("cannot paste over chart at %s", CellName(chart.PlaceRow, chart.PlaceColumn))
		}
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
	for _, chart := range clipboard.Charts {
		targetRow, targetColumn := row+chart.PlaceRow-clipboard.Row, column+chart.PlaceColumn-clipboard.Column
		rows := book.Sheets[index].Rows
		if targetRow >= 1 && targetRow <= len(rows) && targetColumn >= 1 &&
			targetColumn <= len(rows[targetRow-1]) && rows[targetRow-1][targetColumn-1] != (Cell{}) {
			return fmt.Errorf("cannot paste a chart over nonempty cell %s", CellName(targetRow, targetColumn))
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
	for _, span := range clipboard.Spans {
		if span.FirstRow < clipboard.Row || span.FirstColumn < clipboard.Column ||
			span.LastRow >= clipboard.Row+len(clipboard.Cells) || span.LastColumn >= clipboard.Column+width {
			return fmt.Errorf("clipboard span is outside the copied range")
		}
		dr, dc := row-clipboard.Row, column-clipboard.Column
		span.FirstRow, span.LastRow = span.FirstRow+dr, span.LastRow+dr
		span.FirstColumn, span.LastColumn = span.FirstColumn+dc, span.LastColumn+dc
		if err := SpanCellsInBook(&updated, sheet, span); err != nil {
			return err
		}
	}
	for _, chart := range clipboard.Charts {
		if chart.PlaceRow < clipboard.Row || chart.PlaceColumn < clipboard.Column ||
			chart.PlaceRow >= clipboard.Row+len(clipboard.Cells) || chart.PlaceColumn >= clipboard.Column+width {
			return fmt.Errorf("clipboard chart is outside the copied range")
		}
		chart.ID = ""
		chart.PlaceSheet = updated.Sheets[index].Name
		chart.PlaceRow += row - clipboard.Row
		chart.PlaceColumn += column - clipboard.Column
		if _, err := AddChartToBook(&updated, chart); err != nil {
			return err
		}
	}
	*book = updated
	return nil
}
