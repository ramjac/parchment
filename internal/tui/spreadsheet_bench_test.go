package tui

import (
	"testing"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/spreadsheet"
)

func BenchmarkSpreadsheetView(b *testing.B) {
	m := &spreadsheetModel{
		path:   "benchmark.md",
		loaded: true,
		book: spreadsheet.Spreadsheet{
			Artifact: artifact.Artifact{
				Title: "Benchmark Sheet",
			},
			Sheets: []spreadsheet.Sheet{
				{
					Name: "Sheet1",
					Rows: [][]spreadsheet.Cell{
						{{Value: "A1"}, {Value: "B1"}, {Value: "C1"}},
						{{Value: "A2"}, {Value: "B2"}, {Value: "C2"}},
					},
				},
			},
		},
		spreadsheetGrid: spreadsheetGrid{
			width:        120,
			height:       30,
			columnOffset: 0,
			rowOffset:    0,
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}
