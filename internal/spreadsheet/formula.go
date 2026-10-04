package spreadsheet

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Evaluate calculates one cell. Empty referenced cells evaluate to zero;
// formulas support numeric literals, A1 references, parentheses, unary signs,
// and the +, -, *, and / operators.
func Evaluate(book *Spreadsheet, sheetIndex, row, column int) (float64, error) {
	if sheetIndex < 0 || sheetIndex >= len(book.Sheets) || row < 1 || row > MaxRows ||
		column < 1 || column > MaxColumns {
		return 0, errors.New("cell coordinates are outside the workbook")
	}
	e := evaluator{
		book: book, visiting: make(map[cellCoordinate]bool),
		cache: make(map[cellCoordinate]float64), depth: make(map[cellCoordinate]int),
	}
	return e.cell(sheetIndex, row, column)
}

const maxFormulaDepth = 512

type evaluator struct {
	book     *Spreadsheet
	visiting map[cellCoordinate]bool
	cache    map[cellCoordinate]float64
	depth    map[cellCoordinate]int
	depths   []int
}

type cellCoordinate struct {
	sheet, row, column int
}

func (e *evaluator) cell(sheetIndex, row, column int) (float64, error) {
	if row > len(e.book.Sheets[sheetIndex].Rows) ||
		column > len(e.book.Sheets[sheetIndex].Rows[row-1]) {
		return 0, nil
	}
	coordinate := cellCoordinate{sheet: sheetIndex, row: row, column: column}
	if value, ok := e.cache[coordinate]; ok {
		depth := e.depth[coordinate]
		if len(e.visiting)+depth > maxFormulaDepth {
			return 0, fmt.Errorf("formula dependency depth exceeds %d", maxFormulaDepth)
		}
		e.recordDependencyDepth(depth)
		return value, nil
	}
	if e.visiting[coordinate] {
		return 0, fmt.Errorf("circular reference at %s", CellName(row, column))
	}
	if len(e.visiting) >= maxFormulaDepth {
		return 0, fmt.Errorf("formula dependency depth exceeds %d", maxFormulaDepth)
	}
	cell := e.book.Sheets[sheetIndex].Rows[row-1][column-1]
	if cell.Formula == "" {
		if strings.TrimSpace(cell.Value) == "" {
			return 0, nil
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(cell.Value), 64)
		if err != nil {
			return 0, fmt.Errorf("%s is not numeric", CellName(row, column))
		}
		e.cache[coordinate] = value
		return value, nil
	}
	e.visiting[coordinate] = true
	e.depths = append(e.depths, 0)
	defer func() {
		delete(e.visiting, coordinate)
		e.depths = e.depths[:len(e.depths)-1]
	}()
	parser := formulaParser{input: strings.TrimSpace(strings.TrimPrefix(cell.Formula, "=")),
		evaluator: e, sheet: sheetIndex}
	value, err := parser.parseExpression()
	if err != nil {
		return 0, err
	}
	parser.skipSpace()
	if parser.position != len(parser.input) {
		return 0, fmt.Errorf("unexpected character %q", parser.input[parser.position])
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errors.New("formula result is not finite")
	}
	depth := e.depths[len(e.depths)-1] + 1
	e.depth[coordinate] = depth
	if parent := len(e.depths) - 2; parent >= 0 && depth > e.depths[parent] {
		e.depths[parent] = depth
	}
	e.cache[coordinate] = value
	return value, nil
}

func (e *evaluator) recordDependencyDepth(depth int) {
	if len(e.depths) == 0 {
		return
	}
	parent := len(e.depths) - 1
	if depth > e.depths[parent] {
		e.depths[parent] = depth
	}
}

type formulaParser struct {
	input     string
	position  int
	nesting   int
	evaluator *evaluator
	sheet     int
}

func (p *formulaParser) skipSpace() {
	for p.position < len(p.input) && unicode.IsSpace(rune(p.input[p.position])) {
		p.position++
	}
}

func (p *formulaParser) parseExpression() (float64, error) {
	value, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.position >= len(p.input) || (p.input[p.position] != '+' && p.input[p.position] != '-') {
			return value, nil
		}
		operator := p.input[p.position]
		p.position++
		right, err := p.parseTerm()
		if err != nil {
			return 0, err
		}
		if operator == '+' {
			value += right
		} else {
			value -= right
		}
	}
}

func (p *formulaParser) parseTerm() (float64, error) {
	value, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.position >= len(p.input) || (p.input[p.position] != '*' && p.input[p.position] != '/') {
			return value, nil
		}
		operator := p.input[p.position]
		p.position++
		right, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		if operator == '*' {
			value *= right
		} else {
			if right == 0 {
				return 0, errors.New("division by zero")
			}
			value /= right
		}
	}
}

