// Package pdfrender renders documents with the PDF standard-14 fonts.
package pdfrender

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"strings"

	"github.com/go-pdf/fpdf"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
)

const (
	printerFontSize = 12.0
	lineHeightMM    = 25.4 / 6
	maxImageWidthMM = 25.4
	maxBlockImageMM = 76.2
	maxImagePixels  = 16_000_000
)

type imageRef struct {
	name  string
	block bool
}

type imageAsset struct {
	data   []byte
	format string
	width  int
	height int
}

// Render writes a styled, paginated PDF for d. The document paginator keeps
// page breaks, columns, headers, footers, and page numbers consistent with
// text printing while the PDF formatter preserves Markdown presentation.
func Render(w io.Writer, d document.Document) error {
	if err := d.Layout.Validate(); err != nil {
		return err
	}
	pageWidth, pageHeight, err := pageSizeMM(d.Layout)
	if err != nil {
		return err
	}
	geometry, err := d.Layout.Geometry()
	if err != nil {
		return err
	}
	font := pdfFont(d.Layout.Font)
	measure := newMeasurePDF(pageWidth, pageHeight, font)

	assets, err := documentImages(d)
	if err != nil {
		return err
	}
	var refs []imageRef
	textWidth := pageWidth - float64(d.Layout.Margins.Left+d.Layout.Margins.Right)
	measure.SetFont(font, "", printerFontSize)
	gutterWidth := measure.GetStringWidth(strings.Repeat(" ", document.ColumnGutter))
	pages, err := document.PaginateWithFormatter(d, markdownFormatter(assets, &refs, measure, d.Layout.Font, textWidth))
	if err != nil {
		return err
	}
	images := make(map[string]PDFImage, len(assets))
	for name, asset := range assets {
		images[name] = PDFImage{Data: asset.data, Format: asset.format}
	}
	leftPadding := strings.Repeat(" ", geometry.LeftCols)
	file := File{Font: d.Layout.Font, Images: images}

	for _, page := range pages {
		pdfPage := PDFPage{WidthMM: pageWidth, HeightMM: pageHeight}
		for row, line := range page.Lines {
			baseline := float64(row+1) * lineHeightMM
			if row == geometry.TopRows && d.Layout.Header != "" {
				measure.SetFont(font, "", printerFontSize)
				header := expandRunningLine(d.Layout.Header, d, page.Number, len(pages))
				if err := validateWinAnsi(header); err != nil {
					return err
				}
				elements, err := drawRunningLine(measure, header, float64(d.Layout.Margins.Left), textWidth, baseline)
				if err != nil {
					return err
				}
				pdfPage.Elements = append(pdfPage.Elements, elements...)
				continue
			}
			footerStart := geometry.PageRows - geometry.BottomRows - geometry.FooterRows
			footerRow := footerStart + 1
			if d.Layout.Footer != "" {
				if row == footerRow {
					measure.SetFont(font, "", printerFontSize)
					footer := expandRunningLine(d.Layout.Footer, d, page.Number, len(pages))
					if err := validateWinAnsi(footer); err != nil {
						return err
					}
					elements, err := drawRunningLine(measure, footer, float64(d.Layout.Margins.Left), textWidth, baseline)
					if err != nil {
						return err
					}
					pdfPage.Elements = append(pdfPage.Elements, elements...)
					continue
				}
				footerRow++
			}
			numbered := d.Layout.PageNumbers != document.NumbersNone && d.Layout.PageNumbers != ""
			if numbered && row == footerRow {
				measure.SetFont(font, "", printerFontSize)
				text := fmt.Sprintf("Page %d of %d", page.Number, len(pages))
				if err := validateWinAnsi(text); err != nil {
					return err
				}
				element, err := drawAlignedText(measure, text, d.Layout.PageNumbers, float64(d.Layout.Margins.Left), textWidth, baseline)
				if err != nil {
					return err
				}
				pdfPage.Elements = append(pdfPage.Elements, element)
				continue
			}
			if row < len(page.Cells) && len(page.Cells[row]) > 0 {
				cells := page.Cells[row]
				cellWidth := (textWidth - float64(len(cells)-1)*gutterWidth) / float64(len(cells))
				for column, cell := range cells {
					elements, err := renderRichLine(
						measure, cell,
						float64(d.Layout.Margins.Left)+float64(column)*(cellWidth+gutterWidth),
						baseline, cellWidth, d.Layout.Font, refs, assets,
						pageHeight, float64(d.Layout.Margins.Bottom),
					)
					if err != nil {
						return err
					}
					pdfPage.Elements = append(pdfPage.Elements, elements...)
				}
				continue
			}
			line = strings.TrimPrefix(line, leftPadding)
			elements, err := renderRichLine(
				measure, line, float64(d.Layout.Margins.Left), baseline, textWidth, d.Layout.Font,
				refs, assets, pageHeight, float64(d.Layout.Margins.Bottom),
			)
			if err != nil {
				return err
			}
			pdfPage.Elements = append(pdfPage.Elements, elements...)
		}
		file.Pages = append(file.Pages, pdfPage)
	}
	return Write(w, file)
}

