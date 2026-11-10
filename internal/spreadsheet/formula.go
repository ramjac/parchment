package spreadsheet

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

type ErrorCode string

const (
	ErrRef      ErrorCode = "#REF!"
	ErrDiv0     ErrorCode = "#DIV/0!"
	ErrValue    ErrorCode = "#VALUE!"
	ErrName     ErrorCode = "#NAME?"
	ErrCircular ErrorCode = "#CIRCULAR!"
)

type ValueKind int

const (
	NumKind ValueKind = iota
	StrKind
	ErrKind
)

type Value struct {
	Kind ValueKind
	Num  float64
	Str  string
	Err  ErrorCode
}

func (v Value) Float64() (float64, error) {
	if v.Kind == ErrKind {
		return 0, errors.New(string(v.Err))
	}
	if v.Kind == StrKind {
		return 0, errors.New("value is text")
	}
	return v.Num, nil
}

// Evaluate calculates one cell. Empty referenced cells evaluate to zero;
// formulas support numeric literals, A1 references, parentheses, unary signs,
// and the +, -, *, and / operators.
func Evaluate(book *Spreadsheet, sheetIndex, row, column int) (Value, error) {
	if sheetIndex < 0 || sheetIndex >= len(book.Sheets) || row < 1 || row > MaxRows ||
		column < 1 || column > MaxColumns {
		return Value{}, errors.New("cell coordinates are outside the workbook")
	}
	e := evaluator{
		book: book, visiting: make(map[cellCoordinate]bool),
		cache: make(map[cellCoordinate]Value), depth: make(map[cellCoordinate]int),
	}
	return e.cell(sheetIndex, row, column)
}

const maxFormulaDepth = 512

type evaluator struct {
	book     *Spreadsheet
	visiting map[cellCoordinate]bool
	cache    map[cellCoordinate]Value
	depth    map[cellCoordinate]int
	depths   []int
}

type cellCoordinate struct {
	sheet, row, column int
}

func (e *evaluator) cell(sheetIndex, row, column int) (Value, error) {
	if row > len(e.book.Sheets[sheetIndex].Rows) ||
		column > len(e.book.Sheets[sheetIndex].Rows[row-1]) {
		return Value{}, nil
	}
	coordinate := cellCoordinate{sheet: sheetIndex, row: row, column: column}
	if value, ok := e.cache[coordinate]; ok {
		depth := e.depth[coordinate]
		if len(e.visiting)+depth > maxFormulaDepth {
			return Value{}, fmt.Errorf("formula dependency depth exceeds %d", maxFormulaDepth)
		}
		e.recordDependencyDepth(depth)
		return value, nil
	}
	if e.visiting[coordinate] {
		return Value{}, fmt.Errorf("circular reference at %s", CellName(row, column))
	}
	cell := e.book.Sheets[sheetIndex].Rows[row-1][column-1]
	if cell.Formula == "" {
		if strings.TrimSpace(cell.Value) == "" {
			return Value{}, nil
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(cell.Value), 64)
		if err != nil {
			return Value{}, fmt.Errorf("%s is not numeric", CellName(row, column))
		}
		val := Value{Kind: NumKind, Num: value}
		e.cache[coordinate] = val
		return val, nil
	}
	if len(e.visiting) >= maxFormulaDepth {
		return Value{}, fmt.Errorf("formula dependency depth exceeds %d", maxFormulaDepth)
	}
	e.visiting[coordinate] = true
	e.depths = append(e.depths, 0)
	defer func() {
		delete(e.visiting, coordinate)
		e.depths = e.depths[:len(e.depths)-1]
	}()
	parser := NewParser(strings.TrimPrefix(cell.Formula, "="))
	node, err := parser.Parse()
	if err != nil {
		return Value{}, err
	}
	val, err := node.Eval(e, sheetIndex)
	if err != nil {
		return Value{}, err
	}
	if val.Kind == NumKind && (math.IsNaN(val.Num) || math.IsInf(val.Num, 0)) {
		return Value{}, errors.New("formula result is not finite")
	}
	depth := e.depths[len(e.depths)-1] + 1
	e.depth[coordinate] = depth
	if parent := len(e.depths) - 2; parent >= 0 && depth > e.depths[parent] {
		e.depths[parent] = depth
	}
	e.cache[coordinate] = val
	return val, nil
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

type TokenKind int

const (
	TokEOF TokenKind = iota
	TokNumber
	TokString
	TokRef
	TokPlus
	TokMinus
	TokStar
	TokSlash
	TokLParen
	TokRParen
)

type Token struct {
	Kind   TokenKind
	Lexeme string
	Start  int
	End    int
}

type Lexer struct {
	input string
	pos   int
}

func (l *Lexer) skipSpace() {
	for l.pos < len(l.input) {
		r, size := utf8.DecodeRuneInString(l.input[l.pos:])
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\u00A0' {
			l.pos += size
		} else {
			break
		}
	}
}

