package pdfrender

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"

	"github.com/go-pdf/fpdf"
)

// File is a font-aware PDF with independently sized pages.
type File struct {
	Font   string
	Pages  []PDFPage
	Images map[string]PDFImage
}

// PDFPage contains drawing elements positioned from the top-left in
// millimeters.
type PDFPage struct {
	WidthMM    float64
	HeightMM   float64
	Rectangles []Rectangle
	Polygons   []Polygon
	Elements   []Element
}

type Polygon struct {
	Points      []fpdf.PointType
	Color       [3]uint8
	Fill        bool
	LineWidthMM float64
}

// Rectangle is a stroked rectangle positioned in millimeters.
type Rectangle struct {
	X, Y, WidthMM, HeightMM float64
	Color                   [3]uint8
	LineWidthMM             float64
}

// Element is either positioned text or an embedded image. FontSize is in
// points; image dimensions and coordinates are in millimeters.
type Element struct {
	Text          string
	FontStyle     string
	FontFamily    string
	FontSize      float64
	ImageName     string
	X             float64
	Y             float64
	WidthMM       float64
	HeightMM      float64
	Color         [3]uint8
	HasColor      bool
	Strikethrough bool
}

// PDFImage holds a supported PDF image stream.
type PDFImage struct {
	Data   []byte
	Format string
}

// Write serializes a reusable, positioned PDF document. The selected core
// font uses the standard-14 AFM metrics for text measurement.
func Write(w io.Writer, file File) error {
	return WriteContext(context.Background(), w, file)
}

// WriteContext serializes a PDF document while honoring cancellation.
func WriteContext(ctx context.Context, w io.Writer, file File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(file.Pages) == 0 {
		return fmt.Errorf("PDF must contain at least one page")
	}
	if file.Font != "" && file.Font != "courier" && file.Font != "helvetica" && file.Font != "times" {
		return fmt.Errorf("unsupported PDF core font %q", file.Font)
	}
	for _, page := range file.Pages {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !validDimension(page.WidthMM) || !validDimension(page.HeightMM) {
			return fmt.Errorf("PDF page dimensions must be positive and finite")
		}
		for _, rect := range page.Rectangles {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !finite(rect.X) || !finite(rect.Y) || !validDimension(rect.WidthMM) ||
				!validDimension(rect.HeightMM) || !validDimension(rect.LineWidthMM) {
				return fmt.Errorf("PDF rectangle dimensions and coordinates must be finite and positive")
			}
		}
		for _, polygon := range page.Polygons {
			if len(polygon.Points) < 2 || !validDimension(polygon.LineWidthMM) {
				return fmt.Errorf("PDF polygon requires points and a positive line width")
			}
			for _, point := range polygon.Points {
				if !finite(point.X) || !finite(point.Y) {
					return fmt.Errorf("PDF polygon coordinates must be finite")
				}
			}
		}
		for _, element := range page.Elements {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !finite(element.X) || !finite(element.Y) {
				return fmt.Errorf("PDF element coordinates must be finite")
			}
			if !finite(element.FontSize) || element.FontSize < 0 {
				return fmt.Errorf("PDF font size must be finite and non-negative")
			}
			if element.FontFamily != "" && element.FontFamily != "Courier" &&
				element.FontFamily != "Helvetica" && element.FontFamily != "Times" {
				return fmt.Errorf("unsupported PDF element font %q", element.FontFamily)
			}
			if element.ImageName != "" && (!validDimension(element.WidthMM) || !validDimension(element.HeightMM)) {
				return fmt.Errorf("PDF image dimensions must be positive and finite")
			}
		}
	}
	font := pdfFont(file.Font)
	first := file.Pages[0]
	pdf := fpdf.NewCustom(&fpdf.InitType{
		OrientationStr: "P",
		UnitStr:        "mm",
		Size:           fpdf.SizeType{Wd: first.WidthMM, Ht: first.HeightMM},
	})
	pdf.SetMargins(0, 0, 0)
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetFont(font, "", printerFontSize)

	for name, img := range file.Images {
		if err := ctx.Err(); err != nil {
			return err
		}
		pdf.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: img.Format}, bytes.NewReader(img.Data))
		if err := pdf.Error(); err != nil {
			return fmt.Errorf("register image %s: %w", name, err)
		}
	}
	for _, page := range file.Pages {
		if err := ctx.Err(); err != nil {
			return err
		}
		pdf.AddPageFormat("P", fpdf.SizeType{Wd: page.WidthMM, Ht: page.HeightMM})
		if err := pdf.Error(); err != nil {
			return err
		}

		for _, rect := range page.Rectangles {
			if err := ctx.Err(); err != nil {
				return err
			}
			pdf.SetDrawColor(int(rect.Color[0]), int(rect.Color[1]), int(rect.Color[2]))
			pdf.SetLineWidth(rect.LineWidthMM)
			pdf.Rect(rect.X, rect.Y, rect.WidthMM, rect.HeightMM, "D")
		}
		for _, polygon := range page.Polygons {
			if err := ctx.Err(); err != nil {
				return err
			}
			pdf.SetDrawColor(int(polygon.Color[0]), int(polygon.Color[1]), int(polygon.Color[2]))
			pdf.SetFillColor(int(polygon.Color[0]), int(polygon.Color[1]), int(polygon.Color[2]))
			pdf.SetLineWidth(polygon.LineWidthMM)
			style := "D"
			if polygon.Fill {
				style = "F"
			}
			if len(polygon.Points) == 2 && !polygon.Fill {
				pdf.Line(polygon.Points[0].X, polygon.Points[0].Y, polygon.Points[1].X, polygon.Points[1].Y)
			} else {
				pdf.Polygon(polygon.Points, style)
			}
		}
		for _, element := range page.Elements {
			if err := ctx.Err(); err != nil {
				return err
			}
			if element.ImageName != "" {
				img, ok := file.Images[element.ImageName]
				if !ok {
					return fmt.Errorf("PDF image %q is not registered", element.ImageName)
				}
				pdf.ImageOptions(
					element.ImageName, element.X, element.Y, element.WidthMM, element.HeightMM, false,
					fpdf.ImageOptions{ImageType: img.Format}, 0, "",
				)
			} else {
				text, err := encodeWinAnsi(element.Text)
				if err != nil {
					return err
				}
				size := element.FontSize
				if size <= 0 {
					size = printerFontSize
				}
				elementFont := font
				if element.FontFamily != "" {
					elementFont = element.FontFamily
				}
				pdf.SetFont(elementFont, element.FontStyle, size)
				if element.HasColor {
					pdf.SetTextColor(int(element.Color[0]), int(element.Color[1]), int(element.Color[2]))
				} else {
					pdf.SetTextColor(0, 0, 0)
				}
				pdf.Text(element.X, element.Y, text)
				if element.Strikethrough {
					pdf.SetDrawColor(0, 0, 0)
					lineY := element.Y - size*0.09
					pdf.Line(element.X, lineY, element.X+pdf.GetStringWidth(text), lineY)
				}
			}
			if err := pdf.Error(); err != nil {
				return err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return pdf.Output(w)
}

func validDimension(value float64) bool {
	return value > 0 && finite(value)
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
