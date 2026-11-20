package spreadsheet

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type ErrorCode string

const (
	ErrRef      ErrorCode = "#REF!"
	ErrDiv0     ErrorCode = "#DIV/0!"
	ErrValue    ErrorCode = "#VALUE!"
	ErrName     ErrorCode = "#NAME?"
	ErrCircular ErrorCode = "#CIRCULAR!"
	ErrNA       ErrorCode = "#N/A"
	ErrSpill    ErrorCode = "#SPILL!"
	ErrCalc     ErrorCode = "#CALC!"
)

type ValueKind int

const (
	EmptyKind ValueKind = iota
	NumKind
	StrKind
	BoolKind
	ErrKind
	ArrayKind
)

type Value struct {
	Kind  ValueKind
	Num   float64
	Str   string
	Bool  bool
	Err   ErrorCode
	array *valueGrid
}

func (v Value) Float64() (float64, error) {
	if v.Kind == ErrKind {
		return 0, errors.New(string(v.Err))
	}
	if v.Kind == StrKind {
		return 0, errors.New("value is not numeric")
	}
	if v.Kind == BoolKind && v.Bool {
		return 1, nil
	}
	return v.Num, nil
}

// Evaluate calculates one cell. Empty referenced cells evaluate to zero;
// formulas support numeric literals, A1 references, parentheses, unary signs,
// +, -, *, /, ranges, string literals, and aggregate functions.
func Evaluate(book *Spreadsheet, sheetIndex, row, column int) (Value, error) {
	return NewEvaluator(book).Evaluate(sheetIndex, row, column)
}

// Evaluator evaluates cells in one workbook while reusing its dependency cache.
type Evaluator struct {
	evaluator evaluator
}

// NewEvaluator creates an evaluator for book.
func NewEvaluator(book *Spreadsheet) *Evaluator {
	return NewEvaluatorAt(book, time.Now())
}

// NewEvaluatorAt evaluates volatile time functions against a fixed UTC instant.
func NewEvaluatorAt(book *Spreadsheet, now time.Time) *Evaluator {
	return &Evaluator{evaluator: evaluator{
		book: book, visiting: make(map[cellCoordinate]bool),
		cache: make(map[cellCoordinate]Value), depth: make(map[cellCoordinate]int),
		now: now.UTC(),
	}}
}

// Evaluate calculates one cell and caches its result for subsequent lookups.
func (e *Evaluator) Evaluate(sheetIndex, row, column int) (Value, error) {
	book := e.evaluator.book
	if sheetIndex < 0 || sheetIndex >= len(book.Sheets) || row < 1 || row > MaxRows ||
		column < 1 || column > MaxColumns {
		return Value{}, errors.New("cell coordinates are outside the workbook")
	}
	if err := e.evaluator.prepareSpills(); err != nil {
		return Value{}, err
	}
	return e.evaluator.cell(sheetIndex, row, column)
}

const maxFormulaDepth = 512

type evaluator struct {
	book        *Spreadsheet
	now         time.Time
	visiting    map[cellCoordinate]bool
	cache       map[cellCoordinate]Value
	depth       map[cellCoordinate]int
	depths      []int
	spills      map[cellCoordinate]spillValue
	arrays      map[cellCoordinate]valueGrid
	spillErrors map[cellCoordinate]bool
	spillsReady bool
	preparing   bool
}

type cellCoordinate struct {
	sheet, row, column int
}

