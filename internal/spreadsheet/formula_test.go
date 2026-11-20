package spreadsheet

import (
	"math"
	"strings"
	"testing"
	"time"
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
		{name: "division by zero", formula: "=1/0", wantError: "#DIV/0!"},
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

			gotV, err := Evaluate(book, 0, r, c)
			var got float64
			if err == nil {
				got, err = gotV.Float64()
			}

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

	valV, err := Evaluate(book, 0, 1, 1)
	if err == nil {
		_, err = valV.Float64()
	}
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

	valV, err := Evaluate(book, 0, 1, 1)
	if err == nil {
		_, err = valV.Float64()
	}
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
	valV, err := Evaluate(book, -1, 1, 1)
	if err == nil {
		_, err = valV.Float64()
	}
	if err == nil || !strings.Contains(err.Error(), "cell coordinates are outside the workbook") {
		t.Errorf("Expected out of bounds error, got %v", err)
	}

	// Row 0
	valV, err = Evaluate(book, 0, 0, 1)
	if err == nil {
		_, err = valV.Float64()
	}
	if err == nil || !strings.Contains(err.Error(), "cell coordinates are outside the workbook") {
		t.Errorf("Expected out of bounds error, got %v", err)
	}

	// Column 0
	valV, err = Evaluate(book, 0, 1, 0)
	if err == nil {
		_, err = valV.Float64()
	}
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

	valV, err := Evaluate(book, 0, 1, 1)
	if err == nil {
		_, err = valV.Float64()
	}
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

	valV, err := Evaluate(book, 0, 1, 1)
	if err == nil {
		_, err = valV.Float64()
	}
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
	valV, err := Evaluate(book, 0, 2, 2)
	var val float64
	if err == nil {
		val, err = valV.Float64()
	}
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
			got := shiftCellReferences(tt.formula, tt.fromRow, tt.fromColumn, false)
			if got != tt.want {
				t.Errorf("shiftCellReferences(%q, %d, %d) = %q, want %q", tt.formula, tt.fromRow, tt.fromColumn, got, tt.want)
			}
		})
	}
}

func TestAbsoluteReferencesEvaluateAndFollowStructuralEdits(t *testing.T) {
	book := &Spreadsheet{Sheets: []Sheet{
		{Name: "Sheet1", Rows: [][]Cell{{{Value: "3"}, {Formula: "=$A$1+A$1+$A1"}}}},
		{Name: "Other Sheet", Rows: [][]Cell{{{Value: "4"}}}},
		{Name: "Team's! Sheet", Rows: [][]Cell{{{Value: "5"}}}},
	}}
	value, err := Evaluate(book, 0, 1, 2)
	if err != nil || value.Kind != NumKind || value.Num != 9 {
		t.Fatalf("evaluate mixed references = %#v, %v", value, err)
	}
	book.Sheets[0].Rows[0][1].Formula = "='Team''s! Sheet'!$A$1"
	value, err = Evaluate(book, 0, 1, 2)
	if err != nil || value.Kind != NumKind || value.Num != 5 {
		t.Fatalf("evaluate quoted sheet reference = %#v, %v", value, err)
	}
	tests := []struct{ formula, want string }{
		{"=$A$1+A$1+$A1", "=$A$2+A$2+$A2"},
		{"=SUM('Other Sheet'!$B$2:A$1)", "=SUM('Other Sheet'!$B$3:A$2)"},
		{"=SUM($A$1:$B2)", "=SUM($A$2:$B3)"},
	}
	for _, tt := range tests {
		if got := shiftCellReferencesWhere(tt.formula, 1, 0, false, func(string) bool { return true }); got != tt.want {
			t.Errorf("shift %q = %q, want %q", tt.formula, got, tt.want)
		}
	}
}

func TestTranslateFormula(t *testing.T) {
	tests := []struct {
		formula  string
		row, col int
		want     string
	}{
		{`=A1+$A1+A$1+$A$1`, 2, 1, `=B3+$A3+B$1+$A$1`},
		{`=SUM('Other Sheet'!$B2:C$4)+"A1"`, -1, 2, `=SUM('Other Sheet'!$B1:E$4)+"A1"`},
		{`='Team''s! Sheet'!$A1`, 1, 1, `='Team''s! Sheet'!$A2`},
		{`=A1+$A$1`, -1, 0, `=#REF!+$A$1`},
		{`=SUM(A1:B2)`, 0, -1, `=SUM(#REF!)`},
		{`=IV1000`, 1, 1, `=#REF!`},
	}
	for _, tt := range tests {
		got, err := TranslateFormula(tt.formula, tt.row, tt.col)
		if err != nil || got != tt.want {
			t.Errorf("TranslateFormula(%q, %d, %d) = %q, %v; want %q", tt.formula, tt.row, tt.col, got, err, tt.want)
		}
	}
}