func expandRunningLine(spec string, d document.Document, page, total int) string {
	title := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, d.Title)
	return strings.NewReplacer(
		"{title}", title,
		"{page}", fmt.Sprint(page),
		"{pages}", fmt.Sprint(total),
	).Replace(spec)
}

func drawRunningLine(pdf *fpdf.Fpdf, text string, x, width, baseline float64) ([]Element, error) {
	parts := strings.SplitN(text, "|", 3)
	var left, center, right string
	switch len(parts) {
	case 1:
		left = parts[0]
	case 2:
		left, right = parts[0], parts[1]
	default:
		left, center, right = parts[0], parts[1], parts[2]
	}

	left, err := fitText(pdf, left, width)
	if err != nil {
		return nil, err
	}
	elements := make([]Element, 0, 3)
	if left != "" {
		elements = append(elements, Element{Text: left, X: x, Y: baseline, FontSize: printerFontSize})
	}
	leftEnd, err := winAnsiWidth(pdf, left)
	if err != nil {
		return nil, err
	}

	right, err = fitText(pdf, right, max(width-leftEnd, 0))
	if err != nil {
		return nil, err
	}
	rightWidth, err := winAnsiWidth(pdf, right)
	if err != nil {
		return nil, err
	}
	if right != "" {
		elements = append(elements, Element{Text: right, X: x + width - rightWidth, Y: baseline, FontSize: printerFontSize})
	}

	centerWidth := max(width-leftEnd-rightWidth, 0)
	center, err = fitText(pdf, center, centerWidth)
	if err != nil {
		return nil, err
	}
	centerWidthMM, err := winAnsiWidth(pdf, center)
	if err != nil {
		return nil, err
	}
	centerX := x + (width-centerWidthMM)/2
	if centerX < x+leftEnd {
		centerX = x + leftEnd
	}
	if centerX+centerWidthMM > x+width-rightWidth {
		centerX = x + width - rightWidth - centerWidthMM
	}
	if center != "" {
		elements = append(elements, Element{Text: center, X: centerX, Y: baseline, FontSize: printerFontSize})
	}
	return elements, nil
}

func drawAlignedText(pdf *fpdf.Fpdf, text, alignment string, x, width, baseline float64) (Element, error) {
	var err error
	text, err = fitText(pdf, text, width)
	if err != nil {
		return Element{}, err
	}
	textWidth, err := winAnsiWidth(pdf, text)
	if err != nil {
		return Element{}, err
	}
	switch alignment {
	case document.NumbersCenter:
		x += (width - textWidth) / 2
	case document.NumbersRight:
		x += width - textWidth
	}
	return Element{Text: text, X: x, Y: baseline, FontSize: printerFontSize}, nil
}

func fitText(pdf *fpdf.Fpdf, text string, width float64) (string, error) {
	textWidth, err := winAnsiWidth(pdf, text)
	if err != nil {
		return "", err
	}
	if textWidth <= width {
		return text, nil
	}
	var result strings.Builder
	for _, r := range text {
		next := result.String() + string(r)
		nextWidth, err := winAnsiWidth(pdf, next)
		if err != nil {
			return "", err
		}
		if nextWidth > width {
			break
		}
		result.WriteRune(r)
	}
	return result.String(), nil
}

func validateWinAnsi(text string) error {
	_, err := encodeWinAnsi(text)
	return err
}

func winAnsiWidth(pdf *fpdf.Fpdf, text string) (float64, error) {
	encoded, err := encodeWinAnsi(text)
	if err != nil {
		return 0, err
	}
	return pdf.GetStringWidth(encoded), nil
}

func encodeWinAnsi(text string) (string, error) {
	encoded := make([]byte, 0, len(text))
	for _, r := range text {
		switch {
		case r >= 0x20 && r <= 0x7e, r >= 0xa0 && r <= 0xff:
			encoded = append(encoded, byte(r))
		default:
			value, ok := winAnsiSpecial[r]
			if !ok {
				return "", fmt.Errorf("character %U is not supported by PDF core fonts", r)
			}
			encoded = append(encoded, value)
		}
	}
	return string(encoded), nil
}

