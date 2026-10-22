package tui

import (
	"testing"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/spreadsheet"
)

func BenchmarkRenderGrid(b *testing.B) {
	rows := make([][]spreadsheet.Cell, 50)
	for r := 0; r < 50; r++ {
		row := make([]spreadsheet.Cell, 20)
		for c := 0; c < 20; c++ {
			row[c] = spreadsheet.Cell{Value: "123.45"}
		}
		rows[r] = row
	}

	m := &spreadsheetModel{
		spreadsheetGrid: spreadsheetGrid{
			width:        120,
			height:       40,
			row:          0,
			column:       0,
			rowOffset:    0,
			columnOffset: 0,
		},
		loaded: true,
		book: spreadsheet.Spreadsheet{
			Artifact: artifact.Artifact{
				Title: "Bench",
			},
			Sheets: []spreadsheet.Sheet{
				{
					Name: "Sheet1",
					Rows: rows,
				},
			},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.renderGrid()
	}
}

func BenchmarkSpreadsheetView(b *testing.B) {
	rows := make([][]spreadsheet.Cell, 50)
	for r := 0; r < 50; r++ {
		row := make([]spreadsheet.Cell, 20)
		for c := 0; c < 20; c++ {
			row[c] = spreadsheet.Cell{Value: "123.45"}
		}
		rows[r] = row
	}

	m := &spreadsheetModel{
		spreadsheetGrid: spreadsheetGrid{
			width:        120,
			height:       40,
			row:          0,
			column:       0,
			rowOffset:    0,
			columnOffset: 0,
		},
		loaded: true,
		path:   "~/Documents/test.md",
		book: spreadsheet.Spreadsheet{
			Artifact: artifact.Artifact{
				Title: "Bench",
			},
			Sheets: []spreadsheet.Sheet{
				{
					Name: "Sheet1",
					Rows: rows,
				},
			},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}