func TestAdditionalScalarFunctions(t *testing.T) {
	tests := []struct {
		formula string
		want    string
	}{
		{`=IF(2>1,"yes",1/0)`, "yes"},
		{`=IF(2<1,1/0,"no")`, "no"},
		{`=IFERROR(1/0,"fallback")`, "fallback"},
		{`=IFERROR("ok",1/0)`, "ok"},
		{`=ROUND(-12.345,2)`, "-12.35"},
		{`=ROUND(1234,-2)`, "1200"},
		{`=COUNTA(A2:A5)`, "3"},
		{`=A2`, "text"},
		{`=IF("abc"="ABC",TRUE,FALSE)`, "TRUE"},
	}
	for _, tt := range tests {
		book := &Spreadsheet{Sheets: []Sheet{{Rows: [][]Cell{
			{{Formula: tt.formula}}, {{Value: "text"}}, {{}}, {{Value: "0"}}, {{Formula: `=""`}},
		}}}}
		value, err := Evaluate(book, 0, 1, 1)
		if err != nil || value.Display() != tt.want {
			t.Errorf("%s = %q, %v; want %q", tt.formula, value.Display(), err, tt.want)
		}
	}
}

func TestDateFunctionsUseOneUTCInstant(t *testing.T) {
	book := &Spreadsheet{Sheets: []Sheet{{Rows: [][]Cell{{
		{Formula: "=DATE(2024,2,29)"}, {Formula: "=YEAR(A1)"},
		{Formula: "=MONTH(A1)"}, {Formula: "=DAY(A1)"},
		{Formula: "=TODAY()"}, {Formula: "=NOW()"},
	}}}}}
	now := time.Date(2024, 3, 1, 0, 0, 0, 0, time.FixedZone("test", 2*60*60))
	e := NewEvaluatorAt(book, now)
	want := []float64{45351, 2024, 2, 29, 45351, 45351 + 22.0/24}
	for i, expected := range want {
		value, err := e.Evaluate(0, 1, i+1)
		if err != nil || value.Kind != NumKind || math.Abs(value.Num-expected) > 1e-8 {
			t.Errorf("column %d = %#v, %v; want %v", i+1, value, err, expected)
		}
	}
}

func TestCriteriaAndTextJoinFunctions(t *testing.T) {
	tests := []struct{ formula, want string }{
		{`=COUNTIFS(A2:A5,">=2",B2:B5,"ca*")`, "2"},
		{`=SUMIFS(A2:A5,B2:B5,"CA*")`, "5"},
		{`=COUNTIFS(B2:B5,"~*")`, "1"},
		{`=TEXTJOIN("/",TRUE,B2:B5)`, "cat/car/*"},
		{`=TEXTJOIN("/",FALSE,B2:B5)`, "cat/car/*/"},
		{`=COUNTIFS(A2:A5,">1",B2:B4,"*")`, "#VALUE!"},
	}
	for _, tt := range tests {
		book := &Spreadsheet{Sheets: []Sheet{{Rows: [][]Cell{
			{{Formula: tt.formula}},
			{{Value: "2"}, {Value: "cat"}},
			{{Value: "3"}, {Value: "car"}},
			{{Value: "4"}, {Value: "*"}},
			{{Value: "1"}, {}},
		}}}}
		value, err := Evaluate(book, 0, 1, 1)
		if err != nil || value.Display() != tt.want {
			t.Errorf("%s = %q, %v; want %q", tt.formula, value.Display(), err, tt.want)
		}
	}
}

