package printing

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"strings"
	"testing"

	"github.com/ramjac/parchment/internal/presentation"
	"github.com/ramjac/parchment/internal/spreadsheet"
)

type cancelDuringEmbeddedEvaluation struct {
	context.Context
	checks int
}

func (c *cancelDuringEmbeddedEvaluation) Err() error {
	c.checks++
	if c.checks >= 8 {
		return context.Canceled
	}
	return nil
}

func TestEmbeddedChartCancellationDuringWorkbookEvaluation(t *testing.T) {
	book := spreadsheet.Workbook{
		Sheets: []spreadsheet.Sheet{{Name: "Data", Rows: make([][]spreadsheet.Cell, 100)}},
		Charts: []spreadsheet.Chart{{
			ID: "chart", Type: "bar", Sheet: "Data", FirstRow: 1, LastRow: 2, FirstColumn: 1, LastColumn: 1,
		}},
	}
	ref, err := spreadsheet.FormatReference(spreadsheet.Reference{Kind: spreadsheet.ChartReferenceKind, ID: "chart"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &cancelDuringEmbeddedEvaluation{Context: context.Background()}
	called := false
	_, _, err = ExpandEmbeddedReferencesWithChartRendererContext(ctx, ref, book, func(spreadsheet.ChartData) string {
		called = true
		return "chart"
	})
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("chart expansion error = %v, renderer called = %t", err, called)
	}
}

func TestEmbeddedExpansionLimit(t *testing.T) {
	if _, _, err := ExpandEmbeddedReferencesWithChartRendererLimitedContext(
		context.Background(), "ordinary prose", spreadsheet.Workbook{}, nil, EmbeddedChartLimits{ExpandedBytes: 4},
	); !errors.Is(err, ErrEmbeddedExpansionLimit) {
		t.Fatalf("plain source exceeded the limit: %v", err)
	}
	sheet, err := spreadsheet.NewEmbeddedSheet("Data")
	if err != nil {
		t.Fatal(err)
	}
	sheet.Rows = [][]spreadsheet.Cell{{{Value: strings.Repeat("x", 200)}}}
	book := spreadsheet.Workbook{Sheets: []spreadsheet.Sheet{sheet}}
	ref, err := spreadsheet.FormatReference(spreadsheet.Reference{Kind: spreadsheet.SheetReferenceKind, ID: sheet.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{ref, ref + "\n\n" + ref} {
		if _, _, err := ExpandEmbeddedReferencesWithChartRendererLimitedContext(
			context.Background(), source, book, nil, EmbeddedChartLimits{ExpandedBytes: 150},
		); !errors.Is(err, ErrEmbeddedExpansionLimit) {
			t.Fatalf("expanded source exceeded the limit: %v", err)
		}
	}
	source := ref + "\n\n" + ref
	expanded, _, err := ExpandEmbeddedReferencesWithChartRendererLimitedContext(
		context.Background(), source, book, nil, EmbeddedChartLimits{ExpandedBytes: 1024},
	)
	if err != nil || strings.Count(expanded, strings.Repeat("x", 200)) != 2 {
		t.Fatalf("valid expansion failed: %v", err)
	}
	if _, _, err := ExpandEmbeddedReferencesWithChartRendererLimitedContext(
		context.Background(), source, book, nil, EmbeddedChartLimits{ExpandedBytes: len(expanded) - 1},
	); !errors.Is(err, ErrEmbeddedExpansionLimit) {
		t.Fatalf("cumulative expansion was not limited: %v", err)
	}
	if _, _, err := ExpandEmbeddedReferencesWithChartRendererLimitedContext(
		context.Background(), source, book, nil, EmbeddedChartLimits{ExpandedBytes: 1024, ReferenceCount: 1},
	); !errors.Is(err, ErrEmbeddedExpansionLimit) {
		t.Fatalf("embedded reference count was not limited: %v", err)
	}
	malformed := "![Spreadsheet](parchment-sheet:)\n![Spreadsheet](parchment-sheet:)"
	if _, _, err := ExpandEmbeddedReferencesWithChartRendererLimitedContext(
		context.Background(), malformed, book, nil, EmbeddedChartLimits{ExpandedBytes: 1024, ReferenceCount: 1},
	); !errors.Is(err, ErrEmbeddedExpansionLimit) {
		t.Fatalf("malformed reference count was not limited: %v", err)
	}
}

func TestEmbeddedExpansionCancelsWhileScanningLines(t *testing.T) {
	ctx := &cancelDuringEmbeddedEvaluation{Context: context.Background()}
	source := strings.Repeat("\n", 100000) + "![Spreadsheet](parchment-sheet:)\n"
	_, _, err := ExpandEmbeddedReferencesWithChartRendererLimitedContext(
		ctx, source, spreadsheet.Workbook{}, nil, EmbeddedChartLimits{ExpandedBytes: len(source) + 1024},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expansion did not cancel while scanning lines: %v", err)
	}
}

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
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	cancelled, _, err := ExpandEmbeddedReferencesWithChartRendererContext(ctx, chartRef+"\n\n"+chartRef, book, func(spreadsheet.ChartData) string {
		calls++
		cancel()
		return "native chart"
	})
	if !errors.Is(err, context.Canceled) || calls != 1 || cancelled != "" {
		t.Fatalf("cancelled expansion returned %q, %d calls, %v", cancelled, calls, err)
	}
	if _, _, _, err := ExpandEmbeddedReferencesWithImagesContext(ctx, chartRef, book); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled image expansion returned %v", err)
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

func TestExpandEmbeddedReferencesNormalizesCRLFWithoutReferences(t *testing.T) {
	source := "first\r\n\r\nsecond\r\n"
	expanded, errorsFound := ExpandEmbeddedReferences(source, spreadsheet.Workbook{})
	if expanded != "first\n\nsecond\n" || len(errorsFound) != 0 {
		t.Fatalf("expanded prose = %q, %v", expanded, errorsFound)
	}
}

type cancellingExpansionContext struct {
	context.Context
	checks int
}

func (c *cancellingExpansionContext) Err() error {
	c.checks++
	if c.checks >= 2 {
		return context.Canceled
	}
	return nil
}

func TestExpandEmbeddedReferencesFastPathCancellation(t *testing.T) {
	ctx := &cancellingExpansionContext{Context: context.Background()}
	got, _, err := ExpandEmbeddedReferencesWithChartRendererContext(ctx, "plain text", spreadsheet.Workbook{}, nil)
	if got != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled fast path returned %q, %v", got, err)
	}
}

func TestEmbeddedReferencesIgnoreFencedCode(t *testing.T) {
	source := "![Spreadsheet](parchment-sheet:outside)\n\n```md\n![Chart](parchment-chart:inside)\n```"
	references := EmbeddedReferences(source)
	if len(references) != 1 || references[0].ID != "outside" {
		t.Fatalf("embedded references = %+v", references)
	}
}
