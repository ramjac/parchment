package pdfrender

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/go-pdf/fpdf"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/printing"
)

const (
	presentationMarginMM = 15.0
	presentationNotesGap = 5.0
	presentationMinScale = 0.5
)

// RenderPresentation writes a landscape PDF with a title page and one page
// per slide, placing speaker notes at the bottom of their slide's page.
func RenderPresentation(w io.Writer, item presentation.Presentation) error {
	return RenderPresentationContext(context.Background(), w, item)
}

// RenderPresentationContext renders slides while honoring cancellation.
func RenderPresentationContext(ctx context.Context, w io.Writer, item presentation.Presentation) error {
	file, err := presentationPDF(ctx, item)
	if err != nil {
		return err
	}
	return WriteContext(ctx, w, file)
}

// RenderPresentationTwoUp writes portrait pages with two framed slides and
// their speaker notes per page, including the title page as the first slide.
func RenderPresentationTwoUp(w io.Writer, item presentation.Presentation) error {
	return RenderPresentationTwoUpContext(context.Background(), w, item)
}

// RenderPresentationTwoUpContext renders two-up slides while honoring cancellation.
func RenderPresentationTwoUpContext(ctx context.Context, w io.Writer, item presentation.Presentation) error {
	file, err := presentationTwoUpPDF(ctx, item)
	if err != nil {
		return err
	}
	return WriteContext(ctx, w, file)
}

func presentationTwoUpPDF(ctx context.Context, item presentation.Presentation) (File, error) {
	slides, embeddedImages, err := printing.PresentationSlidesWithImagesContext(ctx, item)
	if err != nil {
		return File{}, err
	}
	if item.Font != "" && item.Font != "courier" && item.Font != "helvetica" && item.Font != "times" {
		return File{}, fmt.Errorf("unsupported PDF core font %q", item.Font)
	}
	width, height, err := pageSizeMM(document.DefaultLayout())
	if err != nil {
		return File{}, err
	}
	assets, err := documentImages(document.Document{Images: append(item.Images, embeddedImages...)})
	if err != nil {
		return File{}, err
	}
	images := make(map[string]PDFImage, len(assets))
	for name, asset := range assets {
		images[name] = PDFImage{Data: asset.data, Format: asset.format}
	}
	file := File{Font: item.Font, Images: images}
	measure := newMeasurePDF(width, height, pdfFont(item.Font))
	const gap, padding = 6.0, 3.0
	slotHeight := (height - 2*presentationMarginMM - gap) / 2
	boxHeight := slotHeight * 0.67
	boxWidth := min(width-2*presentationMarginMM, boxHeight*16/9)
	boxHeight = boxWidth * 9 / 16
	boxX := (width - boxWidth) / 2
	var refs []imageRef
	for i, slide := range slides {
		if err := ctx.Err(); err != nil {
			return File{}, err
		}
		if i%2 == 0 {
			file.Pages = append(file.Pages, PDFPage{WidthMM: width, HeightMM: height})
		}
		page := &file.Pages[len(file.Pages)-1]
		boxY := presentationMarginMM + float64(i%2)*(slotHeight+gap)
		page.Rectangles = append(page.Rectangles, Rectangle{
			X: boxX, Y: boxY, WidthMM: boxWidth, HeightMM: boxHeight,
			Color: [3]uint8{200, 200, 200}, LineWidthMM: 0.25,
		})
		content, contentHeight, err := presentationSection(
			ctx, "# "+slide.Title+"\n\n"+slide.Body, boxWidth-2*padding, item.Font, measure, assets, &refs,
		)
		if err != nil {
			return File{}, fmt.Errorf("slide %d: %w", i, err)
		}
		contentScale := 1.0
		if contentHeight > 0 {
			contentScale = min(1, (boxHeight-2*padding)/contentHeight)
		}
		if contentScale < presentationMinScale {
			return File{}, fmt.Errorf("slide %d does not fit in its PDF box", i)
		}
		page.Elements = append(page.Elements, positionPresentationElements(content, boxX+padding, boxY+padding, contentScale)...)
		if slide.Notes != "" {
			notes, notesHeight, err := presentationSection(
				ctx, "## Speaker notes\n\n"+slide.Notes, width-2*presentationMarginMM, item.Font, measure, assets, &refs,
			)
			if err != nil {
				return File{}, fmt.Errorf("slide %d speaker notes: %w", i, err)
			}
			available := slotHeight - boxHeight - presentationNotesGap
			notesScale := min(1, available/notesHeight)
			if notesScale < presentationMinScale {
				return File{}, fmt.Errorf("slide %d speaker notes do not fit below its PDF box", i)
			}
			page.Elements = append(page.Elements, positionPresentationElements(notes, presentationMarginMM, boxY+boxHeight+presentationNotesGap, notesScale)...)
		}
	}
	return file, nil
}