func (l *Lexer) Next() (Token, error) {
	l.skipSpace()
	if l.pos >= len(l.input) {
		return Token{Kind: TokEOF, Start: l.pos, End: l.pos}, nil
	}

	start := l.pos
	c := l.input[l.pos]

	switch c {
	case '+':
		l.pos++
		return Token{Kind: TokPlus, Lexeme: "+", Start: start, End: l.pos}, nil
	case '-':
		l.pos++
		return Token{Kind: TokMinus, Lexeme: "-", Start: start, End: l.pos}, nil
	case '*':
		l.pos++
		return Token{Kind: TokStar, Lexeme: "*", Start: start, End: l.pos}, nil
	case '/':
		l.pos++
		return Token{Kind: TokSlash, Lexeme: "/", Start: start, End: l.pos}, nil
	case '(':
		l.pos++
		return Token{Kind: TokLParen, Lexeme: "(", Start: start, End: l.pos}, nil
	case ')':
		l.pos++
		return Token{Kind: TokRParen, Lexeme: ")", Start: start, End: l.pos}, nil
	}

	if c == '"' {
		l.pos++
		for l.pos < len(l.input) && l.input[l.pos] != '"' {
			l.pos++
		}
		if l.pos >= len(l.input) {
			return Token{}, errors.New("unterminated string")
		}
		l.pos++
		return Token{Kind: TokString, Lexeme: l.input[start:l.pos], Start: start, End: l.pos}, nil
	}

	if c == '\'' {
		l.pos++
		for l.pos < len(l.input) && l.input[l.pos] != '\'' {
			l.pos++
		}
		if l.pos >= len(l.input) {
			return Token{}, errors.New("unterminated sheet name")
		}
		l.pos++
		if l.pos < len(l.input) && l.input[l.pos] == '!' {
			l.pos++
			for l.pos < len(l.input) && isLetter(l.input[l.pos]) {
				l.pos++
			}
			for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
				l.pos++
			}
			return Token{Kind: TokRef, Lexeme: l.input[start:l.pos], Start: start, End: l.pos}, nil
		}
		return Token{}, errors.New("expected ! after sheet name")
	}

	if isDigit(c) || c == '.' {
		digits := false
		for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
			digits = true
			l.pos++
		}
		if l.pos < len(l.input) && l.input[l.pos] == '.' {
			l.pos++
			for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
				digits = true
				l.pos++
			}
		}
		if l.pos < len(l.input) && (l.input[l.pos] == 'e' || l.input[l.pos] == 'E') {
			l.pos++
			if l.pos < len(l.input) && (l.input[l.pos] == '+' || l.input[l.pos] == '-') {
				l.pos++
			}
			exponentStart := l.pos
			for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
				l.pos++
			}
			if l.pos == exponentStart {
				return Token{}, fmt.Errorf("invalid number %q", l.input[start:l.pos])
			}
		}
		if !digits {
			return Token{}, fmt.Errorf("invalid number %q", l.input[start:l.pos])
		}
		return Token{Kind: TokNumber, Lexeme: l.input[start:l.pos], Start: start, End: l.pos}, nil
	}

	if isLetter(c) {
		for l.pos < len(l.input) && isLetter(l.input[l.pos]) {
			l.pos++
		}
		for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
			l.pos++
		}
		if l.pos < len(l.input) && l.input[l.pos] == '!' {
			l.pos++
			for l.pos < len(l.input) && isLetter(l.input[l.pos]) {
				l.pos++
			}
			for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
				l.pos++
			}
		}
		return Token{Kind: TokRef, Lexeme: l.input[start:l.pos], Start: start, End: l.pos}, nil
	}

	if c == '#' {
		for l.pos < len(l.input) {
			r, size := utf8.DecodeRuneInString(l.input[l.pos:])
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\u00A0' {
				break
			}
			l.pos += size
			if l.input[l.pos-1] == '!' || l.input[l.pos-1] == '?' {
				break
			}
		}
		return Token{Kind: TokString, Lexeme: l.input[start:l.pos], Start: start, End: l.pos}, nil
	}

	_, size := utf8.DecodeRuneInString(l.input[l.pos:])
	l.pos += size
	return Token{}, fmt.Errorf("unexpected character %q", l.input[start:start+size])
}

