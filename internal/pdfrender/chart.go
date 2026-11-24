package pdfrender

import (
	"context"
	"fmt"
	"io"
	"math"

	"github.com/go-pdf/fpdf"

	"github.com/ramjac/parchment/internal/chartrender"
	"github.com/ramjac/parchment/internal/document"
	"github.com/ramjac/parchment/internal/spreadsheet"
)

func chartPage(book *spreadsheet.Spreadsheet, chart spreadsheet.Chart) (PDFPage, error) {
	data, err := spreadsheet.EvaluateChart(book, chart)
	if err != nil {
		return PDFPage{}, err
	}
	scene, err := chartrender.Layout(data)
	if err != nil {
		return PDFPage{}, err
	}
	width, height, err := pageSizeMM(document.DefaultLayout())
	if err != nil {
		return PDFPage{}, err
	}
	page := PDFPage{WidthMM: width, HeightMM: height}
	scale := math.Min((width-20)/scene.Width, (height-20)/scene.Height)
	for _, shape := range scene.Shapes {
		polygon := Polygon{Color: [3]uint8{shape.Color.R, shape.Color.G, shape.Color.B}, Fill: shape.Fill, LineWidthMM: max(0.1, shape.Width*scale)}
		for _, point := range shape.Points {
			polygon.Points = append(polygon.Points, fpdf.PointType{X: 10 + point.X*scale, Y: 10 + point.Y*scale})
		}
		page.Polygons = append(page.Polygons, polygon)
	}
	for _, text := range scene.Text {
		if err := validateWinAnsi(text.Text); err != nil {
			return PDFPage{}, fmt.Errorf("chart label: %w", err)
		}
		page.Elements = append(page.Elements, Element{Text: text.Text, X: 10 + text.X*scale, Y: 10 + text.Y*scale, FontSize: 10})
	}
	return page, nil
}

func RenderChartContext(ctx context.Context, w io.Writer, book spreadsheet.Spreadsheet, chart spreadsheet.Chart) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	page, err := chartPage(&book, chart)
	if err != nil {
		return err
	}
	return WriteContext(ctx, w, File{Font: "helvetica", Pages: []PDFPage{page}})
}