func presentationPDF(ctx context.Context, item presentation.Presentation) (File, error) {
	slides, embeddedImages, err := printing.PresentationSlidesWithImagesContext(ctx, item)
	if err != nil {
		return File{}, err
	}
	if item.Font != "" && item.Font != "courier" && item.Font != "helvetica" && item.Font != "times" {
		return File{}, fmt.Errorf("unsupported PDF core font %q", item.Font)
	}
	layout := document.DefaultLayout()
	layout.Orientation = document.Landscape
	width, height, err := pageSizeMM(layout)
	if err != nil {
		return File{}, err
	}
	assets, err := documentImages(document.Document{Images: append(item.Images, embeddedImages...)})
	if err != nil {
		return File{}, err
	}
	images := make(map[string]PDFImage, len(assets))
	for name, asset := range assets {
		images[name] = PDFImage{Data: asset.data, Format: asset.format}
	}
	file := File{Font: item.Font, Images: images}
	measure := newMeasurePDF(width, height, pdfFont(item.Font))
	textWidth := width - 2*presentationMarginMM
	pageHeight := height - 2*presentationMarginMM
	var refs []imageRef
	for i, slide := range slides {
		if err := ctx.Err(); err != nil {
			return File{}, err
		}
		content, contentHeight, err := presentationSection(
			ctx, "# "+slide.Title+"\n\n"+slide.Body, textWidth, item.Font, measure, assets, &refs,
		)
		if err != nil {
			return File{}, fmt.Errorf("slide %d: %w", i, err)
		}
		var notes []Element
		var notesHeight float64
		if slide.Notes != "" {
			notes, notesHeight, err = presentationSection(
				ctx, "## Speaker notes\n\n"+slide.Notes, textWidth, item.Font, measure, assets, &refs,
			)
			if err != nil {
				return File{}, fmt.Errorf("slide %d speaker notes: %w", i, err)
			}
		}
		notesScale, contentSpace := 1.0, pageHeight
		if notesHeight > 0 {
			notesScale = min(1, pageHeight*0.4/notesHeight)
			contentSpace -= notesHeight*notesScale + presentationNotesGap
		}
		contentScale := 1.0
		if contentHeight > 0 {
			contentScale = min(1, contentSpace/contentHeight)
		}
		if contentScale < presentationMinScale || notesScale < presentationMinScale {
			return File{}, fmt.Errorf("slide %d and its speaker notes do not fit on one PDF page", i)
		}
		page := PDFPage{WidthMM: width, HeightMM: height}
		page.Elements = append(page.Elements, positionPresentationElements(content, presentationMarginMM, presentationMarginMM, contentScale)...)
		if notesHeight > 0 {
			top := height - presentationMarginMM - notesHeight*notesScale
			page.Elements = append(page.Elements, positionPresentationElements(notes, presentationMarginMM, top, notesScale)...)
		}
		file.Pages = append(file.Pages, page)
	}
	return file, nil
}

func presentationSection(ctx context.Context, body string, width float64, font string, measure *fpdf.Fpdf, assets map[string]imageAsset, refs *[]imageRef) ([]Element, float64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	formatter := markdownFormatter(assets, refs, measure, font, width)
	lines := formatter(strings.Split(body, "\n"), int(width/2.54), 1, body)
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	var elements []Element
	for i, line := range lines {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		row, err := renderRichLine(measure, line, 0, float64(i+1)*lineHeightMM, width, font, *refs, assets, 10000, 0)
		if err != nil {
			return nil, 0, err
		}
		elements = append(elements, row...)
	}
	return elements, float64(len(lines)) * lineHeightMM, nil
}

func positionPresentationElements(elements []Element, x, y, scale float64) []Element {
	result := make([]Element, len(elements))
	for i, element := range elements {
		element.X = x + element.X*scale
		element.Y = y + element.Y*scale
		element.WidthMM *= scale
		element.HeightMM *= scale
		element.FontSize *= scale
		result[i] = element
	}
	return result
}