type ErrorNode struct {
	Err ErrorCode
}

func (n *ErrorNode) Eval(e *evaluator, currentSheet int) (Value, error) {
	return Value{Kind: ErrKind, Err: n.Err}, nil
}

type Node interface {
	Eval(e *evaluator, currentSheet int) (Value, error)
}

type NumberNode struct {
	Value float64
}

func (n *NumberNode) Eval(e *evaluator, currentSheet int) (Value, error) {
	return Value{Kind: NumKind, Num: n.Value}, nil
}

type StringNode struct {
	Value string
}

func (n *StringNode) Eval(e *evaluator, currentSheet int) (Value, error) {
	return Value{Kind: StrKind, Str: n.Value}, nil
}

type RefNode struct {
	Sheet string
	Row   int
	Col   int
	Start int
	End   int
}

func (n *RefNode) Eval(e *evaluator, currentSheet int) (Value, error) {
	sheetIdx := currentSheet
	if n.Sheet != "" {
		found := -1
		for i, s := range e.book.Sheets {
			if strings.EqualFold(s.Name, n.Sheet) {
				found = i
				break
			}
		}
		if found == -1 {
			return Value{Kind: ErrKind, Err: ErrRef}, nil
		}
		sheetIdx = found
	}
	return e.cell(sheetIdx, n.Row, n.Col)
}

type UnaryOpNode struct {
	Op   TokenKind
	Expr Node
}

func (n *UnaryOpNode) Eval(e *evaluator, currentSheet int) (Value, error) {
	val, err := n.Expr.Eval(e, currentSheet)
	if err != nil {
		return Value{}, err
	}
	if val.Kind == ErrKind {
		return val, nil
	}
	if val.Kind == StrKind {
		return Value{}, errors.New("value is text")
	}
	if n.Op == TokMinus {
		return Value{Kind: NumKind, Num: -val.Num}, nil
	}
	return val, nil
}

type BinaryOpNode struct {
	Op    TokenKind
	Left  Node
	Right Node
}

func (n *BinaryOpNode) Eval(e *evaluator, currentSheet int) (Value, error) {
	left, err := n.Left.Eval(e, currentSheet)
	if err != nil {
		return Value{}, err
	}
	if left.Kind == ErrKind {
		return left, nil
	}
	if left.Kind == StrKind {
		return Value{}, errors.New("value is text")
	}

	right, err := n.Right.Eval(e, currentSheet)
	if err != nil {
		return Value{}, err
	}
	if right.Kind == ErrKind {
		return right, nil
	}
	if right.Kind == StrKind {
		return Value{}, errors.New("value is text")
	}

	switch n.Op {
	case TokPlus:
		return Value{Kind: NumKind, Num: left.Num + right.Num}, nil
	case TokMinus:
		return Value{Kind: NumKind, Num: left.Num - right.Num}, nil
	case TokStar:
		return Value{Kind: NumKind, Num: left.Num * right.Num}, nil
	case TokSlash:
		if right.Num == 0 {
			return Value{}, errors.New("division by zero")
		}
		return Value{Kind: NumKind, Num: left.Num / right.Num}, nil
	}
	return Value{}, errors.New("unknown operator")
}

type Parser struct {
	lexer   *Lexer
	tok     Token
	err     error
	nesting int
}

func NewParser(input string) *Parser {
	p := &Parser{lexer: &Lexer{input: input}}
	p.next()
	return p
}

func (p *Parser) next() {
	if p.err != nil {
		return
	}
	p.tok, p.err = p.lexer.Next()
}

func (p *Parser) Parse() (Node, error) {
	node, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if p.tok.Kind != TokEOF {
		return nil, fmt.Errorf("unexpected character %q", p.tok.Lexeme)
	}
	return node, nil
}

func (p *Parser) parseExpression() (Node, error) {
	node, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for p.tok.Kind == TokPlus || p.tok.Kind == TokMinus {
		op := p.tok.Kind
		p.next()
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		node = &BinaryOpNode{Op: op, Left: node, Right: right}
	}
	return node, nil
}

func (p *Parser) parseTerm() (Node, error) {
	node, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.tok.Kind == TokStar || p.tok.Kind == TokSlash {
		op := p.tok.Kind
		p.next()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		node = &BinaryOpNode{Op: op, Left: node, Right: right}
	}
	return node, nil
}

func (p *Parser) parseUnary() (Node, error) {
	negative := false
	for p.tok.Kind == TokPlus || p.tok.Kind == TokMinus {
		if p.tok.Kind == TokMinus {
			negative = !negative
		}
		p.next()
	}
	node, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	if negative {
		return &UnaryOpNode{Op: TokMinus, Expr: node}, nil
	}
	return node, nil
}

