package pdfrender

import (
	"bytes"
	"compress/zlib"
	"io"
	"math"
	"strings"
	"testing"

	"example.com/parchment/internal/spreadsheet"
)

func TestRenderSpreadsheetPDF(t *testing.T) {
	book := spreadsheet.Spreadsheet{Sheets: []spreadsheet.Sheet{{Name: "Report", Rows: [][]spreadsheet.Cell{
		{{Value: "First cell"}, {}, {}, {}, {Value: "Fifth cell"}},
	}}}}
	var out bytes.Buffer
	if err := RenderSpreadsheet(&out, book); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out.Bytes(), []byte("%PDF-")) || !strings.Contains(out.String(), "/Count 2") ||
		!spreadsheetPDFContains(out.Bytes(), []byte("First cell")) || !spreadsheetPDFContains(out.Bytes(), []byte("Fifth cell")) {
		t.Fatalf("PDF missing data: header=%v pages=%v first=%v fifth=%v",
			bytes.HasPrefix(out.Bytes(), []byte("%PDF-")), strings.Contains(out.String(), "/Count 2"),
			spreadsheetPDFContains(out.Bytes(), []byte("First cell")), spreadsheetPDFContains(out.Bytes(), []byte("Fifth cell")))
	}
}

func TestSpreadsheetPDFGridlinesFollowCellsAndPageBands(t *testing.T) {
	rows := make([][]spreadsheet.Cell, 60)
	for r := range rows {
		rows[r] = make([]spreadsheet.Cell, 5)
		rows[r][0].Value = "first"
		rows[r][4].Value = "last"
	}
	rows[0][0].Value = "one\ntwo\nthree"
	book := spreadsheet.Spreadsheet{Sheets: []spreadsheet.Sheet{{Name: "Grid", Rows: rows}}}
	file, err := spreadsheetPDF(book)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Pages) != 4 {
		t.Fatalf("got %d pages, want two column bands for each row group", len(file.Pages))
	}
	for i, page := range file.Pages {
		columns := 5
		if i%2 == 1 {
			columns = 2
		}
		if len(page.Rectangles) < columns*3 || len(page.Rectangles)%columns != 0 {
			t.Fatalf("page %d has incomplete grid: %d rectangles", i+1, len(page.Rectangles))
		}
		for j, rect := range page.Rectangles {
			if rect.Color[0] < 180 || rect.Color[0] != rect.Color[1] || rect.Color[1] != rect.Color[2] ||
				rect.LineWidthMM <= 0 || rect.LineWidthMM > 0.25 {
				t.Fatalf("page %d grid is not thin and light: %+v", i+1, rect)
			}
			if rect.X < 0 || rect.Y < 0 || rect.X+rect.WidthMM > page.WidthMM || rect.Y+rect.HeightMM > page.HeightMM {
				t.Fatalf("page %d grid extends outside page: %+v", i+1, rect)
			}
			if j%columns != 0 {
				previous := page.Rectangles[j-1]
				if math.Abs(rect.X-previous.X-previous.WidthMM) > 1e-6 ||
					rect.Y != previous.Y || rect.HeightMM != previous.HeightMM {
					t.Fatalf("page %d has misaligned column borders", i+1)
				}
			}
			if j >= columns {
				above := page.Rectangles[j-columns]
				if math.Abs(rect.Y-above.Y-above.HeightMM) > 1e-6 {
					t.Fatalf("page %d has misaligned row borders", i+1)
				}
			}
		}
		if i < 2 && math.Abs(page.Rectangles[columns].HeightMM-3*lineHeightMM) > 1e-6 {
			t.Fatalf("page %d grid does not enclose all wrapped lines in one row", i+1)
		}
	}
	var out bytes.Buffer
	if err := RenderSpreadsheet(&out, book); err != nil {
		t.Fatal(err)
	}
	if !spreadsheetPDFContains(out.Bytes(), []byte(" re S")) {
		t.Fatal("PDF does not draw the cell borders")
	}
}

func spreadsheetPDFContains(pdf, text []byte) bool {
	for _, stream := range bytes.Split(pdf, []byte("stream\n"))[1:] {
		reader, err := zlib.NewReader(bytes.NewReader(stream))
		if err != nil {
			continue
		}
		content, err := io.ReadAll(reader)
		_ = reader.Close()
		if err == nil && bytes.Contains(content, text) {
			return true
		}
	}
	return false
}