func (e *evaluator) cell(sheetIndex, row, column int) (Value, error) {
	coordinate := cellCoordinate{sheet: sheetIndex, row: row, column: column}
	if spill, ok := e.spills[coordinate]; ok {
		owner, err := e.cell(spill.owner.sheet, spill.owner.row, spill.owner.column)
		if err != nil || owner.Kind == ErrKind {
			return owner, err
		}
		return spill.value, nil
	}
	if row > len(e.book.Sheets[sheetIndex].Rows) ||
		column > len(e.book.Sheets[sheetIndex].Rows[row-1]) {
		return Value{Kind: EmptyKind}, nil
	}
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
		trimmed := strings.TrimSpace(cell.Value)
		if cell.Value == "" {
			return Value{Kind: EmptyKind}, nil
		}
		value, err := strconv.ParseFloat(trimmed, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			val := Value{Kind: StrKind, Str: cell.Value}
			e.cache[coordinate] = val
			return val, nil
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
	if val.Kind == ArrayKind {
		if val.array == nil || val.array.rows < 1 || val.array.cols < 1 || len(val.array.values) == 0 {
			val = Value{Kind: ErrKind, Err: ErrCalc}
		} else {
			if e.arrays != nil {
				e.arrays[coordinate] = *val.array
			}
			if e.spillErrors[coordinate] {
				val = Value{Kind: ErrKind, Err: ErrSpill}
			} else {
				val = val.array.values[0]
			}
		}
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
	TokColon
	TokComma
	TokEqual
	TokNotEqual
	TokLess
	TokLessEqual
	TokGreater
	TokGreaterEqual
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
	case ':':
		l.pos++
		return Token{Kind: TokColon, Lexeme: ":", Start: start, End: l.pos}, nil
	case ',':
		l.pos++
		return Token{Kind: TokComma, Lexeme: ",", Start: start, End: l.pos}, nil
	case '=', '<', '>':
		l.pos++
		kind := TokEqual
		if c == '<' {
			kind = TokLess
			if l.pos < len(l.input) && l.input[l.pos] == '>' {
				l.pos++
				kind = TokNotEqual
			} else if l.pos < len(l.input) && l.input[l.pos] == '=' {
				l.pos++
				kind = TokLessEqual
			}
		} else if c == '>' {
			kind = TokGreater
			if l.pos < len(l.input) && l.input[l.pos] == '=' {
				l.pos++
				kind = TokGreaterEqual
			}
		}
		return Token{Kind: kind, Lexeme: l.input[start:l.pos], Start: start, End: l.pos}, nil
	}

	if c == '"' {
		l.pos++
		for l.pos < len(l.input) {
			if l.input[l.pos] == '"' {
				if l.pos+1 < len(l.input) && l.input[l.pos+1] == '"' {
					l.pos += 2
					continue
				}
				l.pos++
				return Token{Kind: TokString, Lexeme: l.input[start:l.pos], Start: start, End: l.pos}, nil
			}
			l.pos++
		}
		return Token{}, errors.New("unterminated string")
	}

	if c == '\'' {
		l.pos++
		for l.pos < len(l.input) {
			if l.input[l.pos] == '\'' {
				if l.pos+1 < len(l.input) && l.input[l.pos+1] == '\'' {
					l.pos += 2
					continue
				}
				break
			}
			l.pos++
		}
		if l.pos >= len(l.input) {
			return Token{}, errors.New("unterminated sheet name")
		}
		l.pos++
		if l.pos < len(l.input) && l.input[l.pos] == '!' {
			l.pos++
			l.scanCellReference()
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

	if isLetter(c) || c == '$' {
		if c == '$' {
			l.scanCellReference()
			return Token{Kind: TokRef, Lexeme: l.input[start:l.pos], Start: start, End: l.pos}, nil
		}
		for l.pos < len(l.input) && isLetter(l.input[l.pos]) {
			l.pos++
		}
		if l.pos < len(l.input) && l.input[l.pos] == '$' {
			l.pos++
		}
		for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
			l.pos++
		}
		if l.pos < len(l.input) && l.input[l.pos] == '!' {
			l.pos++
			l.scanCellReference()
		}
		return Token{Kind: TokRef, Lexeme: l.input[start:l.pos], Start: start, End: l.pos}, nil
	}

	if c == '#' {
		if strings.HasPrefix(l.input[l.pos:], string(ErrNA)) {
			l.pos += len(ErrNA)
			return Token{Kind: TokString, Lexeme: l.input[start:l.pos], Start: start, End: l.pos}, nil
		}
		for l.pos < len(l.input) {
			r, size := utf8.DecodeRuneInString(l.input[l.pos:])
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\u00A0' || r == ',' || r == ')' || r == ':' {
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

func (l *Lexer) scanCellReference() {
	if l.pos < len(l.input) && l.input[l.pos] == '$' {
		l.pos++
	}
	for l.pos < len(l.input) && isLetter(l.input[l.pos]) {
		l.pos++
	}
	if l.pos < len(l.input) && l.input[l.pos] == '$' {
		l.pos++
	}
	for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
		l.pos++
	}
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

type BoolNode struct{ Value bool }

func (n *BoolNode) Eval(_ *evaluator, _ int) (Value, error) {
	return Value{Kind: BoolKind, Bool: n.Value}, nil
}

func (n *StringNode) Eval(e *evaluator, currentSheet int) (Value, error) {
	return Value{Kind: StrKind, Str: n.Value}, nil
}

type ErrorNode struct {
	Err ErrorCode
}

func (n *ErrorNode) Eval(e *evaluator, currentSheet int) (Value, error) {
	return Value{Kind: ErrKind, Err: n.Err}, nil
}

type RefNode struct {
	Sheet       string
	Row         int
	Col         int
	AbsoluteRow bool
	AbsoluteCol bool
	Start       int
	End         int
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

type RangeNode struct {
	Sheet           string
	FromRow         int
	FromCol         int
	ToRow           int
	ToCol           int
	FromAbsoluteRow bool
	FromAbsoluteCol bool
	ToAbsoluteRow   bool
	ToAbsoluteCol   bool
	Start           int
	End             int
	FirstEnd        int
	SecondStart     int
}

func (n *RangeNode) Eval(e *evaluator, currentSheet int) (Value, error) {
	grid, err := evaluateGrid(n, e, currentSheet)
	if err != nil {
		return Value{}, err
	}
	if grid.err != "" {
		return Value{Kind: ErrKind, Err: grid.err}, nil
	}
	return Value{Kind: ArrayKind, array: &grid}, nil
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
	if val.Kind == ArrayKind {
		result := valueGrid{rows: val.array.rows, cols: val.array.cols, values: make([]Value, len(val.array.values))}
		for i, value := range val.array.values {
			num, valueErr := coerceToNumber(value)
			if valueErr != nil {
				result.values[i] = *valueErr
			} else if n.Op == TokMinus {
				result.values[i] = Value{Kind: NumKind, Num: -num}
			} else {
				result.values[i] = Value{Kind: NumKind, Num: num}
			}
		}
		return Value{Kind: ArrayKind, array: &result}, nil
	}
	num, errVal := coerceToNumber(val)
	if errVal != nil {
		return *errVal, nil
	}
	if n.Op == TokMinus {
		return Value{Kind: NumKind, Num: -num}, nil
	}
	return Value{Kind: NumKind, Num: num}, nil
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

	right, err := n.Right.Eval(e, currentSheet)
	if err != nil {
		return Value{}, err
	}
	if right.Kind == ErrKind {
		return right, nil
	}

	if left.Kind == ArrayKind || right.Kind == ArrayKind {
		rows, cols := 1, 1
		if left.Kind == ArrayKind {
			rows, cols = left.array.rows, left.array.cols
		}
		if right.Kind == ArrayKind {
			if left.Kind == ArrayKind && (rows != right.array.rows || cols != right.array.cols) {
				return Value{Kind: ErrKind, Err: ErrValue}, nil
			}
			rows, cols = right.array.rows, right.array.cols
		}
		result := valueGrid{rows: rows, cols: cols, values: make([]Value, rows*cols)}
		for i := range result.values {
			l, r := left, right
			if left.Kind == ArrayKind {
				l = left.array.values[i]
			}
			if right.Kind == ArrayKind {
				r = right.array.values[i]
			}
			result.values[i], err = applyBinary(n.Op, l, r)
			if err != nil {
				return Value{}, err
			}
		}
		return Value{Kind: ArrayKind, array: &result}, nil
	}
	return applyBinary(n.Op, left, right)
}

func applyBinary(op TokenKind, left, right Value) (Value, error) {
	if left.Kind == ErrKind {
		return left, nil
	}
	if right.Kind == ErrKind {
		return right, nil
	}
	if op >= TokEqual && op <= TokGreaterEqual {
		cmp := compareValues(left, right)
		var result bool
		switch op {
		case TokEqual:
			result = cmp == 0
		case TokNotEqual:
			result = cmp != 0
		case TokLess:
			result = cmp < 0
		case TokLessEqual:
			result = cmp <= 0
		case TokGreater:
			result = cmp > 0
		case TokGreaterEqual:
			result = cmp >= 0
		}
		return Value{Kind: BoolKind, Bool: result}, nil
	}
	lNum, lErrVal := coerceToNumber(left)
	if lErrVal != nil {
		return *lErrVal, nil
	}
	rNum, rErrVal := coerceToNumber(right)
	if rErrVal != nil {
		return *rErrVal, nil
	}

	switch op {
	case TokPlus:
		return Value{Kind: NumKind, Num: lNum + rNum}, nil
	case TokMinus:
		return Value{Kind: NumKind, Num: lNum - rNum}, nil
	case TokStar:
		return Value{Kind: NumKind, Num: lNum * rNum}, nil
	case TokSlash:
		if rNum == 0 {
			return Value{Kind: ErrKind, Err: ErrDiv0}, nil
		}
		return Value{Kind: NumKind, Num: lNum / rNum}, nil
	}
	return Value{}, errors.New("unknown operator")
}

func coerceToNumber(v Value) (float64, *Value) {
	switch v.Kind {
	case ErrKind:
		return 0, &v
	case EmptyKind:
		return 0, nil
	case NumKind:
		return v.Num, nil
	case BoolKind:
		if v.Bool {
			return 1, nil
		}
		return 0, nil
	case StrKind:
		s := strings.TrimSpace(v.Str)
		if s == "" {
			return 0, nil
		}
		num, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(num) || math.IsInf(num, 0) {
			errVal := Value{Kind: ErrKind, Err: ErrValue}
			return 0, &errVal
		}
		return num, nil
	}
	return 0, nil
}

type FunctionCallNode struct {
	Name string
	Args []Node
}

func (n *FunctionCallNode) Eval(e *evaluator, currentSheet int) (Value, error) {
	name := strings.ToUpper(n.Name)
	if value, handled, err := evalAdditionalFunction(name, n.Args, e, currentSheet); handled {
		return value, err
	}
	switch name {
	case "SUM", "AVERAGE", "COUNT", "MIN", "MAX":
	default:
		return Value{Kind: ErrKind, Err: ErrName}, nil
	}

	var nums []float64

	for _, arg := range n.Args {
		if rng, ok := arg.(*RangeNode); ok {
			sheetIdx := currentSheet
			if rng.Sheet != "" {
				found := -1
				for i, s := range e.book.Sheets {
					if strings.EqualFold(s.Name, rng.Sheet) {
						found = i
						break
					}
				}
				if found == -1 {
					return Value{Kind: ErrKind, Err: ErrRef}, nil
				}
				sheetIdx = found
			}

			r1, r2 := min(rng.FromRow, rng.ToRow), max(rng.FromRow, rng.ToRow)
			c1, c2 := min(rng.FromCol, rng.ToCol), max(rng.FromCol, rng.ToCol)

			for r := r1; r <= r2; r++ {
				for c := c1; c <= c2; c++ {
					val, err := e.cell(sheetIdx, r, c)
					if err != nil {
						return Value{}, err
					}
					if val.Kind == ErrKind {
						return val, nil
					}
					if val.Kind == NumKind {
						nums = append(nums, val.Num)
					}
				}
			}
		} else {
			val, err := arg.Eval(e, currentSheet)
			if err != nil {
				return Value{}, err
			}
			if val.Kind == ErrKind {
				return val, nil
			}
			if val.Kind == ArrayKind {
				for _, item := range val.array.values {
					if item.Kind == ErrKind {
						return item, nil
					}
					if item.Kind == NumKind {
						nums = append(nums, item.Num)
					}
				}
			} else if val.Kind == NumKind {
				nums = append(nums, val.Num)
			} else if val.Kind == StrKind {
				trimmed := strings.TrimSpace(val.Str)
				num, err := strconv.ParseFloat(trimmed, 64)
				if err != nil || math.IsNaN(num) || math.IsInf(num, 0) {
					return Value{Kind: ErrKind, Err: ErrValue}, nil
				}
				nums = append(nums, num)
			}
		}
	}

	switch name {
	case "SUM":
		sum := 0.0
		for _, v := range nums {
			sum += v
		}
		return Value{Kind: NumKind, Num: sum}, nil
	case "AVERAGE":
		if len(nums) == 0 {
			return Value{Kind: ErrKind, Err: ErrDiv0}, nil
		}
		sum := 0.0
		for _, v := range nums {
			sum += v
		}
		return Value{Kind: NumKind, Num: sum / float64(len(nums))}, nil
	case "COUNT":
		return Value{Kind: NumKind, Num: float64(len(nums))}, nil
	case "MIN":
		if len(nums) == 0 {
			return Value{Kind: ErrKind, Err: ErrValue}, nil
		}
		minVal := nums[0]
		for _, v := range nums[1:] {
			if v < minVal {
				minVal = v
			}
		}
		return Value{Kind: NumKind, Num: minVal}, nil
	case "MAX":
		if len(nums) == 0 {
			return Value{Kind: ErrKind, Err: ErrValue}, nil
		}
		maxVal := nums[0]
		for _, v := range nums[1:] {
			if v > maxVal {
				maxVal = v
			}
		}
		return Value{Kind: NumKind, Num: maxVal}, nil
	}

	return Value{Kind: ErrKind, Err: ErrName}, nil
}

type Parser struct {
	lexer   *Lexer
	tok     Token
	peekTok Token
	err     error
	nesting int
}

func NewParser(input string) *Parser {
	p := &Parser{lexer: &Lexer{input: input}}
	p.next()
	p.next()
	return p
}

func (p *Parser) next() {
	p.tok = p.peekTok
	if p.err != nil {
		p.peekTok = Token{Kind: TokEOF}
		return
	}
	p.peekTok, p.err = p.lexer.Next()
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
	for p.tok.Kind >= TokEqual && p.tok.Kind <= TokGreaterEqual {
		op := p.tok.Kind
		p.next()
		right, err := p.parseExpressionArithmetic()
		if err != nil {
			return nil, err
		}
		node = &BinaryOpNode{Op: op, Left: node, Right: right}
	}
	return node, nil
}

func (p *Parser) parseExpressionArithmetic() (Node, error) {
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
		switch ErrorCode(val) {
		case ErrRef, ErrDiv0, ErrValue, ErrName, ErrCircular, ErrNA, ErrSpill, ErrCalc:
			p.next()
			return &ErrorNode{Err: ErrorCode(val)}, nil
		}
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			content := val[1 : len(val)-1]
			val = strings.ReplaceAll(content, "\"\"", "\"")
		}
		p.next()
		return &StringNode{Value: val}, nil
	case TokRef:
		if strings.EqualFold(p.tok.Lexeme, "TRUE") || strings.EqualFold(p.tok.Lexeme, "FALSE") {
			value := strings.EqualFold(p.tok.Lexeme, "TRUE")
			p.next()
			return &BoolNode{Value: value}, nil
		}
		if p.peekTok.Kind == TokLParen && !strings.Contains(p.tok.Lexeme, "!") {
			return p.parseFunctionCall()
		}
		if p.peekTok.Kind == TokColon {
			return p.parseRange()
		}
		return p.parseCellRef()
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

func (p *Parser) parseCellRef() (Node, error) {
	ref := p.tok.Lexeme
	start := p.tok.Start
	end := p.tok.End
	p.next()

	sheetName := ""
	cellRef := ref
	if idx := strings.LastIndex(ref, "!"); idx != -1 {
		sheetName = ref[:idx]
		cellRef = ref[idx+1:]
		if len(sheetName) >= 2 && sheetName[0] == '\'' && sheetName[len(sheetName)-1] == '\'' {
			sheetName = strings.ReplaceAll(sheetName[1:len(sheetName)-1], "''", "'")
		}
	}

	row, col, absRow, absCol, err := parseFormulaCoordinates(cellRef)
	if err != nil {
		return nil, err
	}

	return &RefNode{Sheet: sheetName, Row: row, Col: col, AbsoluteRow: absRow, AbsoluteCol: absCol, Start: start, End: end}, nil
}

func (p *Parser) parseRange() (Node, error) {
	firstToken := p.tok
	p.next() // consume first TokRef
	p.next() // consume TokColon

	if p.tok.Kind != TokRef {
		return nil, errors.New("expected cell reference after ':'")
	}
	secondToken := p.tok
	p.next() // consume second TokRef

	sheetName := ""
	firstRef := firstToken.Lexeme
	if idx := strings.LastIndex(firstRef, "!"); idx != -1 {
		sheetName = firstRef[:idx]
		firstRef = firstRef[idx+1:]
		if len(sheetName) >= 2 && sheetName[0] == '\'' && sheetName[len(sheetName)-1] == '\'' {
			sheetName = strings.ReplaceAll(sheetName[1:len(sheetName)-1], "''", "'")
		}
	}

	fromRow, fromCol, fromAbsRow, fromAbsCol, err := parseFormulaCoordinates(firstRef)
	if err != nil {
		return nil, err
	}

	secondRef := secondToken.Lexeme
	if idx := strings.LastIndex(secondRef, "!"); idx != -1 {
		sName := secondRef[:idx]
		secondRef = secondRef[idx+1:]
		if len(sName) >= 2 && sName[0] == '\'' && sName[len(sName)-1] == '\'' {
			sName = strings.ReplaceAll(sName[1:len(sName)-1], "''", "'")
		}
		if sheetName == "" {
			sheetName = sName
		}
	}

	toRow, toCol, toAbsRow, toAbsCol, err := parseFormulaCoordinates(secondRef)
	if err != nil {
		return nil, err
	}

	return &RangeNode{
		Sheet: sheetName, FromRow: fromRow, FromCol: fromCol, ToRow: toRow, ToCol: toCol,
		FromAbsoluteRow: fromAbsRow, FromAbsoluteCol: fromAbsCol,
		ToAbsoluteRow: toAbsRow, ToAbsoluteCol: toAbsCol,
		Start: firstToken.Start, End: secondToken.End, FirstEnd: firstToken.End, SecondStart: secondToken.Start,
	}, nil
}

func parseFormulaCoordinates(ref string) (row, col int, absoluteRow, absoluteCol bool, err error) {
	i := 0
	if strings.HasPrefix(ref, "$") {
		absoluteCol = true
		i++
	}
	start := i
	for i < len(ref) && isLetter(ref[i]) {
		i++
	}
	if i == start {
		return 0, 0, false, false, fmt.Errorf("invalid cell reference %q", ref)
	}
	letters := ref[start:i]
	if i < len(ref) && ref[i] == '$' {
		absoluteRow = true
		i++
	}
	row, col, err = CellCoordinates(letters + ref[i:])
	return
}

func formatFormulaReference(original string, row, col int, absoluteRow, absoluteCol bool) string {
	prefix := ""
	if separator := strings.LastIndexByte(original, '!'); separator >= 0 {
		prefix = original[:separator+1]
	}
	name := CellName(row, col)
	i := 0
	for i < len(name) && isLetter(name[i]) {
		i++
	}
	if absoluteCol {
		prefix += "$"
	}
	prefix += name[:i]
	if absoluteRow {
		prefix += "$"
	}
	return prefix + name[i:]
}

func (p *Parser) parseFunctionCall() (Node, error) {
	name := p.tok.Lexeme
	p.next() // consume funcName
	p.next() // consume '('

	var args []Node
	if p.tok.Kind != TokRParen {
		for {
			if p.nesting >= maxFormulaDepth {
				return nil, fmt.Errorf("formula nesting exceeds %d", maxFormulaDepth)
			}
			p.nesting++
			arg, err := p.parseExpression()
			p.nesting--
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
			if p.tok.Kind == TokComma {
				p.next()
				continue
			}
			break
		}
	}

	if p.tok.Kind != TokRParen {
		return nil, errors.New("expected ')'")
	}
	p.next()

	return &FunctionCallNode{
		Name: name,
		Args: args,
	}, nil
}

type refTarget struct {
	start   int
	end     int
	isRange bool
	ref     *RefNode
	rng     *RangeNode
}

func extractAllRefs(node Node) []refTarget {
	var targets []refTarget
	var walk func(n Node)
	walk = func(n Node) {
		switch t := n.(type) {
		case *RefNode:
			targets = append(targets, refTarget{
				start:   t.Start,
				end:     t.End,
				isRange: false,
				ref:     t,
			})
		case *RangeNode:
			targets = append(targets, refTarget{
				start:   t.Start,
				end:     t.End,
				isRange: true,
				rng:     t,
			})
		case *UnaryOpNode:
			walk(t.Expr)
		case *BinaryOpNode:
			walk(t.Left)
			walk(t.Right)
		case *FunctionCallNode:
			for _, arg := range t.Args {
				walk(arg)
			}
		}
	}
	walk(node)
	return targets
}

// TranslateFormula adjusts relative references when a formula is copied.
func TranslateFormula(formula string, rowDelta, colDelta int) (string, error) {
	if !strings.HasPrefix(formula, "=") {
		return "", errors.New("formula must begin with =")
	}
	node, err := NewParser(formula[1:]).Parse()
	if err != nil {
		return "", err
	}
	targets := extractAllRefs(node)
	sort.Slice(targets, func(i, j int) bool { return targets[i].start > targets[j].start })
	data := []byte(formula)
	translate := func(original string, row, col int, absRow, absCol bool) string {
		if !absRow {
			row += rowDelta
		}
		if !absCol {
			col += colDelta
		}
		if row < 1 || row > MaxRows || col < 1 || col > MaxColumns {
			return "#REF!"
		}
		return formatFormulaReference(original, row, col, absRow, absCol)
	}
	for _, target := range targets {
		start, end := target.start+1, target.end+1
		var replacement string
		if target.isRange {
			r := target.rng
			firstEnd, secondStart := r.FirstEnd+1, r.SecondStart+1
			first := translate(string(data[start:firstEnd]), r.FromRow, r.FromCol, r.FromAbsoluteRow, r.FromAbsoluteCol)
			second := translate(string(data[secondStart:end]), r.ToRow, r.ToCol, r.ToAbsoluteRow, r.ToAbsoluteCol)
			if first == "#REF!" || second == "#REF!" {
				replacement = "#REF!"
			} else {
				replacement = first + string(data[firstEnd:secondStart]) + second
			}
		} else {
			r := target.ref
			replacement = translate(string(data[start:end]), r.Row, r.Col, r.AbsoluteRow, r.AbsoluteCol)
		}
		data = append(data[:start], append([]byte(replacement), data[end:]...)...)
	}
	return string(data), nil
}

func shiftCellReferences(formula string, fromRow, fromColumn int, delete bool) string {
	return shiftCellReferencesWhere(formula, fromRow, fromColumn, delete, func(sheet string) bool {
		return sheet == ""
	})
}

func shiftCellReferencesForSheet(book *Spreadsheet, formulaSheet, changedSheet int, formula string, fromRow, fromColumn int, delete bool) string {
	return shiftCellReferencesWhere(formula, fromRow, fromColumn, delete, func(sheet string) bool {
		if sheet == "" {
			return formulaSheet == changedSheet
		}
		for i := range book.Sheets {
			if strings.EqualFold(book.Sheets[i].Name, sheet) {
				return i == changedSheet
			}
		}
		return false
	})
}

func shiftCellReferencesWhere(formula string, fromRow, fromColumn int, delete bool, matchesSheet func(string) bool) string {
	if !strings.HasPrefix(formula, "=") {
		return formula
	}
	parser := NewParser(strings.TrimPrefix(formula, "="))
	node, err := parser.Parse()
	if err != nil {
		return fallbackShift(formula, fromRow, fromColumn, delete)
	}

	targets := extractAllRefs(node)
	if len(targets) == 0 {
		return formula
	}

	sort.Slice(targets, func(i, j int) bool {
		return targets[i].start > targets[j].start
	})

	bytes := []byte(formula)
	for _, target := range targets {
		if target.isRange {
			rng := target.rng
			if !matchesSheet(rng.Sheet) {
				continue
			}

			r1, r2 := min(rng.FromRow, rng.ToRow), max(rng.FromRow, rng.ToRow)
			c1, c2 := min(rng.FromCol, rng.ToCol), max(rng.FromCol, rng.ToCol)

			start := rng.Start + 1
			end := rng.End + 1

			if delete {
				if fromRow > 0 {
					if fromRow < r1 {
						r1--
						r2--
					} else if fromRow >= r1 && fromRow <= r2 {
						if r1 == r2 {
							bytes = append(bytes[:start], append([]byte("#REF!"), bytes[end:]...)...)
							continue
						}
						r2--
					}
				}
				if fromColumn > 0 {
					if fromColumn < c1 {
						c1--
						c2--
					} else if fromColumn >= c1 && fromColumn <= c2 {
						if c1 == c2 {
							bytes = append(bytes[:start], append([]byte("#REF!"), bytes[end:]...)...)
							continue
						}
						c2--
					}
				}
			} else {
				if fromRow > 0 {
					if r1 >= fromRow {
						r1++
						r2++
					} else if r2 >= fromRow {
						r2++
					}
				}
				if fromColumn > 0 {
					if c1 >= fromColumn {
						c1++
						c2++
					} else if c2 >= fromColumn {
						c2++
					}
				}
			}

			if r1 < 1 || r2 > MaxRows || c1 < 1 || c2 > MaxColumns {
				bytes = append(bytes[:start], append([]byte("#REF!"), bytes[end:]...)...)
				continue
			}

			fromRow, toRow, fromCol, toCol := r1, r2, c1, c2
			if rng.FromRow > rng.ToRow {
				fromRow, toRow = toRow, fromRow
			}
			if rng.FromCol > rng.ToCol {
				fromCol, toCol = toCol, fromCol
			}
			firstEnd, secondStart := rng.FirstEnd+1, rng.SecondStart+1
			first := formatFormulaReference(string(bytes[start:firstEnd]), fromRow, fromCol, rng.FromAbsoluteRow, rng.FromAbsoluteCol)
			second := formatFormulaReference(string(bytes[secondStart:end]), toRow, toCol, rng.ToAbsoluteRow, rng.ToAbsoluteCol)
			if !strings.Contains(first, "!") {
				if separator := strings.LastIndexByte(second, '!'); separator >= 0 {
					first = second[:separator+1] + first
					second = second[separator+1:]
				}
			}
			newName := first + string(bytes[firstEnd:secondStart]) + second
			bytes = append(bytes[:start], append([]byte(newName), bytes[end:]...)...)
		} else {
			ref := target.ref
			if !matchesSheet(ref.Sheet) {
				continue
			}

			r, c := ref.Row, ref.Col
			start := ref.Start + 1
			end := ref.End + 1

			if delete && (fromRow > 0 && r == fromRow || fromColumn > 0 && c == fromColumn) {
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
				if r < 1 || r > MaxRows || c < 1 || c > MaxColumns {
					bytes = append(bytes[:start], append([]byte("#REF!"), bytes[end:]...)...)
					continue
				}
				newName := formatFormulaReference(string(bytes[start:end]), r, c, ref.AbsoluteRow, ref.AbsoluteCol)
				bytes = append(bytes[:start], append([]byte(newName), bytes[end:]...)...)
			}
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
