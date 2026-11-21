package printing

import (
	"bytes"
	"image/png"
	"strings"
	"testing"

	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/spreadsheet"
)

func TestExpandEmbeddedReferencesResolvesSheetAndCrossSheetChart(t *testing.T) {
	sales, err := spreadsheet.NewEmbeddedSheet("Sales")
	if err != nil {
		t.Fatal(err)
	}
	charts, err := spreadsheet.NewEmbeddedSheet("Charts")
	if err != nil {
		t.Fatal(err)
	}
	book := spreadsheet.Workbook{Sheets: []spreadsheet.Sheet{sales, charts}}
	if err := book.SetCell(spreadsheet.SetCellOptions{
		Sheet: "Sales", Row: 1, Column: 1, Cell: spreadsheet.Cell{Value: "North"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCell(spreadsheet.SetCellOptions{
		Sheet: "Sales", Row: 1, Column: 2, Cell: spreadsheet.Cell{Value: "3"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCell(spreadsheet.SetCellOptions{
		Sheet: "Sales", Row: 2, Column: 1, Cell: spreadsheet.Cell{Value: "South"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCell(spreadsheet.SetCellOptions{
		Sheet: "Sales", Row: 2, Column: 2, Cell: spreadsheet.Cell{Value: "5"},
	}); err != nil {
		t.Fatal(err)
	}
	chart, err := book.AddChart(spreadsheet.Chart{
		ID: "chart-1", Type: "bar", Sheet: "Sales", FirstRow: 1, LastRow: 2,
		FirstColumn: 1, LastColumn: 2, Labels: true,
		PlaceSheet: "Charts", PlaceRow: 1, PlaceColumn: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	sheetRef, _ := spreadsheet.FormatReference(spreadsheet.Reference{Kind: spreadsheet.SheetReferenceKind, ID: sales.ID})
	chartRef, _ := spreadsheet.FormatReference(spreadsheet.Reference{Kind: spreadsheet.ChartReferenceKind, ID: chart.ID})
	source := sheetRef + "\n\n```\n" + chartRef + "\n```\n\n" + chartRef
	expanded, errorsFound := ExpandEmbeddedReferences(source, book)
	if len(errorsFound) != 0 {
		t.Fatalf("expand returned errors: %v", errorsFound)
	}
	if strings.Count(expanded, "**Spreadsheet: Sales**") != 1 || strings.Count(expanded, "**bar chart (bar chart)**") != 1 {
		t.Fatalf("expanded references =\n%s", expanded)
	}
	if !strings.Contains(expanded, chartRef) {
		t.Fatal("reference inside a code fence was interpreted")
	}
	deck, err := PresentationSlides(presentation.Presentation{
		Source:   "# Deck\n\n## Slide\n\n" + chartRef + "\n\n: " + sheetRef,
		Workbook: book,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(deck) != 2 || !strings.Contains(deck[1].Body, "**bar chart") ||
		strings.Contains(deck[1].Body, "**Spreadsheet: Sales**") ||
		!strings.Contains(deck[1].Notes, "**Spreadsheet: Sales**") {
		t.Fatalf("slide body and notes were not resolved independently: %+v", deck)
	}
	graphicalDeck, chartImages, err := PresentationSlidesWithImagesContext(t.Context(), presentation.Presentation{
		Source:   "# Deck\n\n## Slide\n\n" + chartRef + "\n\n: " + sheetRef,
		Workbook: book,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(graphicalDeck) != 2 || len(chartImages) != 1 ||
		!strings.Contains(graphicalDeck[1].Body, chartImages[0].Name) ||
		!strings.Contains(graphicalDeck[1].Notes, "**Spreadsheet: Sales**") {
		t.Fatalf("graphical slide and notes references = %+v, %+v", graphicalDeck, chartImages)
	}
	if _, err := png.DecodeConfig(bytes.NewReader(chartImages[0].Data)); err != nil {
		t.Fatalf("presentation chart image is not a valid PNG: %v", err)
	}
	imageMarkdown, chartImages, imageErrors := ExpandEmbeddedReferencesWithImages(chartRef, book)
	if len(imageErrors) > 0 || len(chartImages) != 1 || !strings.Contains(imageMarkdown, chartImages[0].Name) {
		t.Fatalf("chart image expansion = %q, %+v, %v", imageMarkdown, chartImages, imageErrors)
	}
	if _, err := png.DecodeConfig(bytes.NewReader(chartImages[0].Data)); err != nil {
		t.Fatalf("chart image is not a valid PNG: %v", err)
	}
	renderedCharts := 0
	custom, customErrors := ExpandEmbeddedReferencesWithChartRenderer(source, book, func(data spreadsheet.ChartData) string {
		renderedCharts++
		if len(data.Labels) != 2 || data.Labels[0] != "North" || data.Series[0].Values[1] != 5 {
			t.Fatalf("custom renderer did not receive evaluated chart data: %+v", data)
		}
		return "native chart"
	})
	if len(customErrors) != 0 || renderedCharts != 1 ||
		!strings.Contains(custom, "native chart") || !strings.Contains(custom, chartRef) ||
		!strings.Contains(custom, "**Spreadsheet: Sales**") {
		t.Fatalf("custom rendering did not preserve reference boundaries: %q, %v", custom, customErrors)
	}
}

func TestExpandEmbeddedReferencesKeepsBrokenReferencePlaceholder(t *testing.T) {
	source := "![Spreadsheet](parchment-sheet:missing)"
	expanded, errorsFound := ExpandEmbeddedReferences(source, spreadsheet.Workbook{})
	if len(errorsFound) != 1 || !strings.Contains(expanded, "Embedded content unavailable") {
		t.Fatalf("broken reference expansion = %q, %v", expanded, errorsFound)
	}
}

func TestExpandEmbeddedReferencesLeavesOrdinaryProseUntouched(t *testing.T) {
	source := "The text parchment-sheet:missing is documentation, not a reference."
	expanded, errorsFound := ExpandEmbeddedReferences(source, spreadsheet.Workbook{})
	if expanded != source || len(errorsFound) != 0 {
		t.Fatalf("ordinary prose = %q, %v", expanded, errorsFound)
	}
}

func TestEmbeddedReferencesIgnoreFencedCode(t *testing.T) {
	source := "![Spreadsheet](parchment-sheet:outside)\n\n```md\n![Chart](parchment-chart:inside)\n```"
	references := EmbeddedReferences(source)
	if len(references) != 1 || references[0].ID != "outside" {
		t.Fatalf("embedded references = %+v", references)
	}
}
