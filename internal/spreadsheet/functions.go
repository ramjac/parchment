package spreadsheet

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Display formats a calculated value for a grid or text export.
func (v Value) Display() string {
	switch v.Kind {
	case NumKind:
		return strconv.FormatFloat(v.Num, 'f', -1, 64)
	case StrKind:
		return v.Str
	case BoolKind:
		if v.Bool {
			return "TRUE"
		}
		return "FALSE"
	case ErrKind:
		return string(v.Err)
	default:
		return ""
	}
}

func compareValues(a, b Value) int {
	if a.Kind != StrKind && b.Kind != StrKind {
		left, _ := coerceToNumber(a)
		right, _ := coerceToNumber(b)
		switch {
		case left < right:
			return -1
		case left > right:
			return 1
		default:
			return 0
		}
	}
	if a.Kind != StrKind {
		return -1
	}
	if b.Kind != StrKind {
		return 1
	}
	return strings.Compare(strings.ToLower(a.Str), strings.ToLower(b.Str))
}

type valueGrid struct {
	rows, cols int
	values     []Value
	err        ErrorCode
}

func evaluateGrid(node Node, e *evaluator, currentSheet int) (valueGrid, error) {
	rng, ok := node.(*RangeNode)
	if !ok {
		value, err := node.Eval(e, currentSheet)
		if value.Kind == ArrayKind && value.array != nil {
			return *value.array, err
		}
		return valueGrid{rows: 1, cols: 1, values: []Value{value}}, err
	}
	sheet := currentSheet
	if rng.Sheet != "" {
		sheet = -1
		for i := range e.book.Sheets {
			if strings.EqualFold(e.book.Sheets[i].Name, rng.Sheet) {
				sheet = i
				break
			}
		}
		if sheet < 0 {
			return valueGrid{err: ErrRef}, nil
		}
	}
	firstRow, lastRow := min(rng.FromRow, rng.ToRow), max(rng.FromRow, rng.ToRow)
	firstCol, lastCol := min(rng.FromCol, rng.ToCol), max(rng.FromCol, rng.ToCol)
	grid := valueGrid{rows: lastRow - firstRow + 1, cols: lastCol - firstCol + 1}
	grid.values = make([]Value, 0, grid.rows*grid.cols)
	for row := firstRow; row <= lastRow; row++ {
		for col := firstCol; col <= lastCol; col++ {
			value, err := e.cell(sheet, row, col)
			if err != nil {
				return valueGrid{}, err
			}
			grid.values = append(grid.values, value)
		}
	}
	return grid, nil
}

func criterionMatcher(criterion Value) func(Value) bool {
	if criterion.Kind != StrKind {
		return func(value Value) bool { return compareValues(value, criterion) == 0 }
	}
	text := criterion.Str
	operator := "="
	for _, prefix := range []string{"<=", ">=", "<>", "<", ">", "="} {
		if strings.HasPrefix(text, prefix) {
			operator, text = prefix, strings.TrimPrefix(text, prefix)
			break
		}
	}
	if operator == "=" || operator == "<>" {
		var pattern strings.Builder
		pattern.WriteString("(?is)^")
		hasWildcard := false
		runes := []rune(text)
		for i := 0; i < len(runes); i++ {
			switch runes[i] {
			case '~':
				if i+1 < len(runes) && strings.ContainsRune("*?~", runes[i+1]) {
					i++
				}
				pattern.WriteString(regexp.QuoteMeta(string(runes[i])))
			case '*':
				pattern.WriteString(".*")
				hasWildcard = true
			case '?':
				pattern.WriteString(".")
				hasWildcard = true
			default:
				pattern.WriteString(regexp.QuoteMeta(string(runes[i])))
			}
		}
		pattern.WriteByte('$')
		if hasWildcard {
			re, err := regexp.Compile(pattern.String())
			if err != nil {
				return func(Value) bool { return false }
			}
			return func(value Value) bool { return re.MatchString(value.Display()) == (operator == "=") }
		}
		text = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(text, "~*", "*"), "~?", "?"), "~~", "~")
	}
	target := Value{Kind: StrKind, Str: text}
	if number, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil && !math.IsNaN(number) && !math.IsInf(number, 0) {
		target = Value{Kind: NumKind, Num: number}
	}
	return func(value Value) bool {
		if text == "" && value.Kind == EmptyKind {
			value = Value{Kind: StrKind}
		}
		cmp := compareValues(value, target)
		switch operator {
		case "=":
			return cmp == 0
		case "<>":
			return cmp != 0
		case "<":
			return cmp < 0
		case "<=":
			return cmp <= 0
		case ">":
			return cmp > 0
		case ">=":
			return cmp >= 0
		}
		return false
	}
}