func TestLookupFunctions(t *testing.T) {
	tests := []struct{ formula, want string }{
		{`=INDEX(A2:B4,2,2)`, "bee"},
		{`=INDEX(A2:B4,4,1)`, "#REF!"},
		{`=MATCH(3,A2:A4,0)`, "2"},
		{`=MATCH(4,A2:A4,1)`, "2"},
		{`=XMATCH(3,A2:A4,0,-1)`, "2"},
		{`=XMATCH("b*",B2:B4,2)`, "2"},
		{`=XLOOKUP(5,A2:A4,B2:B4,"missing")`, "see"},
		{`=XLOOKUP(4,A2:A4,B2:B4,"missing")`, "missing"},
		{`=XLOOKUP(4,A2:A4,B2:B4,"missing",-1)`, "bee"},
		{`=IFERROR(MATCH(9,A2:A4,0),"no")`, "no"},
	}
	for _, tt := range tests {
		book := &Spreadsheet{Sheets: []Sheet{{Rows: [][]Cell{
			{{Formula: tt.formula}},
			{{Value: "1"}, {Value: "aye"}},
			{{Value: "3"}, {Value: "bee"}},
			{{Value: "5"}, {Value: "see"}},
		}}}}
		value, err := Evaluate(book, 0, 1, 1)
		if err != nil || value.Display() != tt.want {
			t.Errorf("%s = %q, %v; want %q", tt.formula, value.Display(), err, tt.want)
		}
	}
}

func TestDynamicArraySpills(t *testing.T) {
	tests := []struct {
		formula string
		rows    [][]Cell
		want    []string
	}{
		{`=SEQUENCE(2,2,2,3)`, nil, []string{"2", "5", "8", "11"}},
		{`=FILTER(C1:D3,C1:C3>1)`, [][]Cell{
			{{}, {}, {Value: "1"}, {Value: "a"}},
			{{}, {}, {Value: "2"}, {Value: "b"}},
			{{}, {}, {Value: "3"}, {Value: "c"}},
		}, []string{"2", "b", "3", "c"}},
		{`=SORT(C1:D3,1,-1)`, [][]Cell{
			{{}, {}, {Value: "1"}, {Value: "a"}},
			{{}, {}, {Value: "2"}, {Value: "b"}},
			{{}, {}, {Value: "3"}, {Value: "c"}},
		}, []string{"3", "c", "2", "b"}},
		{`=UNIQUE(C1:C3)`, [][]Cell{{{}, {}, {Value: "a"}}, {{}, {}, {Value: "b"}}, {{}, {}, {Value: "a"}}}, []string{"a", "", "b", ""}},
	}
	for _, tt := range tests {
		rows := tt.rows
		if len(rows) == 0 {
			rows = [][]Cell{{{}}}
		}
		rows[0][0] = Cell{Formula: tt.formula}
		book := &Spreadsheet{Sheets: []Sheet{{Name: "Sheet1", Rows: rows}}}
		e := NewEvaluator(book)
		for i, want := range tt.want {
			value, err := e.Evaluate(0, 1+i/2, 1+i%2)
			if err != nil || value.Display() != want {
				t.Errorf("%s cell %s = %q, %v; want %q", tt.formula, CellName(1+i/2, 1+i%2), value.Display(), err, want)
			}
		}
	}
}

func TestSpillCollisionAndReferences(t *testing.T) {
	book := &Spreadsheet{Sheets: []Sheet{{Name: "Sheet1", Rows: [][]Cell{
		{{Formula: "=SEQUENCE(3)"}, {Formula: "=A3*2"}, {Formula: "=SUM(A1:A3)"}},
	}}}}
	e := NewEvaluator(book)
	value, err := e.Evaluate(0, 1, 2)
	if err != nil || value.Display() != "6" {
		t.Fatalf("reference to spill = %q, %v", value.Display(), err)
	}
	value, err = e.Evaluate(0, 1, 3)
	if err != nil || value.Display() != "6" {
		t.Fatalf("aggregate of spill = %q, %v", value.Display(), err)
	}
	book.Sheets[0].Rows = append(book.Sheets[0].Rows, []Cell{{Value: "block"}})
	value, err = NewEvaluator(book).Evaluate(0, 1, 1)
	if err != nil || value.Err != ErrSpill {
		t.Fatalf("blocked spill = %#v, %v", value, err)
	}
}

