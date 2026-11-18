package pdfrender

import (
	"io"
	"strings"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/printing"
	"example.com/parchment/internal/spreadsheet"
)

// RenderSpreadsheet writes the same row-major page bands as text printing.
func RenderSpreadsheet(w io.Writer, book spreadsheet.Spreadsheet) error {
	file, err := spreadsheetPDF(book)
	if err != nil {
		return err
	}
	return Write(w, file)
}

func spreadsheetPDF(book spreadsheet.Spreadsheet) (File, error) {
	pages, err := printing.SpreadsheetPages(book)
	if err != nil {
		return File{}, err
	}
	width, height, err := pageSizeMM(document.DefaultLayout())
	if err != nil {
		return File{}, err
	}
	file := File{Font: "courier"}
	for _, sheetPage := range pages {
		page := PDFPage{WidthMM: width, HeightMM: height}
		// Center borders in the existing inter-column and inter-line spacing.
		y := (float64(sheetPage.GridTopRow) + 0.2) * lineHeightMM
		for _, rows := range sheetPage.RowHeights {
			x := (float64(sheetPage.GridLeftCol) - 0.5) * 2.54
			for _, cols := range sheetPage.ColumnWidths {
				page.Rectangles = append(page.Rectangles, Rectangle{
					X: x, Y: y, WidthMM: float64(cols) * 2.54, HeightMM: float64(rows) * lineHeightMM,
					Color: [3]uint8{200, 200, 200}, LineWidthMM: 0.25,
				})
				x += float64(cols) * 2.54
			}
			y += float64(rows) * lineHeightMM
		}
		for row, line := range sheetPage.Lines {
			line = strings.TrimRight(line, " ")
			if line != "" {
				page.Elements = append(page.Elements, Element{
					Text: line, X: 0, Y: float64(row+1) * lineHeightMM, FontSize: printerFontSize,
				})
			}
		}
		file.Pages = append(file.Pages, page)
	}
	return file, nil
}