func (p *Parser) parsePrimary() (Node, error) {
	if p.err != nil {
		return nil, p.err
	}

	switch p.tok.Kind {
	case TokNumber:
		val, err := strconv.ParseFloat(p.tok.Lexeme, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", p.tok.Lexeme)
		}
		p.next()
		return &NumberNode{Value: val}, nil
	case TokString:
		val := p.tok.Lexeme
		if val == "#REF!" {
			p.next()
			return &ErrorNode{Err: ErrRef}, nil
		}
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		p.next()
		return &StringNode{Value: val}, nil
	case TokRef:
		ref := p.tok.Lexeme
		start := p.tok.Start
		end := p.tok.End
		p.next()

		sheetName := ""
		cellRef := ref
		if idx := strings.Index(ref, "!"); idx != -1 {
			sheetName = ref[:idx]
			cellRef = ref[idx+1:]
			if len(sheetName) >= 2 && sheetName[0] == '\'' && sheetName[len(sheetName)-1] == '\'' {
				sheetName = sheetName[1 : len(sheetName)-1]
			}
		}

		row, col, err := CellCoordinates(cellRef)
		if err != nil {
			return nil, err
		}

		return &RefNode{Sheet: sheetName, Row: row, Col: col, Start: start, End: end}, nil
	case TokLParen:
		if p.nesting >= maxFormulaDepth {
			return nil, fmt.Errorf("formula nesting exceeds %d", maxFormulaDepth)
		}
		p.nesting++
		p.next()
		node, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if p.tok.Kind != TokRParen {
			return nil, errors.New("expected ')'")
		}
		p.nesting--
		p.next()
		return node, nil
	}
	return nil, errors.New("expected a number, cell reference, or '('")
}

func extractRefs(node Node) []*RefNode {
	var refs []*RefNode
	switch n := node.(type) {
	case *RefNode:
		refs = append(refs, n)
	case *UnaryOpNode:
		refs = append(refs, extractRefs(n.Expr)...)
	case *BinaryOpNode:
		refs = append(refs, extractRefs(n.Left)...)
		refs = append(refs, extractRefs(n.Right)...)
	}
	return refs
}

func shiftCellReferences(formula string, fromRow, fromColumn int, delete bool) string {
	if !strings.HasPrefix(formula, "=") {
		return formula
	}
	parser := NewParser(strings.TrimPrefix(formula, "="))
	node, err := parser.Parse()
	if err != nil {
		return fallbackShift(formula, fromRow, fromColumn, delete)
	}

	refs := extractRefs(node)
	if len(refs) == 0 {
		return formula
	}

	bytes := []byte(formula)
	for i := len(refs) - 1; i >= 0; i-- {
		ref := refs[i]
		if ref.Sheet != "" {
			continue
		}

		r, c := ref.Row, ref.Col

		if delete && (fromRow > 0 && r == fromRow || fromColumn > 0 && c == fromColumn) {
			start := ref.Start + 1
			end := ref.End + 1
			bytes = append(bytes[:start], append([]byte("#REF!"), bytes[end:]...)...)
			continue
		}

		shifted := false
		if fromRow > 0 && r > fromRow {
			if delete {
				r--
			} else {
				r++
			}
			shifted = true
		} else if fromRow > 0 && r >= fromRow && !delete {
			r++
			shifted = true
		}

		if fromColumn > 0 && c > fromColumn {
			if delete {
				c--
			} else {
				c++
			}
			shifted = true
		} else if fromColumn > 0 && c >= fromColumn && !delete {
			c++
			shifted = true
		}

		if shifted {
			start := ref.Start + 1
			end := ref.End + 1
			newName := CellName(r, c)
			bytes = append(bytes[:start], append([]byte(newName), bytes[end:]...)...)
		}
	}
	return string(bytes)
}

func fallbackShift(formula string, fromRow, fromColumn int, delete bool) string {
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
		if delete {
			if (fromRow > 0 && row == fromRow) || (fromColumn > 0 && column == fromColumn) {
				result.WriteString("#REF!")
				i = end
				continue
			}
			if fromRow > 0 && row > fromRow {
				row--
			}
			if fromColumn > 0 && column > fromColumn {
				column--
			}
		} else {
			if fromRow > 0 && row >= fromRow {
				row++
			}
			if fromColumn > 0 && column >= fromColumn {
				column++
			}
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

func isLetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func isDigit(value byte) bool { return value >= '0' && value <= '9' }