func TestDynamicArrayEdgeCases(t *testing.T) {
	tests := []struct{ formula, want string }{
		{`=SUM(SEQUENCE(3))`, "6"},
		{`=COUNTA(SEQUENCE(3))`, "3"},
		{`=FILTER(B1:B2,B1:B2>5,"none")`, "none"},
		{`=FILTER(B1:B2,B1:B2>5)`, "#CALC!"},
		{`=UNIQUE(B1:B2,FALSE,TRUE)`, "#CALC!"},
		{`=SEQUENCE(0)`, "#VALUE!"},
	}
	for _, tt := range tests {
		book := &Spreadsheet{Sheets: []Sheet{{Name: "Sheet1", Rows: [][]Cell{
			{{Formula: tt.formula}, {Value: "1"}}, {{}, {Value: "1"}},
		}}}}
		value, err := Evaluate(book, 0, 1, 1)
		if err != nil || value.Display() != tt.want {
			t.Errorf("%s = %q, %v; want %q", tt.formula, value.Display(), err, tt.want)
		}
	}
	book := &Spreadsheet{Sheets: []Sheet{{Name: "Sheet1", Rows: make([][]Cell, MaxRows)}}}
	book.Sheets[0].Rows[MaxRows-1] = make([]Cell, MaxColumns)
	book.Sheets[0].Rows[MaxRows-1][MaxColumns-1] = Cell{Formula: "=SEQUENCE(2)"}
	value, err := Evaluate(book, 0, MaxRows, MaxColumns)
	if err != nil || value.Err != ErrSpill {
		t.Fatalf("out-of-bounds spill = %#v, %v", value, err)
	}
	cycle := &Spreadsheet{Sheets: []Sheet{{Name: "Sheet1", Rows: [][]Cell{{
		{Formula: "=SEQUENCE(B1)"}, {Formula: "=A2+2"},
	}}}}}
	if _, err := Evaluate(cycle, 0, 1, 1); err == nil || !strings.Contains(err.Error(), "circular reference") {
		t.Fatalf("spill cycle = %v", err)
	}
}

func TestOverlappingSpillsHaveStablePriority(t *testing.T) {
	book := &Spreadsheet{Sheets: []Sheet{{Name: "Sheet1", Rows: [][]Cell{
		{{}, {}, {Formula: "=SEQUENCE(3)"}},
		{{Formula: "=SEQUENCE(1,3)"}},
	}}}}
	e := NewEvaluator(book)
	winner, err := e.Evaluate(0, 2, 3)
	if err != nil || winner.Display() != "2" {
		t.Fatalf("winning spill cell: %+v, %v", winner, err)
	}
	blocked, err := e.Evaluate(0, 2, 1)
	if err != nil || blocked.Err != ErrSpill {
		t.Fatalf("blocked spill anchor: %+v, %v", blocked, err)
	}
}

func TestRecordDependencyDepth_EmptyDepths(t *testing.T) {
	e := evaluator{}
	// This should return immediately and not panic because e.depths is empty.
	e.recordDependencyDepth(5)
}

