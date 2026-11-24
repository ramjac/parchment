package spreadsheet_test

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/ramjac/parchment/internal/spreadsheet"
)

func loadMortgageBudget(tb testing.TB) spreadsheet.Spreadsheet {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join("..", "tui", "testdata", "mortgage-budget.md"))
	if err != nil {
		tb.Fatal(err)
	}
	book, err := spreadsheet.Decode(data)
	if err != nil {
		tb.Fatal(err)
	}
	return book
}

func TestMortgageBudgetWorkload(t *testing.T) {
	view := loadMortgageBudget(t)
	if len(view.Sheets) != 5 || len(view.Sheets[0].Rows) != 365 {
		t.Fatalf("unexpected workbook shape: %d sheets", len(view.Sheets))
	}
	for _, check := range []struct {
		sheet, row, column int
		want               float64
	}{
		{0, 6, 4, 1500},
		{0, 365, 6, 0},
		{1, 2, 2, 200*12*1000 + 12*10*(200*201/2) + 200*(12*13/2)},
	} {
		value, err := spreadsheet.Evaluate(&view, check.sheet, check.row, check.column)
		if err != nil {
			t.Fatal(err)
		}
		got, err := value.Float64()
		if err != nil || math.Abs(got-check.want) > 0.01 {
			t.Fatalf("sheet %d cell %s = %v, %v; want %v", check.sheet, spreadsheet.CellName(check.row, check.column), got, err, check.want)
		}
	}
	view.Sheets[0].Rows[0][1].Value = "400000"
	value, err := spreadsheet.Evaluate(&view, 0, 6, 4)
	if err != nil || value.Num != 2000 {
		t.Fatalf("interest after principal change = %v, %v; want 2000", value, err)
	}
	view.Sheets[2].Rows[1][1].Value = "1018"
	value, err = spreadsheet.Evaluate(&view, 1, 2, 2)
	want := float64(200*12*1000 + 12*10*(200*201/2) + 200*(12*13/2) + 7)
	if err != nil || value.Num != want {
		t.Fatalf("rollup after budget change = %v, %v; want %v", value, err, want)
	}
	view.Sheets[2].Rows[1][13].Formula = "=N2"
	if _, err := spreadsheet.Evaluate(&view, 1, 2, 2); err == nil {
		t.Fatal("circular budget formula did not produce an error")
	}
}

func BenchmarkMortgageBudget(b *testing.B) {
	book := loadMortgageBudget(b)
	for _, target := range []struct {
		name               string
		sheet, row, column int
	}{
		{"MortgageLastBalance", 0, 365, 6},
		{"DepartmentRollup", 1, 2, 2},
	} {
		b.Run(target.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := spreadsheet.Evaluate(&book, target.sheet, target.row, target.column); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Run("PasteAndRecalculate", func(b *testing.B) {
		clipboard, err := spreadsheet.CopyRange(book, "Department1", 2, 2, 51, 13)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			current := loadMortgageBudget(b)
			b.StartTimer()
			if err := spreadsheet.PasteRange(&current, "Department2", 2, 2, clipboard); err != nil {
				b.Fatal(err)
			}
			if _, err := spreadsheet.Evaluate(&current, 1, 3, 2); err != nil {
				b.Fatal(err)
			}
			b.StopTimer()
			if current.Sheets[3].Rows[1][1] != book.Sheets[2].Rows[1][1] {
				b.Fatal("pasted budget input did not match source")
			}
			b.StartTimer()
		}
	})
}
