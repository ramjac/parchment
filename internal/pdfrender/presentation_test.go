package pdfrender

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"strings"
	"testing"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/presentation"
)

func TestPresentationPDFPagesNotesAndImages(t *testing.T) {
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 180, 120))); err != nil {
		t.Fatal(err)
	}
	img, err := artifact.NewImage(pngData.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	item := presentation.Presentation{
		Source: "# Title\n\nPresenter\n\n## First\n\n.image " + img.Name + "\n\n: A speaker note.\n\n## Second\n\nOther content.",
		Images: []artifact.Image{img},
		Font:   "helvetica",
	}
	file, err := presentationPDF(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Pages) != 3 {
		t.Fatalf("pages = %d, want title and two slides", len(file.Pages))
	}
	for _, page := range file.Pages {
		if page.WidthMM <= page.HeightMM {
			t.Fatalf("page is not landscape: %v x %v", page.WidthMM, page.HeightMM)
		}
	}
	var imageBottom, notesTop float64
	for _, element := range file.Pages[1].Elements {
		if element.ImageName == img.Name {
			imageBottom = element.Y + element.HeightMM
		}
		if strings.Contains(element.Text, "Speaker") {
			notesTop = element.Y - element.FontSize*25.4/72
		}
	}
	if imageBottom < 70 || notesTop < file.Pages[1].HeightMM/2 || imageBottom >= notesTop {
		t.Fatalf("image bottom %f and notes top %f overlap or are misplaced", imageBottom, notesTop)
	}
	var out bytes.Buffer
	if err := RenderPresentation(&out, item); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out.Bytes(), []byte("%PDF-")) || !strings.Contains(out.String(), "/Count 3") ||
		!pdfStreamContains(out.Bytes(), []byte("A speaker note.")) {
		t.Fatal("PDF is missing pages or speaker note content")
	}
}

func TestPresentationPDFRejectsOversizedNotes(t *testing.T) {
	item := presentation.Presentation{Source: "# Deck\n\n## Slide\n\nBody\n\n" + strings.Repeat(": An unusually long speaker note line.\n", 500)}
	var out bytes.Buffer
	if err := RenderPresentation(&out, item); err == nil || !strings.Contains(err.Error(), "fit on one PDF page") {
		t.Fatalf("oversized notes error = %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("failed render wrote a partial PDF")
	}
}

func TestPresentationTwoUpPDF(t *testing.T) {
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 180, 120))); err != nil {
		t.Fatal(err)
	}
	img, err := artifact.NewImage(pngData.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	item := presentation.Presentation{
		Source: "# Deck\n\n## First\n\n.image " + img.Name + "\n\n: First reminder\n\n## Second\n\nClosing\n\n: Second reminder\n",
		Images: []artifact.Image{img},
	}
	file, err := presentationTwoUpPDF(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Pages) != 2 || len(file.Pages[0].Rectangles) != 2 || len(file.Pages[1].Rectangles) != 1 {
		t.Fatalf("unexpected page/box counts: %+v", file.Pages)
	}
	for _, page := range file.Pages {
		if page.WidthMM >= page.HeightMM {
			t.Fatalf("page is not portrait: %.2f x %.2f", page.WidthMM, page.HeightMM)
		}
	}
	first := file.Pages[0].Rectangles[1]
	second := file.Pages[1].Rectangles[0]
	if first.Color[0] < 180 || first.LineWidthMM > 0.5 {
		t.Fatal("slide box is not light")
	}
	for _, tc := range []struct {
		page    PDFPage
		box     Rectangle
		note    string
		noteEnd float64
	}{
		{file.Pages[0], first, "First reminder", file.Pages[0].HeightMM - presentationMarginMM},
		{file.Pages[1], second, "Second reminder", file.Pages[1].HeightMM / 2},
	} {
		found := false
		for _, element := range tc.page.Elements {
			if strings.Contains(element.Text, tc.note) {
				found = true
				if element.Y <= tc.box.Y+tc.box.HeightMM || element.Y >= tc.noteEnd {
					t.Fatalf("note %q is not below its box: %+v", tc.note, element)
				}
			}
			if element.ImageName == img.Name &&
				(element.X < tc.box.X || element.Y < tc.box.Y || element.X+element.WidthMM > tc.box.X+tc.box.WidthMM ||
					element.Y+element.HeightMM > tc.box.Y+tc.box.HeightMM) {
				t.Fatalf("image is outside its slide box: %+v", element)
			}
		}
		if !found {
			t.Fatalf("missing note %q", tc.note)
		}
	}
	var out bytes.Buffer
	if err := RenderPresentationTwoUp(&out, item); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out.Bytes(), []byte("%PDF-")) || !strings.Contains(out.String(), "/Count 2") ||
		!pdfStreamContains(out.Bytes(), []byte(" re ")) {
		t.Fatal("PDF missing two pages or drawn slide boxes")
	}
}

func TestPresentationTwoUpPDFRejectsOverflow(t *testing.T) {
	for _, source := range []string{
		"# Deck\n\n## Slide\n\n" + strings.Repeat("Long paragraph.\n\n", 300),
		"# Deck\n\n## Slide\n\nBody\n\n" + strings.Repeat(": Excessive notes\n", 300),
	} {
		var out bytes.Buffer
		if err := RenderPresentationTwoUp(&out, presentation.Presentation{Source: source}); err == nil || out.Len() != 0 {
			t.Fatalf("overflow produced output or no error: %v", err)
		}
	}
}
