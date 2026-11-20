package spreadsheet

import (
	"context"
	"errors"
	"fmt"
)

// CellSpan occupies a rectangle whose top-left cell is its only value.
type CellSpan struct {
	FirstRow    int `json:"first_row"`
	FirstColumn int `json:"first_column"`
	LastRow     int `json:"last_row"`
	LastColumn  int `json:"last_column"`
}

func (s CellSpan) contains(row, column int) bool {
	return row >= s.FirstRow && row <= s.LastRow && column >= s.FirstColumn && column <= s.LastColumn
}

func (s CellSpan) overlaps(other CellSpan) bool {
	return s.FirstRow <= other.LastRow && other.FirstRow <= s.LastRow &&
		s.FirstColumn <= other.LastColumn && other.FirstColumn <= s.LastColumn
}

// SpanAt returns the span containing a physical coordinate, including its anchor.
func SpanAt(sheet Sheet, row, column int) (CellSpan, bool) {
	for _, span := range sheet.Spans {
		if span.contains(row, column) {
			return span, true
		}
	}
	return CellSpan{}, false
}

// ExpandSpanRange includes every span intersecting the range, including
// spans reached by an earlier expansion.
func ExpandSpanRange(sheet Sheet, selection CellSpan) CellSpan {
	for {
		before := selection
		for _, span := range sheet.Spans {
			if span.overlaps(selection) {
				selection.FirstRow = min(selection.FirstRow, span.FirstRow)
				selection.FirstColumn = min(selection.FirstColumn, span.FirstColumn)
				selection.LastRow = max(selection.LastRow, span.LastRow)
				selection.LastColumn = max(selection.LastColumn, span.LastColumn)
			}
		}
		if selection == before {
			return selection
		}
	}
}

func validateCellSpans(sheet Sheet) error {
	occupied := make(map[[2]int]bool)
	for _, span := range sheet.Spans {
		if span.FirstRow < 1 || span.FirstColumn < 1 || span.LastRow < span.FirstRow ||
			span.LastColumn < span.FirstColumn || span.LastRow > len(sheet.Rows) ||
			span.LastColumn > rowWidth(sheet.Rows) ||
			(span.FirstRow == span.LastRow && span.FirstColumn == span.LastColumn) {
			return errors.New("invalid cell span")
		}
		for row := span.FirstRow; row <= span.LastRow; row++ {
			for col := span.FirstColumn; col <= span.LastColumn; col++ {
				key := [2]int{row, col}
				if occupied[key] {
					return errors.New("cell spans cannot overlap")
				}
				occupied[key] = true
				if (row != span.FirstRow || col != span.FirstColumn) &&
					sheet.Rows[row-1][col-1] != (Cell{}) {
					return fmt.Errorf("covered cell %s must be empty", CellName(row, col))
				}
			}
		}
	}
	return nil
}

// SpanCellsInBook merges an empty rectangle around its top-left cell.
func SpanCellsInBook(book *Spreadsheet, name string, span CellSpan) error {
	if span.FirstRow < 1 || span.FirstColumn < 1 || span.LastRow < span.FirstRow ||
		span.LastColumn < span.FirstColumn || span.LastRow > MaxRows || span.LastColumn > MaxColumns ||
		(span.FirstRow == span.LastRow && span.FirstColumn == span.LastColumn) {
		return errors.New("span must contain at least two cells within the workbook")
	}
	index, err := SheetIndex(*book, name)
	if err != nil {
		return err
	}
	sheet := &book.Sheets[index]
	for _, existing := range sheet.Spans {
		if span.overlaps(existing) {
			return errors.New("cell spans cannot overlap")
		}
	}
	spills, err := NewEvaluator(book).SpillCells(index)
	if err != nil {
		return err
	}
	for row := span.FirstRow; row <= span.LastRow; row++ {
		for col := span.FirstColumn; col <= span.LastColumn; col++ {
			if _, ok := spills[[2]int{row, col}]; ok {
				return fmt.Errorf("cannot span spilled cell %s", CellName(row, col))
			}
			if row == span.FirstRow && col == span.FirstColumn {
				continue
			}
			if _, ok := ChartAt(book, sheet.Name, row, col); ok {
				return fmt.Errorf("cannot cover chart cell %s", CellName(row, col))
			}
			if row <= len(sheet.Rows) && col <= len(sheet.Rows[row-1]) &&
				sheet.Rows[row-1][col-1] != (Cell{}) {
				return fmt.Errorf("cannot cover nonempty cell %s", CellName(row, col))
			}
		}
	}
	if err := setCellInBook(book, index, SetCellOptions{
		Row: span.LastRow, Column: span.LastColumn,
	}); err != nil {
		return err
	}
	sheet.Spans = append(sheet.Spans, span)
	return nil
}

// SplitCellInBook removes a span without changing its anchor value.
func SplitCellInBook(book *Spreadsheet, name string, row, column int) error {
	index, err := SheetIndex(*book, name)
	if err != nil {
		return err
	}
	sheet := &book.Sheets[index]
	for i, span := range sheet.Spans {
		if span.contains(row, column) {
			if span.FirstRow != row || span.FirstColumn != column {
				return errors.New("cannot split a covered cell; use its span anchor")
			}
			sheet.Spans = append(sheet.Spans[:i], sheet.Spans[i+1:]...)
			return nil
		}
	}
	return errors.New("cell is not spanned")
}

func (s *Service) SpanCells(ctx context.Context, path, sheet string, span CellSpan) (Spreadsheet, error) {
	return s.modify(ctx, path, "Span cells", func(book *Spreadsheet) error {
		return SpanCellsInBook(book, sheet, span)
	})
}

func (s *Service) SplitCell(ctx context.Context, path, sheet string, row, column int) (Spreadsheet, error) {
	return s.modify(ctx, path, "Split cell", func(book *Spreadsheet) error {
		return SplitCellInBook(book, sheet, row, column)
	})
}

func insertCellSpans(sheet *Sheet, at int, row bool) {
	for i := range sheet.Spans {
		first, last := &sheet.Spans[i].FirstColumn, &sheet.Spans[i].LastColumn
		if row {
			first, last = &sheet.Spans[i].FirstRow, &sheet.Spans[i].LastRow
		}
		if at <= *first {
			*first++
			*last++
		} else if at <= *last {
			*last++
		}
	}
}

type movedSpanAnchor struct {
	row, column int
	cell        Cell
}

func deleteCellSpans(sheet *Sheet, at int, row bool) []movedSpanAnchor {
	var moved []movedSpanAnchor
	spans := sheet.Spans[:0]
	for _, span := range sheet.Spans {
		first, last := &span.FirstColumn, &span.LastColumn
		if row {
			first, last = &span.FirstRow, &span.LastRow
		}
		if at < *first {
			*first--
			*last--
		} else if at <= *last {
			if *first == *last {
				continue
			}
			if at == *first {
				moved = append(moved, movedSpanAnchor{
					row: span.FirstRow, column: span.FirstColumn,
					cell: sheet.Rows[span.FirstRow-1][span.FirstColumn-1],
				})
			}
			*last--
		}
		if span.FirstRow != span.LastRow || span.FirstColumn != span.LastColumn {
			spans = append(spans, span)
		}
	}
	sheet.Spans = spans
	return moved
}

func restoreSpanAnchors(book *Spreadsheet, sheet int, moved []movedSpanAnchor) {
	for _, anchor := range moved {
		_ = setCellInBook(book, sheet, SetCellOptions{
			Row: anchor.row, Column: anchor.column, Cell: anchor.cell,
		})
	}
}