func TestAggregatesAndRanges(t *testing.T) {
	book := &Spreadsheet{
		Sheets: []Sheet{
			{
				Name: "Sheet1",
				Rows: [][]Cell{
					{{Value: "10"}, {Value: "20"}},            // A1, B1
					{{Value: "text"}, {Value: ""}},            // A2, B2
					{{Value: "30"}, {Formula: "=SUM(A1:B2)"}}, // A3, B3
				},
			},
			{
				Name: "Sheet Two",
				Rows: [][]Cell{
					{{Value: "100"}, {Value: "200"}},
				},
			},
		},
	}

	// SUM ignores non-numeric text ("text") and empty cells inside ranges
	val, err := Evaluate(book, 0, 3, 2)
	if err != nil {
		t.Fatalf("Evaluate SUM(A1:B2) failed: %v", err)
	}
	if val.Kind != NumKind || val.Num != 30 {
		t.Fatalf("expected SUM(A1:B2) = 30, got %+v", val)
	}

	// AVERAGE
	book.Sheets[0].Rows[2][1] = Cell{Formula: "=AVERAGE(A1:B1)"}
	val, err = Evaluate(book, 0, 3, 2)
	if err != nil || val.Kind != NumKind || val.Num != 15 {
		t.Fatalf("expected AVERAGE(A1:B1) = 15, got %+v (err: %v)", val, err)
	}

	// COUNT
	book.Sheets[0].Rows[2][1] = Cell{Formula: "=COUNT(A1:B2)"}
	val, err = Evaluate(book, 0, 3, 2)
	if err != nil || val.Kind != NumKind || val.Num != 2 {
		t.Fatalf("expected COUNT(A1:B2) = 2, got %+v (err: %v)", val, err)
	}

	// MIN and MAX
	book.Sheets[0].Rows[2][1] = Cell{Formula: "=MIN(A1:B2)"}
	val, err = Evaluate(book, 0, 3, 2)
	if err != nil || val.Kind != NumKind || val.Num != 10 {
		t.Fatalf("expected MIN(A1:B2) = 10, got %+v (err: %v)", val, err)
	}
	book.Sheets[0].Rows[2][1] = Cell{Formula: "=MAX(A1:B2)"}
	val, err = Evaluate(book, 0, 3, 2)
	if err != nil || val.Kind != NumKind || val.Num != 20 {
		t.Fatalf("expected MAX(A1:B2) = 20, got %+v (err: %v)", val, err)
	}

	// Direct scalar string coercion: numeric string parses
	book.Sheets[0].Rows[2][1] = Cell{Formula: `=SUM(A1, "50")`}
	val, err = Evaluate(book, 0, 3, 2)
	if err != nil || val.Kind != NumKind || val.Num != 60 {
		t.Fatalf("expected SUM(A1, \"50\") = 60, got %+v (err: %v)", val, err)
	}

	// Direct non-numeric scalar string returns #VALUE!
	book.Sheets[0].Rows[2][1] = Cell{Formula: `=SUM(A1, "invalid")`}
	val, err = Evaluate(book, 0, 3, 2)
	if err != nil || val.Kind != ErrKind || val.Err != ErrValue {
		t.Fatalf("expected SUM(A1, \"invalid\") = #VALUE!, got %+v (err: %v)", val, err)
	}

	// AVERAGE of empty range returns #DIV/0!
	book.Sheets[0].Rows[2][1] = Cell{Formula: "=AVERAGE(B2)"}
	val, err = Evaluate(book, 0, 3, 2)
	if err != nil || val.Kind != ErrKind || val.Err != ErrDiv0 {
		t.Fatalf("expected AVERAGE(B2) = #DIV/0!, got %+v (err: %v)", val, err)
	}

	// MIN / MAX of empty range returns #VALUE!
	book.Sheets[0].Rows[2][1] = Cell{Formula: "=MIN(B2)"}
	val, err = Evaluate(book, 0, 3, 2)
	if err != nil || val.Kind != ErrKind || val.Err != ErrValue {
		t.Fatalf("expected MIN(B2) = #VALUE!, got %+v (err: %v)", val, err)
	}

	// Unknown function name returns #NAME?
	book.Sheets[0].Rows[2][1] = Cell{Formula: "=UNKNOWN(A1)"}
	val, err = Evaluate(book, 0, 3, 2)
	if err != nil || val.Kind != ErrKind || val.Err != ErrName {
		t.Fatalf("expected UNKNOWN(A1) = #NAME?, got %+v (err: %v)", val, err)
	}

	// Nested function calls
	book.Sheets[0].Rows[2][1] = Cell{Formula: "=SUM(AVERAGE(A1:B1), MAX(A1:A1))"}
	val, err = Evaluate(book, 0, 3, 2)
	if err != nil || val.Kind != NumKind || val.Num != 25 {
		t.Fatalf("expected nested SUM = 25, got %+v (err: %v)", val, err)
	}

	// Cross-sheet range references
	book.Sheets[0].Rows[2][1] = Cell{Formula: `='Sheet Two'!A1:B1`}
	// A standalone range spills its values.
	val, err = Evaluate(book, 0, 3, 2)
	if err != nil || val.Kind != NumKind || val.Num != 100 {
		t.Fatalf("expected standalone range anchor = 100, got %+v (err: %v)", val, err)
	}
	val, err = Evaluate(book, 0, 3, 3)
	if err != nil || val.Kind != NumKind || val.Num != 200 {
		t.Fatalf("expected standalone range spill = 200, got %+v (err: %v)", val, err)
	}

	book.Sheets[0].Rows[2][1] = Cell{Formula: `=SUM('Sheet Two'!A1:B1)`}
	val, err = Evaluate(book, 0, 3, 2)
	if err != nil || val.Kind != NumKind || val.Num != 300 {
		t.Fatalf("expected SUM('Sheet Two'!A1:B1) = 300, got %+v (err: %v)", val, err)
	}

	// Escaped string literal: "foo""bar" -> foo"bar
	book.Sheets[0].Rows[2][1] = Cell{Formula: `="foo""bar"`}
	val, err = Evaluate(book, 0, 3, 2)
	if err != nil || val.Kind != StrKind || val.Str != `foo"bar` {
		t.Fatalf("expected escaped string foo\"bar, got %+v (err: %v)", val, err)
	}

	// Error literal propagation
	book.Sheets[0].Rows[2][1] = Cell{Formula: `=#REF!`}
	val, err = Evaluate(book, 0, 3, 2)
	if err != nil || val.Kind != ErrKind || val.Err != ErrRef {
		t.Fatalf("expected #REF! literal, got %+v (err: %v)", val, err)
	}
	book.Sheets[0].Rows[2][1] = Cell{Formula: `=1 + #DIV/0!`}
	val, err = Evaluate(book, 0, 3, 2)
	if err != nil || val.Kind != ErrKind || val.Err != ErrDiv0 {
		t.Fatalf("expected #DIV/0! propagation, got %+v (err: %v)", val, err)
	}
}

