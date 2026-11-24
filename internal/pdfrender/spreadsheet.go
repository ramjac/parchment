package pdfrender

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"strings"

	"github.com/ramjac/parchment/internal/chartrender"
	"github.com/ramjac/parchment/internal/document"
	"github.com/ramjac/parchment/internal/printing"
	"github.com/ramjac/parchment/internal/spreadsheet"
)

// RenderSpreadsheet writes the same row-major page bands as text printing.
func RenderSpreadsheet(w io.Writer, book spreadsheet.Spreadsheet) error {
	return RenderSpreadsheetContext(context.Background(), w, book)
}

// RenderSpreadsheetContext renders workbook pages while honoring cancellation.
func RenderSpreadsheetContext(ctx context.Context, w io.Writer, book spreadsheet.Spreadsheet) error {
	file, err := spreadsheetPDFContext(ctx, book)
	if err != nil {
		return err
	}
	return WriteContext(ctx, w, file)
}

func spreadsheetPDF(book spreadsheet.Spreadsheet) (File, error) {
	return spreadsheetPDFContext(context.Background(), book)
}

func spreadsheetPDFContext(ctx context.Context, book spreadsheet.Spreadsheet) (File, error) {
	pages, err := printing.SpreadsheetPagesContext(ctx, book)
	if err != nil {
		return File{}, err
	}
	width, height, err := pageSizeMM(document.DefaultLayout())
	if err != nil {
		return File{}, err
	}
	file := File{Font: "courier", Images: make(map[string]PDFImage)}
	chartImages := make(map[string]image.Image)
	for _, sheetPage := range pages {
		if err := ctx.Err(); err != nil {
			return File{}, err
		}
		page := PDFPage{WidthMM: width, HeightMM: height}
		// Center borders in the existing inter-column and inter-line spacing.
		y := (float64(sheetPage.GridTopRow) + 0.2) * lineHeightMM
		coveredCells := make(map[[2]int]bool)
		for _, span := range sheetPage.Spans {
			for row := span.FirstRow; row <= span.LastRow; row++ {
				for col := span.FirstColumn; col <= span.LastColumn; col++ {
					coveredCells[[2]int{row, col}] = true
				}
			}
		}
		for rowIndex, rows := range sheetPage.RowHeights {
			x := (float64(sheetPage.GridLeftCol) - 0.5) * 2.54
			for colIndex, cols := range sheetPage.ColumnWidths {
				covered := rowIndex > 0 && colIndex > 0 && coveredCells[[2]int{rowIndex - 1, colIndex - 1}]
				if !covered {
					page.Rectangles = append(page.Rectangles, Rectangle{
						X: x, Y: y, WidthMM: float64(cols) * 2.54, HeightMM: float64(rows) * lineHeightMM,
						Color: [3]uint8{200, 200, 200}, LineWidthMM: 0.25,
					})
				}
				x += float64(cols) * 2.54
			}
			y += float64(rows) * lineHeightMM
		}
		for _, span := range sheetPage.Spans {
			x := (float64(sheetPage.GridLeftCol) - 0.5) * 2.54
			y := (float64(sheetPage.GridTopRow) + 0.2) * lineHeightMM
			for _, cols := range sheetPage.ColumnWidths[:span.FirstColumn+1] {
				x += float64(cols) * 2.54
			}
			for _, rows := range sheetPage.RowHeights[:span.FirstRow+1] {
				y += float64(rows) * lineHeightMM
			}
			var widthCols, heightRows int
			for _, cols := range sheetPage.ColumnWidths[span.FirstColumn+1 : span.LastColumn+2] {
				widthCols += cols
			}
			for _, rows := range sheetPage.RowHeights[span.FirstRow+1 : span.LastRow+2] {
				heightRows += rows
			}
			page.Rectangles = append(page.Rectangles, Rectangle{
				X: x, Y: y, WidthMM: float64(widthCols) * 2.54, HeightMM: float64(heightRows) * lineHeightMM,
				Color: [3]uint8{200, 200, 200}, LineWidthMM: 0.25,
			})
		}
		for row, line := range sheetPage.Lines {
			line = strings.TrimRight(line, " ")
			if line != "" {
				page.Elements = append(page.Elements, Element{
					Text: line, X: 0, Y: float64(row+1) * lineHeightMM, FontSize: printerFontSize,
				})
			}
		}
		for _, chart := range book.Charts {
			sheet := book.Sheets[sheetPage.SheetIndex]
			if !strings.EqualFold(chart.PlaceSheet, sheet.Name) {
				continue
			}
			span := spreadsheet.CellSpan{FirstRow: chart.PlaceRow, LastRow: chart.PlaceRow, FirstColumn: chart.PlaceColumn, LastColumn: chart.PlaceColumn}
			if s, ok := spreadsheet.SpanAt(sheet, chart.PlaceRow, chart.PlaceColumn); ok {
				span = s
			}
			r0, r1 := max(span.FirstRow-1, sheetPage.FirstRow), min(span.LastRow, sheetPage.FirstRow+len(sheetPage.RowHeights)-1)
			c0, c1 := max(span.FirstColumn-1, sheetPage.FirstColumn), min(span.LastColumn, sheetPage.FirstColumn+len(sheetPage.ColumnWidths)-1)
			if r0 >= r1 || c0 >= c1 {
				continue
			}
			img, ok := chartImages[chart.ID]
			if !ok {
				data, evalErr := spreadsheet.EvaluateChart(&book, chart)
				if evalErr != nil {
					continue
				}
				scene, layoutErr := chartrender.Layout(data)
				if layoutErr != nil {
					continue
				}
				img = chartrender.Raster(scene)
				chartImages[chart.ID] = img
			}
			fullHeight, topHeight, partHeight := 0, 0, 0
			for row := span.FirstRow - 1; row < span.LastRow; row++ {
				h := sheetPage.AllRowHeights[row]
				fullHeight += h
				if row < r0 {
					topHeight += h
				} else if row < r1 {
					partHeight += h
				}
			}
			bounds := img.Bounds()
			widthPixels, heightPixels := bounds.Dx(), bounds.Dy()
			columns := span.LastColumn - span.FirstColumn + 1
			x0 := (c0 - (span.FirstColumn - 1)) * widthPixels / columns
			x1 := (c1 - (span.FirstColumn - 1)) * widthPixels / columns
			y0 := topHeight * heightPixels / fullHeight
			y1 := (topHeight + partHeight) * heightPixels / fullHeight
			if x1 <= x0 || y1 <= y0 {
				continue
			}
			part := image.NewRGBA(image.Rect(0, 0, x1-x0, y1-y0))
			draw.Draw(part, part.Bounds(), img, image.Pt(bounds.Min.X+x0, bounds.Min.Y+y0), draw.Src)
			var encoded bytes.Buffer
			if err := png.Encode(&encoded, part); err != nil {
				return File{}, err
			}
			name := fmt.Sprintf("chart-%s-%d", chart.ID, len(file.Pages))
			file.Images[name] = PDFImage{Data: encoded.Bytes(), Format: "PNG"}
			x := (float64(sheetPage.GridLeftCol) - 0.5 + float64(sheetPage.ColumnWidths[0])) * 2.54
			for c := sheetPage.FirstColumn; c < c0; c++ {
				x += float64(sheetPage.ColumnWidths[c-sheetPage.FirstColumn+1]) * 2.54
			}
			y := (float64(sheetPage.GridTopRow) + 0.2 + float64(sheetPage.RowHeights[0])) * lineHeightMM
			for r := sheetPage.FirstRow; r < r0; r++ {
				y += float64(sheetPage.AllRowHeights[r]) * lineHeightMM
			}
			var columnsWidth int
			for c := c0; c < c1; c++ {
				columnsWidth += sheetPage.ColumnWidths[c-sheetPage.FirstColumn+1]
			}
			page.Elements = append(page.Elements, Element{ImageName: name, X: x, Y: y,
				WidthMM: float64(columnsWidth) * 2.54, HeightMM: float64(partHeight) * lineHeightMM})
		}
		file.Pages = append(file.Pages, page)
	}
	return file, nil
}
