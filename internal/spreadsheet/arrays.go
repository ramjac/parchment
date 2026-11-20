package spreadsheet

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

func arrayResult(rows, cols int, values []Value) Value {
	grid := valueGrid{rows: rows, cols: cols, values: values}
	return Value{Kind: ArrayKind, array: &grid}
}

func evalArrayFunction(name string, args []Node, e *evaluator, sheet int) (Value, error) {
	bad := Value{Kind: ErrKind, Err: ErrValue}
	if name == "SEQUENCE" {
		if len(args) < 1 || len(args) > 4 {
			return bad, nil
		}
		rows, code, err := integerArgument(args[0], e, sheet)
		if err != nil || code != "" {
			return Value{Kind: ErrKind, Err: code}, err
		}
		cols := 1
		if len(args) > 1 {
			cols, code, err = integerArgument(args[1], e, sheet)
			if err != nil || code != "" {
				return Value{Kind: ErrKind, Err: code}, err
			}
		}
		if rows < 1 || rows > MaxRows || cols < 1 || cols > MaxColumns {
			return bad, nil
		}
		start, step := 1.0, 1.0
		for i := 2; i < len(args); i++ {
			value, err := args[i].Eval(e, sheet)
			if err != nil || value.Kind == ErrKind {
				return value, err
			}
			num, code := coerceToNumber(value)
			if code != nil {
				return *code, nil
			}
			if i == 2 {
				start = num
			} else {
				step = num
			}
		}
		values := make([]Value, rows*cols)
		for i := range values {
			num := start + float64(i)*step
			if math.IsNaN(num) || math.IsInf(num, 0) {
				return bad, nil
			}
			values[i] = Value{Kind: NumKind, Num: num}
		}
		return arrayResult(rows, cols, values), nil
	}
	if len(args) == 0 {
		return bad, nil
	}
	grid, err := evaluateGrid(args[0], e, sheet)
	if err != nil {
		return Value{}, err
	}
	if grid.err != "" {
		return Value{Kind: ErrKind, Err: grid.err}, nil
	}
	switch name {
	case "FILTER":
		if len(args) < 2 || len(args) > 3 {
			return bad, nil
		}
		include, err := evaluateGrid(args[1], e, sheet)
		if err != nil {
			return Value{}, err
		}
		if include.err != "" {
			return Value{Kind: ErrKind, Err: include.err}, nil
		}
		byRows := include.rows == grid.rows && include.cols == 1
		byCols := include.rows == 1 && include.cols == grid.cols
		if !byRows && !byCols {
			return bad, nil
		}
		var selected []int
		for i, value := range include.values {
			num, valueErr := coerceToNumber(value)
			if valueErr != nil {
				return *valueErr, nil
			}
			if num != 0 {
				selected = append(selected, i)
			}
		}
		if len(selected) == 0 {
			if len(args) == 3 {
				return args[2].Eval(e, sheet)
			}
			return Value{Kind: ErrKind, Err: ErrCalc}, nil
		}
		if byRows {
			values := make([]Value, 0, len(selected)*grid.cols)
			for _, row := range selected {
				values = append(values, grid.values[row*grid.cols:(row+1)*grid.cols]...)
			}
			return arrayResult(len(selected), grid.cols, values), nil
		}
		values := make([]Value, 0, len(selected)*grid.rows)
		for r := 0; r < grid.rows; r++ {
			for _, c := range selected {
				values = append(values, grid.values[r*grid.cols+c])
			}
		}
		return arrayResult(grid.rows, len(selected), values), nil
	case "SORT", "UNIQUE":
		maxArgs := 4
		if name == "UNIQUE" {
			maxArgs = 3
		}
		if len(args) > maxArgs {
			return bad, nil
		}
		byCol, index, order, once := false, 1, 1, false
		for i := 1; i < len(args); i++ {
			n, code, err := integerArgument(args[i], e, sheet)
			if err != nil || code != "" {
				return Value{Kind: ErrKind, Err: code}, err
			}
			if name == "SORT" {
				switch i {
				case 1:
					index = n
				case 2:
					order = n
				case 3:
					byCol = n != 0
				}
			} else if i == 1 {
				byCol = n != 0
			} else {
				once = n != 0
			}
		}
		count, width := grid.rows, grid.cols
		if byCol {
			count, width = grid.cols, grid.rows
		}
		if name == "SORT" && (index < 1 || index > width || order != 1 && order != -1) {
			return bad, nil
		}
		get := func(group, item int) Value {
			if byCol {
				return grid.values[item*grid.cols+group]
			}
			return grid.values[group*grid.cols+item]
		}
		groups := make([]int, count)
		for i := range groups {
			groups[i] = i
			for j := 0; j < width; j++ {
				if value := get(i, j); value.Kind == ErrKind {
					return value, nil
				}
			}
		}
		if name == "SORT" {
			sort.SliceStable(groups, func(i, j int) bool {
				return order*compareValues(get(groups[i], index-1), get(groups[j], index-1)) < 0
			})
		} else {
			keys := make([]string, count)
			frequency := make(map[string]int, count)
			for i := range groups {
				var key strings.Builder
				for j := 0; j < width; j++ {
					value := get(i, j)
					text := strings.ToLower(value.Display())
					fmt.Fprintf(&key, "%d:%s:%s;", value.Kind, strconv.Itoa(len(text)), text)
				}
				keys[i] = key.String()
				frequency[keys[i]]++
			}
			seen := make(map[string]bool)
			groups = groups[:0]
			for i, key := range keys {
				if !seen[key] && (!once || frequency[key] == 1) {
					groups = append(groups, i)
				}
				seen[key] = true
			}
			if len(groups) == 0 {
				return Value{Kind: ErrKind, Err: ErrCalc}, nil
			}
		}
		rows, cols := len(groups), width
		if byCol {
			rows, cols = width, len(groups)
		}
		values := make([]Value, rows*cols)
		for i, group := range groups {
			for j := 0; j < width; j++ {
				if byCol {
					values[j*cols+i] = get(group, j)
				} else {
					values[i*cols+j] = get(group, j)
				}
			}
		}
		return arrayResult(rows, cols, values), nil
	}
	return bad, nil
}
