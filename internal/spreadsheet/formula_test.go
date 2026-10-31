package spreadsheet

import (
	"math"
	"strings"
	"testing"
)

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name      string
		formula   string
		value     string
		row       int
		col       int
		want      float64
		wantError string
	}{
		{name: "empty cell", value: "", want: 0},
		{name: "number value", value: "42.5", want: 42.5},
		{name: "invalid number", value: "abc", wantError: "is not numeric"},
		{name: "literal formula", formula: "=123", want: 123},
		{name: "addition", formula: "=1+2", want: 3},
		{name: "subtraction", formula: "=5-3", want: 2},
		{name: "multiplication", formula: "=4*5", want: 20},
		{name: "division", formula: "=20/4", want: 5},
		{name: "division by zero", formula: "=1/0", wantError: "division by zero"},
		{name: "parentheses", formula: "=2*(3+4)", want: 14},
		{name: "unary minus", formula: "=-5", want: -5},
		{name: "multiple unary", formula: "=--5", want: 5},
		{name: "spaces in formula", formula: "= 1 +  2 * ( 3 ) ", want: 7},
		{name: "cell reference", formula: "=A2", want: 10, row: 1, col: 1}, // A2 will have value 10 in our mock
		{name: "decimal exponent", formula: "=1.2e2", want: 120},
		{name: "invalid cell reference", formula: "=1+a", wantError: "invalid cell reference"},
		{name: "unmatched paren", formula: "=(1+2", wantError: "expected ')'"},
		{name: "unary plus", formula: "=+5", want: 5},
		{name: "infinity result", formula: "=1e300*1e300", wantError: "not finite"},
		{name: "invalid decimal exponent 1", formula: "=1ea", wantError: "invalid number"},
		{name: "invalid decimal exponent 2", formula: "=1e", wantError: "invalid number"},
		{name: "invalid decimal exponent 3", formula: "=1e+", wantError: "invalid number"},
		{name: "invalid decimal missing digits", formula: "=.", wantError: "invalid number"},
		{name: "unexpected trailing chars", formula: "=1+2 a", wantError: "unexpected character"},
		{name: "incomplete unary minus", formula: "=-", wantError: "expected a number"},
		{name: "incomplete unary minus space", formula: "=- ", wantError: "expected a number"},
		{name: "unexpected symbol in term", formula: "=1*", wantError: "expected a number"},
		{name: "invalid reference coordinate 0", formula: "=A0", wantError: "outside supported dimensions"},
		{name: "parse float out of range", formula: "=1e1000000000000", wantError: "invalid number"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			book := &Spreadsheet{
				Sheets: []Sheet{
					{
						Rows: [][]Cell{
							{{Formula: tt.formula, Value: tt.value}}, // A1
							{{Value: "10"}},                          // A2
						},
					},
				},
			}

			r, c := 1, 1
			if tt.row != 0 {
				r = tt.row
			}
			if tt.col != 0 {
				c = tt.col
			}

			got, err := Evaluate(book, 0, r, c)

			if tt.wantError != "" {
				if err == nil {
					t.Fatalf("Evaluate() expected error %q, got nil", tt.wantError)
				}
				if !strings.Contains(err.Error(), tt.wantError) {
					t.Errorf("Evaluate() error = %v, wantError %q", err, tt.wantError)
				}
				return
			}

			if err != nil {
				t.Fatalf("Evaluate() unexpected error: %v", err)
			}

			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("Evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvaluateCircularReference(t *testing.T) {
	book := &Spreadsheet{
		Sheets: []Sheet{
			{
				Rows: [][]Cell{
					{{Formula: "=A2"}}, // A1
					{{Formula: "=A1"}}, // A2
				},
			},
		},
	}

	_, err := Evaluate(book, 0, 1, 1)
	if err == nil {
		t.Fatal("Evaluate() expected error for circular reference, got nil")
	}
	if !strings.Contains(err.Error(), "circular reference") {
		t.Errorf("Evaluate() error = %v, want circular reference", err)
	}
}

func TestEvaluateUnknownCharacter(t *testing.T) {
	// an unexpected character that is not a valid cell reference start
	book := &Spreadsheet{
		Sheets: []Sheet{
			{
				Rows: [][]Cell{
					{{Formula: "=1 + @"}}, // A1
				},
			},
		},
	}

	_, err := Evaluate(book, 0, 1, 1)
	if err == nil {
		t.Fatal("Evaluate() expected error for unexpected character, got nil")
	}
	if !strings.Contains(err.Error(), "unexpected character") {
		t.Errorf("Evaluate() error = %v, want unexpected character", err)
	}
}

func TestEvaluateOutOfBounds(t *testing.T) {
	book := &Spreadsheet{
		Sheets: []Sheet{
			{
				Rows: [][]Cell{
					{{Value: "10"}},
				},
			},
		},
	}

	// Negative sheet index
	_, err := Evaluate(book, -1, 1, 1)
	if err == nil || !strings.Contains(err.Error(), "cell coordinates are outside the workbook") {
		t.Errorf("Expected out of bounds error, got %v", err)
	}

	// Row 0
	_, err = Evaluate(book, 0, 0, 1)
	if err == nil || !strings.Contains(err.Error(), "cell coordinates are outside the workbook") {
		t.Errorf("Expected out of bounds error, got %v", err)
	}

	// Column 0
	_, err = Evaluate(book, 0, 1, 0)
	if err == nil || !strings.Contains(err.Error(), "cell coordinates are outside the workbook") {
		t.Errorf("Expected out of bounds error, got %v", err)
	}
}

func TestEvaluateExceedsMaxNesting(t *testing.T) {
	book := &Spreadsheet{
		Sheets: []Sheet{
			{
				Rows: [][]Cell{
					{{Formula: "=" + strings.Repeat("(", 513) + "1" + strings.Repeat(")", 513)}},
				},
			},
		},
	}

	_, err := Evaluate(book, 0, 1, 1)
	if err == nil || !strings.Contains(err.Error(), "formula nesting exceeds") {
		t.Errorf("Expected nesting exceeds error, got %v", err)
	}
}

func TestEvaluateExceedsMaxDependency(t *testing.T) {
	book := &Spreadsheet{
		Sheets: []Sheet{
			{
				Rows: make([][]Cell, 514),
			},
		},
	}
	for i := 0; i < 513; i++ {
		book.Sheets[0].Rows[i] = []Cell{{Formula: "=" + CellName(i+2, 1)}}
	}
	book.Sheets[0].Rows[513] = []Cell{{Value: "1"}}

	_, err := Evaluate(book, 0, 1, 1)
	if err == nil || !strings.Contains(err.Error(), "formula dependency depth exceeds") {
		t.Errorf("Expected dependency depth exceeds error, got %v", err)
	}
}

func TestEvaluateCellOutsideSheetDimension(t *testing.T) {
	book := &Spreadsheet{
		Sheets: []Sheet{
			{
				Rows: [][]Cell{
					{{Value: "1"}}, // Only A1 exists
				},
			},
		},
	}

	// Reference a cell that is within workbook max limits but beyond the allocated grid slice.
	// Empty referenced cells evaluate to zero.
	val, err := Evaluate(book, 0, 2, 2)
	if err != nil {
		t.Errorf("Expected nil error for empty cell outside allocated grid, got %v", err)
	}
	if val != 0 {
		t.Errorf("Expected 0 value for empty cell outside allocated grid, got %v", val)
	}
}

func TestShiftCellReferences(t *testing.T) {
	tests := []struct {
		name       string
		formula    string
		fromRow    int
		fromColumn int
		want       string
	}{
		{name: "no shift", formula: "=A1", fromRow: 0, fromColumn: 0, want: "=A1"},
		{name: "shift row", formula: "=A1", fromRow: 1, fromColumn: 0, want: "=A2"},
		{name: "shift column", formula: "=A1", fromRow: 0, fromColumn: 1, want: "=B1"},
		{name: "shift both", formula: "=A1", fromRow: 1, fromColumn: 1, want: "=B2"},
		{name: "multiple references", formula: "=A1+B2", fromRow: 1, fromColumn: 1, want: "=B2+C3"},
		{name: "no shift if before row", formula: "=A1+A3", fromRow: 2, fromColumn: 0, want: "=A1+A4"},
		{name: "no shift if before col", formula: "=A1+C1", fromRow: 0, fromColumn: 2, want: "=A1+D1"},
		{name: "not a reference A", formula: "=A", fromRow: 1, fromColumn: 1, want: "=A"},
		{name: "not a reference 1", formula: "=1", fromRow: 1, fromColumn: 1, want: "=1"},
		{name: "not a reference A1A", formula: "=A1A", fromRow: 1, fromColumn: 1, want: "=A1A"},
		{name: "not a reference 1A1", formula: "=1A1", fromRow: 1, fromColumn: 1, want: "=1A1"},
		{name: "decimal exponent skips", formula: "=1e2+A1", fromRow: 1, fromColumn: 1, want: "=1e2+B2"},
		{name: "decimal exponent sign skips", formula: "=1.5e+2+A1", fromRow: 1, fromColumn: 1, want: "=1.5e+2+B2"},
		{name: "word not reference", formula: "=SUM(A1,B1)", fromRow: 1, fromColumn: 1, want: "=SUM(B2,C2)"},
		{name: "invalid reference format", formula: "=A-1", fromRow: 1, fromColumn: 1, want: "=A-1"},
		{name: "invalid reference 0", formula: "=A0+1", fromRow: 1, fromColumn: 1, want: "=A0+1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shiftCellReferences(tt.formula, tt.fromRow, tt.fromColumn)
			if got != tt.want {
				t.Errorf("shiftCellReferences(%q, %d, %d) = %q, want %q", tt.formula, tt.fromRow, tt.fromColumn, got, tt.want)
			}
		})
	}
}