func (p *formulaParser) parseUnary() (float64, error) {
	p.skipSpace()
	negative := false
	for {
		p.skipSpace()
		if p.position >= len(p.input) {
			break
		}
		switch p.input[p.position] {
		case '+':
			p.position++
		case '-':
			negative = !negative
			p.position++
		default:
			value, err := p.parsePrimary()
			if negative {
				value = -value
			}
			return value, err
		}
	}
	return 0, errors.New("expected a number, cell reference, or '('")
}

func (p *formulaParser) parsePrimary() (float64, error) {
	p.skipSpace()
	if p.position >= len(p.input) {
		return 0, errors.New("expected a number, cell reference, or '('")
	}
	if p.input[p.position] == '(' {
		if p.nesting >= maxFormulaDepth {
			return 0, fmt.Errorf("formula nesting exceeds %d", maxFormulaDepth)
		}
		p.nesting++
		defer func() { p.nesting-- }()
		p.position++
		value, err := p.parseExpression()
		if err != nil {
			return 0, err
		}
		p.skipSpace()
		if p.position >= len(p.input) || p.input[p.position] != ')' {
			return 0, errors.New("expected ')'")
		}
		p.position++
		return value, nil
	}
	if isLetter(p.input[p.position]) {
		start := p.position
		for p.position < len(p.input) && isLetter(p.input[p.position]) {
			p.position++
		}
		for p.position < len(p.input) && isDigit(p.input[p.position]) {
			p.position++
		}
		row, column, err := CellCoordinates(p.input[start:p.position])
		if err != nil {
			return 0, err
		}
		return p.evaluator.cell(p.sheet, row, column)
	}
	if isDigit(p.input[p.position]) || p.input[p.position] == '.' {
		start := p.position
		digits := false
		for p.position < len(p.input) && isDigit(p.input[p.position]) {
			digits = true
			p.position++
		}
		if p.position < len(p.input) && p.input[p.position] == '.' {
			p.position++
			for p.position < len(p.input) && isDigit(p.input[p.position]) {
				digits = true
				p.position++
			}
		}
		if p.position < len(p.input) && (p.input[p.position] == 'e' || p.input[p.position] == 'E') {
			p.position++
			if p.position < len(p.input) && (p.input[p.position] == '+' || p.input[p.position] == '-') {
				p.position++
			}
			exponentStart := p.position
			for p.position < len(p.input) && isDigit(p.input[p.position]) {
				p.position++
			}
			if p.position == exponentStart {
				return 0, fmt.Errorf("invalid number %q", p.input[start:p.position])
			}
		}
		if !digits {
			return 0, fmt.Errorf("invalid number %q", p.input[start:p.position])
		}
		number, err := strconv.ParseFloat(p.input[start:p.position], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number %q", p.input[start:p.position])
		}
		return number, nil
	}
	return 0, fmt.Errorf("unexpected character %q", p.input[p.position])
}

func isLetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func isDigit(value byte) bool { return value >= '0' && value <= '9' }

func shiftCellReferences(formula string, fromRow, fromColumn int) string {
	var result strings.Builder
	for i := 0; i < len(formula); {
		if isDecimalExponent(formula, i) {
			result.WriteByte(formula[i])
			i++
			continue
		}
		if !isLetter(formula[i]) || i > 0 && (isLetter(formula[i-1]) || isDigit(formula[i-1])) {
			result.WriteByte(formula[i])
			i++
			continue
		}
		endLetters := i
		for endLetters < len(formula) && isLetter(formula[endLetters]) {
			endLetters++
		}
		end := endLetters
		for end < len(formula) && isDigit(formula[end]) {
			end++
		}
		if endLetters == end || end < len(formula) && (isLetter(formula[end]) || isDigit(formula[end])) {
			result.WriteByte(formula[i])
			i++
			continue
		}
		row, column, err := CellCoordinates(formula[i:end])
		if err != nil {
			result.WriteString(formula[i:end])
			i = end
			continue
		}
		if fromRow > 0 && row >= fromRow {
			row++
		}
		if fromColumn > 0 && column >= fromColumn {
			column++
		}
		result.WriteString(CellName(row, column))
		i = end
	}
	return result.String()
}

func isDecimalExponent(formula string, index int) bool {
	if index < 2 || (formula[index] != 'e' && formula[index] != 'E') ||
		formula[index-1] != '.' || !isDigit(formula[index-2]) {
		return false
	}
	next := index + 1
	if next < len(formula) && (formula[next] == '+' || formula[next] == '-') {
		next++
	}
	return next < len(formula) && isDigit(formula[next])
}