func lookupIndex(needle Value, values []Value, mode, direction int) (int, ErrorCode) {
	best := -1
	wildcard := needle
	if wildcard.Kind == StrKind {
		wildcard.Str = "=" + wildcard.Str
	}
	matchWildcard := criterionMatcher(wildcard)
	for step := 0; step < len(values); step++ {
		i := step
		if direction < 0 {
			i = len(values) - 1 - step
		}
		value := values[i]
		if value.Kind == ErrKind {
			return -1, value.Err
		}
		cmp := compareValues(value, needle)
		if (mode == 2 && matchWildcard(value)) || (mode != 2 && cmp == 0) {
			return i, ""
		}
		if mode == -1 && cmp < 0 && (best < 0 || compareValues(value, values[best]) > 0) ||
			mode == 1 && cmp > 0 && (best < 0 || compareValues(value, values[best]) < 0) {
			best = i
		}
	}
	if best < 0 {
		return -1, ErrNA
	}
	return best, ""
}

func integerArgument(node Node, e *evaluator, sheet int) (int, ErrorCode, error) {
	value, err := node.Eval(e, sheet)
	if err != nil {
		return 0, "", err
	}
	if value.Kind == ErrKind {
		return 0, value.Err, nil
	}
	num, valueErr := coerceToNumber(value)
	if valueErr != nil || math.IsNaN(num) || math.IsInf(num, 0) || num < -1e9 || num > 1e9 {
		return 0, ErrValue, nil
	}
	return int(num), "", nil
}