var winAnsiSpecial = map[rune]byte{
	0x20ac: 0x80, 0x201a: 0x82, 0x0192: 0x83, 0x201e: 0x84, 0x2026: 0x85,
	0x2020: 0x86, 0x2021: 0x87, 0x02c6: 0x88, 0x2030: 0x89, 0x0160: 0x8a,
	0x2039: 0x8b, 0x0152: 0x8c, 0x017d: 0x8e, 0x2018: 0x91, 0x2019: 0x92,
	0x201c: 0x93, 0x201d: 0x94, 0x2022: 0x95, 0x2013: 0x96, 0x2014: 0x97,
	0x02dc: 0x98, 0x2122: 0x99, 0x0161: 0x9a, 0x203a: 0x9b, 0x0153: 0x9c,
	0x017e: 0x9e, 0x0178: 0x9f,
}

func winAnsiRune(r rune) bool {
	if r >= 0x20 && r <= 0x7e || r >= 0xa0 && r <= 0xff {
		return true
	}
	_, ok := winAnsiSpecial[r]
	return ok
}

func inlineImageSize(asset imageAsset) (float64, float64) {
	height := lineHeightMM * 0.9
	width := height * float64(asset.width) / float64(asset.height)
	if width > maxImageWidthMM {
		width = maxImageWidthMM
		height = width * float64(asset.height) / float64(asset.width)
	}
	return width, height
}

func documentImages(d document.Document) (map[string]imageAsset, error) {
	images := make(map[string]artifact.Image, len(d.Images))
	for _, img := range d.Images {
		images[img.Name] = img
	}
	assets := make(map[string]imageAsset, len(images))
	for name, img := range images {
		asset, err := prepareImage(img.Data)
		if err != nil {
			return nil, fmt.Errorf("prepare image %s: %w", name, err)
		}
		assets[name] = asset
	}
	return assets, nil
}

func prepareImage(data []byte) (imageAsset, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return imageAsset{}, err
	}
	if err := validateImageDimensions(config); err != nil {
		return imageAsset{}, err
	}
	if format == "jpeg" {
		return imageAsset{data: data, format: "JPG", width: config.Width, height: config.Height}, nil
	}
	if format != "png" && format != "gif" {
		return imageAsset{}, fmt.Errorf("unsupported image format %q", format)
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return imageAsset{}, err
	}
	bounds := decoded.Bounds()
	rgba := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(rgba, rgba.Bounds(), decoded, bounds.Min, draw.Src)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, rgba); err != nil {
		return imageAsset{}, err
	}
	return imageAsset{
		data: encoded.Bytes(), format: "PNG", width: bounds.Dx(), height: bounds.Dy(),
	}, nil
}

func validateImageDimensions(config image.Config) error {
	if config.Width <= 0 || config.Height <= 0 {
		return fmt.Errorf("invalid image dimensions %dx%d", config.Width, config.Height)
	}
	width, height := uint64(config.Width), uint64(config.Height)
	if width > maxImagePixels || height > maxImagePixels || width*height > maxImagePixels {
		return fmt.Errorf("image dimensions %dx%d exceed the PDF limit of %d pixels", config.Width, config.Height, maxImagePixels)
	}
	return nil
}

func pageSizeMM(layout document.Layout) (float64, float64, error) {
	var width, height float64
	switch layout.PageSize {
	case document.PageLetter:
		width, height = 215.9, 279.4
	case document.PageA4:
		width, height = 210, 297
	case document.PageLegal:
		width, height = 215.9, 355.6
	default:
		return 0, 0, fmt.Errorf("unsupported page size %q", layout.PageSize)
	}
	if layout.Orientation == document.Landscape {
		width, height = height, width
	}
	return width, height, nil
}

func newMeasurePDF(width, height float64, font string) *fpdf.Fpdf {
	pdf := fpdf.NewCustom(&fpdf.InitType{
		OrientationStr: "P",
		UnitStr:        "mm",
		Size:           fpdf.SizeType{Wd: width, Ht: height},
	})
	pdf.SetFont(pdfFont(font), "", printerFontSize)
	return pdf
}

func pdfFont(font string) string {
	switch font {
	case "helvetica":
		return "Helvetica"
	case "times":
		return "Times"
	default:
		return "Courier"
	}
}
