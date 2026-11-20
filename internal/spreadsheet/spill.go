package spreadsheet

import (
	"fmt"
	"reflect"
	"strings"
)

type spillValue struct {
	owner cellCoordinate
	value Value
}

func hasArrayExpression(node Node) bool {
	switch n := node.(type) {
	case *RangeNode:
		return true
	case *FunctionCallNode:
		switch strings.ToUpper(n.Name) {
		case "FILTER", "SORT", "UNIQUE", "SEQUENCE":
			return true
		}
		for _, arg := range n.Args {
			if hasArrayExpression(arg) {
				return true
			}
		}
	case *BinaryOpNode:
		return hasArrayExpression(n.Left) || hasArrayExpression(n.Right)
	case *UnaryOpNode:
		return hasArrayExpression(n.Expr)
	}
	return false
}

func (e *evaluator) prepareSpills() error {
	if e.spillsReady || e.preparing {
		return nil
	}
	e.preparing = true
	defer func() { e.preparing = false }()
	var anchors []cellCoordinate
	for sheet, page := range e.book.Sheets {
		for row, cells := range page.Rows {
			for col, cell := range cells {
				if cell.Formula == "" {
					continue
				}
				node, err := NewParser(strings.TrimPrefix(cell.Formula, "=")).Parse()
				if err == nil && hasArrayExpression(node) {
					anchors = append(anchors, cellCoordinate{sheet, row + 1, col + 1})
				}
			}
		}
	}
	if len(anchors) == 0 {
		e.spillsReady = true
		return nil
	}
	for pass := 0; pass <= maxFormulaDepth; pass++ {
		e.cache = make(map[cellCoordinate]Value)
		e.depth = make(map[cellCoordinate]int)
		e.arrays = make(map[cellCoordinate]valueGrid)
		for _, anchor := range anchors {
			if _, err := e.cell(anchor.sheet, anchor.row, anchor.column); err != nil {
				return err
			}
		}
		next := make(map[cellCoordinate]spillValue)
		errors := make(map[cellCoordinate]bool)
		for _, anchor := range anchors {
			grid, ok := e.arrays[anchor]
			if !ok {
				continue
			}
			if grid.rows < 1 || grid.cols < 1 || grid.rows > MaxRows-anchor.row+1 ||
				grid.cols > MaxColumns-anchor.column+1 || len(grid.values) != grid.rows*grid.cols {
				errors[anchor] = true
				continue
			}
			blocked := false
			for r := 0; r < grid.rows && !blocked; r++ {
				for c := 0; c < grid.cols; c++ {
					if r == 0 && c == 0 {
						continue
					}
					coordinate := cellCoordinate{anchor.sheet, anchor.row + r, anchor.column + c}
					if _, exists := next[coordinate]; exists || e.hasStoredCell(coordinate) {
						blocked = true
						break
					}
				}
			}
			if blocked {
				errors[anchor] = true
				continue
			}
			for r := 0; r < grid.rows; r++ {
				for c := 0; c < grid.cols; c++ {
					if r != 0 || c != 0 {
						coordinate := cellCoordinate{anchor.sheet, anchor.row + r, anchor.column + c}
						next[coordinate] = spillValue{owner: anchor, value: grid.values[r*grid.cols+c]}
					}
				}
			}
		}
		if reflect.DeepEqual(next, e.spills) && reflect.DeepEqual(errors, e.spillErrors) {
			e.spillsReady = true
			e.cache = make(map[cellCoordinate]Value)
			e.depth = make(map[cellCoordinate]int)
			return nil
		}
		e.spills, e.spillErrors = next, errors
	}
	return fmt.Errorf("spreadsheet spill dependencies did not converge")
}

func (e *evaluator) hasStoredCell(c cellCoordinate) bool {
	rows := e.book.Sheets[c.sheet].Rows
	return c.row <= len(rows) && c.column <= len(rows[c.row-1]) && rows[c.row-1][c.column-1] != (Cell{})
}

// SpillCells returns computed, non-anchor cells for one sheet.
func (e *Evaluator) SpillCells(sheet int) (map[[2]int]Value, error) {
	if sheet < 0 || sheet >= len(e.evaluator.book.Sheets) {
		return nil, fmt.Errorf("sheet is outside the workbook")
	}
	if err := e.evaluator.prepareSpills(); err != nil {
		return nil, err
	}
	result := make(map[[2]int]Value)
	for coordinate, spill := range e.evaluator.spills {
		if coordinate.sheet == sheet {
			result[[2]int{coordinate.row, coordinate.column}] = spill.value
		}
	}
	return result, nil
}