func evalAdditionalFunction(name string, args []Node, e *evaluator, sheet int) (Value, bool, error) {
	bad := Value{Kind: ErrKind, Err: ErrValue}
	switch name {
	case "SEQUENCE", "FILTER", "SORT", "UNIQUE":
		value, err := evalArrayFunction(name, args, e, sheet)
		return value, true, err
	case "INDEX":
		if len(args) < 2 || len(args) > 3 {
			return bad, true, nil
		}
		grid, err := evaluateGrid(args[0], e, sheet)
		if err != nil {
			return Value{}, true, err
		}
		if grid.err != "" {
			return Value{Kind: ErrKind, Err: grid.err}, true, nil
		}
		row, code, err := integerArgument(args[1], e, sheet)
		if err != nil || code != "" {
			return Value{Kind: ErrKind, Err: code}, true, err
		}
		col := 1
		if len(args) == 3 {
			col, code, err = integerArgument(args[2], e, sheet)
			if err != nil || code != "" {
				return Value{Kind: ErrKind, Err: code}, true, err
			}
		}
		if row < 1 || row > grid.rows || col < 1 || col > grid.cols {
			return Value{Kind: ErrKind, Err: ErrRef}, true, nil
		}
		return grid.values[(row-1)*grid.cols+col-1], true, nil
	case "MATCH", "XMATCH", "XLOOKUP":
		minimum, maximum := 2, 4
		if name == "MATCH" {
			maximum = 3
		} else if name == "XLOOKUP" {
			minimum, maximum = 3, 6
		}
		if len(args) < minimum || len(args) > maximum {
			return bad, true, nil
		}
		needle, err := args[0].Eval(e, sheet)
		if err != nil || needle.Kind == ErrKind {
			return needle, true, err
		}
		grid, err := evaluateGrid(args[1], e, sheet)
		if err != nil {
			return Value{}, true, err
		}
		if grid.err != "" {
			return Value{Kind: ErrKind, Err: grid.err}, true, nil
		}
		if grid.rows != 1 && grid.cols != 1 {
			return bad, true, nil
		}
		var resultGrid valueGrid
		if name == "XLOOKUP" {
			resultGrid, err = evaluateGrid(args[2], e, sheet)
			if err != nil {
				return Value{}, true, err
			}
			if resultGrid.err != "" {
				return Value{Kind: ErrKind, Err: resultGrid.err}, true, nil
			}
			if resultGrid.rows != grid.rows || resultGrid.cols != grid.cols {
				return bad, true, nil
			}
		}
		mode, direction, modeArg, searchArg := 0, 1, 2, 3
		if name == "MATCH" {
			mode = -1
		} else if name == "XLOOKUP" {
			modeArg, searchArg = 4, 5
		}
		if len(args) > modeArg {
			n, code, err := integerArgument(args[modeArg], e, sheet)
			if err != nil || code != "" {
				return Value{Kind: ErrKind, Err: code}, true, err
			}
			mode = n
			if name == "MATCH" {
				mode = -mode
			}
		}
		if mode < -1 || mode > 2 || name == "MATCH" && mode == 2 {
			return bad, true, nil
		}
		if len(args) > searchArg {
			direction, grid.err, err = integerArgument(args[searchArg], e, sheet)
			if err != nil || grid.err != "" {
				return Value{Kind: ErrKind, Err: grid.err}, true, err
			}
			if direction != -1 && direction != 1 {
				return bad, true, nil
			}
		}
		index, code := lookupIndex(needle, grid.values, mode, direction)
		if code != "" {
			if name == "XLOOKUP" && code == ErrNA && len(args) > 3 {
				value, err := args[3].Eval(e, sheet)
				return value, true, err
			}
			return Value{Kind: ErrKind, Err: code}, true, nil
		}
		if name == "XLOOKUP" {
			return resultGrid.values[index], true, nil
		}
		return Value{Kind: NumKind, Num: float64(index + 1)}, true, nil
	case "SUMIFS", "COUNTIFS":
		firstRange := 0
		if name == "SUMIFS" {
			firstRange = 1
		}
		if len(args) < firstRange+2 || (len(args)-firstRange)%2 != 0 {
			return bad, true, nil
		}
		var sums valueGrid
		var err error
		if firstRange == 1 {
			sums, err = evaluateGrid(args[0], e, sheet)
			if err != nil {
				return Value{}, true, err
			}
			if sums.err != "" {
				return Value{Kind: ErrKind, Err: sums.err}, true, nil
			}
		}
		var matches []bool
		rows, cols := 0, 0
		for i := firstRange; i < len(args); i += 2 {
			grid, err := evaluateGrid(args[i], e, sheet)
			if err != nil {
				return Value{}, true, err
			}
			if grid.err != "" {
				return Value{Kind: ErrKind, Err: grid.err}, true, nil
			}
			if i == firstRange {
				rows, cols = grid.rows, grid.cols
				matches = make([]bool, len(grid.values))
				for j := range matches {
					matches[j] = true
				}
				if firstRange == 1 && (sums.rows != rows || sums.cols != cols) {
					return bad, true, nil
				}
			} else if rows != grid.rows || cols != grid.cols {
				return bad, true, nil
			}
			if len(grid.values)*(len(args)-firstRange)/2 > 1<<20 {
				return bad, true, nil
			}
			criterion, err := args[i+1].Eval(e, sheet)
			if err != nil || criterion.Kind == ErrKind {
				return criterion, true, err
			}
			matchesCriterion := criterionMatcher(criterion)
			for j, value := range grid.values {
				if value.Kind == ErrKind {
					return value, true, nil
				}
				matches[j] = matches[j] && matchesCriterion(value)
			}
		}
		result := 0.0
		for i, match := range matches {
			if !match {
				continue
			}
			if firstRange == 0 {
				result++
			} else if sums.values[i].Kind == ErrKind {
				return sums.values[i], true, nil
			} else if sums.values[i].Kind == NumKind {
				result += sums.values[i].Num
			}
		}
		return Value{Kind: NumKind, Num: result}, true, nil
	case "TEXTJOIN":
		if len(args) < 3 {
			return bad, true, nil
		}
		delimiter, err := args[0].Eval(e, sheet)
		if err != nil || delimiter.Kind == ErrKind {
			return delimiter, true, err
		}
		ignore, err := args[1].Eval(e, sheet)
		if err != nil || ignore.Kind == ErrKind {
			return ignore, true, err
		}
		num, valueErr := coerceToNumber(ignore)
		if valueErr != nil {
			return *valueErr, true, nil
		}
		var parts []string
		size := 0
		for _, arg := range args[2:] {
			grid, err := evaluateGrid(arg, e, sheet)
			if err != nil {
				return Value{}, true, err
			}
			if grid.err != "" {
				return Value{Kind: ErrKind, Err: grid.err}, true, nil
			}
			for _, value := range grid.values {
				if value.Kind == ErrKind {
					return value, true, nil
				}
				text := value.Display()
				if num != 0 && text == "" {
					continue
				}
				size += len(text) + len(delimiter.Display())
				if size > MaxFormulaLength*256 {
					return bad, true, nil
				}
				parts = append(parts, text)
			}
		}
		return Value{Kind: StrKind, Str: strings.Join(parts, delimiter.Display())}, true, nil
	case "DATE", "YEAR", "MONTH", "DAY", "TODAY", "NOW":
		want := 1
		if name == "DATE" {
			want = 3
		} else if name == "TODAY" || name == "NOW" {
			want = 0
		}
		if len(args) != want {
			return bad, true, nil
		}
		nums := make([]float64, len(args))
		for i, arg := range args {
			value, err := arg.Eval(e, sheet)
			if err != nil || value.Kind == ErrKind {
				return value, true, err
			}
			num, valueErr := coerceToNumber(value)
			if valueErr != nil {
				return *valueErr, true, nil
			}
			nums[i] = num
		}
		epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
		switch name {
		case "TODAY", "NOW":
			now := e.now.UTC()
			serial := float64(now.Unix()-epoch.Unix())/86400 + float64(now.Nanosecond())/86400e9
			if name == "TODAY" {
				serial = math.Floor(serial)
			}
			return Value{Kind: NumKind, Num: serial}, true, nil
		case "DATE":
			if nums[0] < 1 || nums[0] > 9999 || math.Abs(nums[1]) > 120000 || math.Abs(nums[2]) > 4000000 {
				return bad, true, nil
			}
			date := time.Date(int(nums[0]), time.Month(int(nums[1])), int(nums[2]), 0, 0, 0, 0, time.UTC)
			if date.Year() < 1 || date.Year() > 9999 {
				return bad, true, nil
			}
			return Value{Kind: NumKind, Num: float64(date.Unix()-epoch.Unix()) / 86400}, true, nil
		default:
			days := math.Floor(nums[0])
			if days < -693593 || days > 2958465 {
				return bad, true, nil
			}
			date := time.Unix(epoch.Unix()+int64(days)*86400, 0).UTC()
			part := date.Year()
			if name == "MONTH" {
				part = int(date.Month())
			} else if name == "DAY" {
				part = date.Day()
			}
			return Value{Kind: NumKind, Num: float64(part)}, true, nil
		}
	case "IF":
		if len(args) < 2 || len(args) > 3 {
			return bad, true, nil
		}
		condition, err := args[0].Eval(e, sheet)
		if err != nil || condition.Kind == ErrKind {
			return condition, true, err
		}
		number, valueErr := coerceToNumber(condition)
		if valueErr != nil {
			return *valueErr, true, nil
		}
		if number != 0 {
			value, err := args[1].Eval(e, sheet)
			return value, true, err
		}
		if len(args) == 3 {
			value, err := args[2].Eval(e, sheet)
			return value, true, err
		}
		return Value{Kind: BoolKind}, true, nil
	case "IFERROR":
		if len(args) != 2 {
			return bad, true, nil
		}
		value, err := args[0].Eval(e, sheet)
		if err != nil {
			return Value{}, true, err
		}
		if value.Kind == ErrKind {
			value, err = args[1].Eval(e, sheet)
		}
		return value, true, err
	case "ROUND":
		if len(args) != 2 {
			return bad, true, nil
		}
		numbers := [2]float64{}
		for i, arg := range args {
			value, err := arg.Eval(e, sheet)
			if err != nil || value.Kind == ErrKind {
				return value, true, err
			}
			num, valueErr := coerceToNumber(value)
			if valueErr != nil {
				return *valueErr, true, nil
			}
			numbers[i] = num
		}
		digits := math.Trunc(numbers[1])
		if digits > 308 || digits < -308 {
			return bad, true, nil
		}
		factor := math.Pow10(int(digits))
		result := math.Round(numbers[0]*factor) / factor
		if math.IsInf(result, 0) || math.IsNaN(result) {
			return bad, true, nil
		}
		return Value{Kind: NumKind, Num: result}, true, nil
	case "COUNTA":
		if len(args) == 0 {
			return bad, true, nil
		}
		count := 0
		for _, arg := range args {
			if rng, ok := arg.(*RangeNode); ok {
				index := sheet
				if rng.Sheet != "" {
					index = -1
					for i := range e.book.Sheets {
						if strings.EqualFold(e.book.Sheets[i].Name, rng.Sheet) {
							index = i
							break
						}
					}
					if index < 0 {
						return Value{Kind: ErrKind, Err: ErrRef}, true, nil
					}
				}
				for r := min(rng.FromRow, rng.ToRow); r <= max(rng.FromRow, rng.ToRow); r++ {
					for c := min(rng.FromCol, rng.ToCol); c <= max(rng.FromCol, rng.ToCol); c++ {
						value, err := e.cell(index, r, c)
						if err != nil {
							return Value{}, true, err
						}
						if value.Kind != EmptyKind {
							count++
						}
					}
				}
				continue
			}
			value, err := arg.Eval(e, sheet)
			if err != nil {
				return Value{}, true, err
			}
			if value.Kind == ArrayKind {
				for _, item := range value.array.values {
					if item.Kind != EmptyKind {
						count++
					}
				}
			} else if value.Kind != EmptyKind {
				count++
			}
		}
		return Value{Kind: NumKind, Num: float64(count)}, true, nil
	default:
		return Value{}, false, nil
	}
}
