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
