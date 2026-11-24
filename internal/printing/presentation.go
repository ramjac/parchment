package printing

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/ramjac/parchment/internal/artifact"
	"github.com/ramjac/parchment/internal/document"
	"github.com/ramjac/parchment/internal/presentation"
)

// PresentationSlides prepares the title page and slides for printing.
func PresentationSlides(item presentation.Presentation) ([]presentation.Slide, error) {
	return PresentationSlidesContext(context.Background(), item)
}

// PresentationSlidesContext resolves slide content while honoring cancellation.
func PresentationSlidesContext(ctx context.Context, item presentation.Presentation) ([]presentation.Slide, error) {
	slides, _, err := presentationSlidesContext(ctx, item, false)
	return slides, err
}

// PresentationSlidesWithImagesContext resolves chart references to generated
// image assets for graphical PDF output.
func PresentationSlidesWithImagesContext(
	ctx context.Context,
	item presentation.Presentation,
) ([]presentation.Slide, []artifact.Image, error) {
	return presentationSlidesContext(ctx, item, true)
}

func presentationSlidesContext(
	ctx context.Context,
	item presentation.Presentation,
	chartsAsImages bool,
) ([]presentation.Slide, []artifact.Image, error) {
	deck, err := presentation.Parse(item.Source)
	if err != nil {
		return nil, nil, err
	}
	slides := make([]presentation.Slide, 0, len(deck.Slides)+1)
	slides = append(slides, presentation.Slide{Title: deck.Title, Body: deck.Header})
	slides = append(slides, deck.Slides...)
	var images []artifact.Image
	baseDir := ""
	if item.Path != "" {
		baseDir = filepath.Dir(item.Path)
	}
	for i := range slides {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		body, err := presentation.ExpandDirectivesContext(ctx, slides[i].Body, baseDir, item.Images)
		if err != nil {
			return nil, nil, fmt.Errorf("slide %d: %w", i, err)
		}
		var referenceErrors []error
		var bodyImages []artifact.Image
		if chartsAsImages {
			body, bodyImages, referenceErrors = ExpandEmbeddedReferencesWithImages(body, item.Workbook)
		} else {
			body, referenceErrors = ExpandEmbeddedReferences(body, item.Workbook)
		}
		if len(referenceErrors) > 0 {
			return nil, nil, fmt.Errorf("slide %d: %w", i, referenceErrors[0])
		}
		notes := slides[i].Notes
		var notesImages []artifact.Image
		if chartsAsImages {
			notes, notesImages, referenceErrors = ExpandEmbeddedReferencesWithImages(notes, item.Workbook)
		} else {
			notes, referenceErrors = ExpandEmbeddedReferences(notes, item.Workbook)
		}
		if len(referenceErrors) > 0 {
			return nil, nil, fmt.Errorf("slide %d speaker notes: %w", i, referenceErrors[0])
		}
		slides[i].Body, slides[i].Notes = body, notes
		images = append(images, bodyImages...)
		images = append(images, notesImages...)
	}
	return slides, images, nil
}

// PrintPresentationTwoUp renders portrait text pages with two framed slides
// and their speaker notes per page.
func PrintPresentationTwoUp(item presentation.Presentation) (string, error) {
	slides, err := PresentationSlides(item)
	if err != nil {
		return "", err
	}
	layout := document.DefaultLayout()
	layout.PageNumbers = document.NumbersNone
	geometry, err := layout.Geometry()
	if err != nil {
		return "", err
	}
	inner := layout
	inner.Margins.Left += 3
	inner.Margins.Right += 3
	innerGeometry, err := inner.Geometry()
	if err != nil {
		return "", err
	}
	slotRows := (geometry.BodyRows() - 1) / 2
	boxRows := slotRows * 2 / 3
	pad := strings.Repeat(" ", geometry.LeftCols)
	border := pad + "+" + strings.Repeat("-", geometry.TextCols-2) + "+"
	var pages []string
	var rows []string
	for i, slide := range slides {
		if i%2 == 0 {
			rows = make([]string, geometry.PageRows)
		}
		content, err := presentationTextLines(inner, "# "+slide.Title+"\n\n"+slide.Body)
		if err != nil {
			return "", fmt.Errorf("slide %d: %w", i, err)
		}
		if len(content) > boxRows-2 {
			return "", fmt.Errorf("slide %d does not fit in its text box", i)
		}
		var notes []string
		if slide.Notes != "" {
			notes, err = presentationTextLines(layout, "Speaker notes:\n\n"+slide.Notes)
			if err != nil {
				return "", fmt.Errorf("slide %d speaker notes: %w", i, err)
			}
			if len(notes) > slotRows-boxRows-1 {
				return "", fmt.Errorf("slide %d speaker notes do not fit below its text box", i)
			}
		}
		top := geometry.TopRows + (i%2)*(slotRows+1)
		rows[top], rows[top+boxRows-1] = border, border
		for row := 0; row < boxRows-2; row++ {
			text := ""
			if row < len(content) {
				text = strings.TrimPrefix(content[row], strings.Repeat(" ", innerGeometry.LeftCols))
			}
			rows[top+1+row] = pad + "|" + text + strings.Repeat(" ", max(0, geometry.TextCols-2-runewidth.StringWidth(text))) + "|"
		}
		copy(rows[top+boxRows+1:], notes)
		if i%2 == 1 || i == len(slides)-1 {
			pages = append(pages, strings.Join(rows, "\n")+"\n")
		}
	}
	return strings.Join(pages, "\f"), nil
}

// PrintPresentation renders one landscape text page per slide, including
// speaker notes at the bottom. Oversized slides return an error.
func PrintPresentation(item presentation.Presentation) (string, error) {
	slides, err := PresentationSlides(item)
	if err != nil {
		return "", err
	}
	layout := document.DefaultLayout()
	layout.Orientation = document.Landscape
	layout.PageNumbers = document.NumbersNone
	geometry, err := layout.Geometry()
	if err != nil {
		return "", err
	}
	pages := make([]string, 0, len(slides))
	for i, slide := range slides {
		content, err := presentationTextLines(layout, "# "+slide.Title+"\n\n"+slide.Body)
		if err != nil {
			return "", fmt.Errorf("slide %d: %w", i, err)
		}
		var notes []string
		if slide.Notes != "" {
			notes, err = presentationTextLines(layout, "Speaker notes:\n\n"+slide.Notes)
			if err != nil {
				return "", fmt.Errorf("slide %d speaker notes: %w", i, err)
			}
		}
		gap := 0
		if len(notes) > 0 {
			gap = 1
		}
		if len(content)+gap+len(notes) > geometry.BodyRows() {
			return "", fmt.Errorf("slide %d and its speaker notes do not fit on one text page", i)
		}
		rows := make([]string, geometry.PageRows)
		copy(rows[geometry.TopRows:], content)
		copy(rows[geometry.TopRows+geometry.BodyRows()-len(notes):], notes)
		pages = append(pages, strings.Join(rows, "\n")+"\n")
	}
	return strings.Join(pages, "\f"), nil
}

func presentationTextLines(layout document.Layout, body string) ([]string, error) {
	d := document.Document{
		Artifact: artifact.Artifact{Kind: artifact.PresentationKind},
		Body:     body, Layout: layout,
	}
	pages, err := document.Paginate(d)
	if err != nil {
		return nil, err
	}
	if len(pages) != 1 {
		return nil, fmt.Errorf("content does not fit on one text page")
	}
	g, err := layout.Geometry()
	if err != nil {
		return nil, err
	}
	lines := pages[0].Lines[g.TopRows : g.TopRows+g.BodyRows()]
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end], nil
}