func TestRangeShifting(t *testing.T) {
	// Row deletion shrinking range
	got := shiftCellReferences("=SUM(A1:A5)", 3, 0, true)
	if got != "=SUM(A1:A4)" {
		t.Errorf("delete row 3 from A1:A5 = %q, want =SUM(A1:A4)", got)
	}

	// Row deletion at top boundary
	got = shiftCellReferences("=SUM(A1:A5)", 1, 0, true)
	if got != "=SUM(A1:A4)" {
		t.Errorf("delete row 1 from A1:A5 = %q, want =SUM(A1:A4)", got)
	}

	// Row deletion collapsing 1-row range
	got = shiftCellReferences("=SUM(A3:B3)", 3, 0, true)
	if got != "=SUM(#REF!)" {
		t.Errorf("delete row 3 from A3:B3 = %q, want =SUM(#REF!)", got)
	}

	// Col deletion shrinking range
	got = shiftCellReferences("=SUM(A1:E1)", 0, 2, true)
	if got != "=SUM(A1:D1)" {
		t.Errorf("delete col 2 from A1:E1 = %q, want =SUM(A1:D1)", got)
	}

	// Col deletion collapsing 1-col range
	got = shiftCellReferences("=SUM(B1:B5)", 0, 2, true)
	if got != "=SUM(#REF!)" {
		t.Errorf("delete col 2 from B1:B5 = %q, want =SUM(#REF!)", got)
	}

	// Row insertion expanding range
	got = shiftCellReferences("=SUM(A1:A5)", 3, 0, false)
	if got != "=SUM(A1:A6)" {
		t.Errorf("insert row 3 into A1:A5 = %q, want =SUM(A1:A6)", got)
	}

	// Col insertion expanding range
	got = shiftCellReferences("=SUM(A1:E1)", 0, 2, false)
	if got != "=SUM(A1:F1)" {
		t.Errorf("insert col 2 into A1:E1 = %q, want =SUM(A1:F1)", got)
	}
}

func TestErrorValueParsing(t *testing.T) {
	book := &Spreadsheet{Sheets: []Sheet{{Rows: [][]Cell{{{Formula: "=#REF!"}}}}}}
	val, err := Evaluate(book, 0, 1, 1)
	if err != nil || val.Kind != ErrKind || val.Err != ErrRef {
		t.Errorf("expected #REF! error value, got %+v (err: %v)", val, err)
	}
}

func TestShiftCellReferencesDeletion(t *testing.T) {
	for _, test := range []struct {
		formula    string
		fromRow    int
		fromColumn int
		want       string
	}{
		{"=A1+B2", 1, 0, "=#REF!+B1"},
		{"=A2+B2", 0, 1, "=#REF!+A2"},
		{"=C3", 2, 0, "=C2"},
		{"=C3", 0, 2, "=B3"},
	} {
		if got := shiftCellReferences(test.formula, test.fromRow, test.fromColumn, true); got != test.want {
			t.Errorf("shiftCellReferences(%q, %d, %d, true) = %q, want %q", test.formula, test.fromRow, test.fromColumn, got, test.want)
		}
	}
}
