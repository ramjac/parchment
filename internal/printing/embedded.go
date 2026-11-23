package printing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"math"
	"strconv"
	"strings"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/chartrender"
	"example.com/parchment/internal/spreadsheet"
)

const (
	embeddedPreviewRows    = 24
	embeddedPreviewColumns = 8
)

var ErrEmbeddedChartLimit = errors.New("embedded chart limit exceeded")
var ErrEmbeddedExpansionLimit = errors.New("embedded content expansion limit exceeded")

type EmbeddedChartLimits struct {
	Count          int
	Bytes          int
	ExpandedBytes  int
	ReferenceCount int
}

// EmbeddedReferences returns block-level embedded object references outside
// fenced code blocks, preserving source order.
func EmbeddedReferences(source string) []spreadsheet.Reference {
	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
	var references []spreadsheet.Reference
	fence := ""
	for _, line := range lines {
		if marker := markdownFence(line); marker != "" {
			if fence == "" {
				fence = marker
			} else if marker[0] == fence[0] && len(marker) >= len(fence) &&
				strings.TrimSpace(strings.TrimLeft(line, " \t")[len(marker):]) == "" {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if reference, ok := spreadsheet.ParseReference(line); ok {
			references = append(references, reference)
		}
	}
	return references
}

// ExpandEmbeddedReferences replaces standalone sheet and chart references with
// bounded Markdown previews. Fenced code blocks are left untouched. Errors are
// returned alongside actionable placeholders so interactive views can render
// broken references while exports can reject them.
func ExpandEmbeddedReferences(source string, book spreadsheet.Workbook) (string, []error) {
	expanded, _, errorsFound, _ := expandEmbeddedReferences(context.Background(), source, book, false, nil, nil)
	return expanded, errorsFound
}

// ExpandEmbeddedReferencesWithImages replaces chart references with generated
// PNG assets while expanding sheet references as bounded Markdown tables.
func ExpandEmbeddedReferencesWithImages(source string, book spreadsheet.Workbook) (string, []artifact.Image, []error) {
	expanded, images, errorsFound, _ := ExpandEmbeddedReferencesWithImagesContext(context.Background(), source, book)
	return expanded, images, errorsFound
}

// ExpandEmbeddedReferencesWithImagesContext stops between references when cancelled.
func ExpandEmbeddedReferencesWithImagesContext(ctx context.Context, source string, book spreadsheet.Workbook) (string, []artifact.Image, []error, error) {
	return expandEmbeddedReferences(ctx, source, book, true, nil, nil)
}

// ExpandEmbeddedReferencesWithImagesLimitedContext limits retained chart images.
func ExpandEmbeddedReferencesWithImagesLimitedContext(ctx context.Context, source string, book spreadsheet.Workbook, limits EmbeddedChartLimits) (string, []artifact.Image, []error, error) {
	return expandEmbeddedReferences(ctx, source, book, true, nil, &limits)
}

// ExpandEmbeddedReferencesWithChartRenderer uses render for evaluated charts
// while retaining the same reference parsing and sheet previews as other output.
func ExpandEmbeddedReferencesWithChartRenderer(source string, book spreadsheet.Workbook, render func(spreadsheet.ChartData) string) (string, []error) {
	expanded, errorsFound, _ := ExpandEmbeddedReferencesWithChartRendererContext(context.Background(), source, book, render)
	return expanded, errorsFound
}

// ExpandEmbeddedReferencesWithChartRendererContext stops between references when cancelled.
func ExpandEmbeddedReferencesWithChartRendererContext(ctx context.Context, source string, book spreadsheet.Workbook, render func(spreadsheet.ChartData) string) (string, []error, error) {
	expanded, _, errorsFound, err := expandEmbeddedReferences(ctx, source, book, false, render, nil)
	return expanded, errorsFound, err
}

// ExpandEmbeddedReferencesWithChartRendererLimitedContext also limits expanded Markdown.
func ExpandEmbeddedReferencesWithChartRendererLimitedContext(ctx context.Context, source string, book spreadsheet.Workbook, render func(spreadsheet.ChartData) string, limits EmbeddedChartLimits) (string, []error, error) {
	expanded, _, errorsFound, err := expandEmbeddedReferences(ctx, source, book, false, render, &limits)
	return expanded, errorsFound, err
}

func expandEmbeddedReferences(ctx context.Context, source string, book spreadsheet.Workbook, chartsAsImages bool, render func(spreadsheet.ChartData) string, limits *EmbeddedChartLimits) (string, []artifact.Image, []error, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, nil, err
	}
	if !strings.Contains(source, "parchment-sheet:") && !strings.Contains(source, "parchment-chart:") &&
		!strings.Contains(source, "\r\n") {
		if err := ctx.Err(); err != nil {
			return "", nil, nil, err
		}
		if limits != nil && limits.ExpandedBytes > 0 && len(source) > limits.ExpandedBytes {
			return "", nil, nil, ErrEmbeddedExpansionLimit
		}
		return source, nil, nil, nil
	}
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	expandedBytes := len(normalized)
	if limits != nil && limits.ExpandedBytes > 0 && expandedBytes > limits.ExpandedBytes {
		return "", nil, nil, ErrEmbeddedExpansionLimit
	}
	var errorsFound []error
	var images []artifact.Image
	imageBytes := 0
	referenceCount := 0
	fence := ""
	processLine := func(i int, line string) (string, error) {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if marker := markdownFence(line); marker != "" {
			if fence == "" {
				fence = marker
			} else if marker[0] == fence[0] && len(marker) >= len(fence) &&
				strings.TrimSpace(strings.TrimLeft(line, " \t")[len(marker):]) == "" {
				fence = ""
			}
			return line, nil
		}
		if fence != "" {
			return line, nil
		}
		reference, ok := spreadsheet.ParseReference(line)
		if !ok {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "![Spreadsheet](") && strings.Contains(trimmed, "parchment-sheet:") ||
				strings.HasPrefix(trimmed, "![Chart](") && strings.Contains(trimmed, "parchment-chart:") {
				referenceCount++
				if limits != nil && limits.ReferenceCount > 0 && referenceCount > limits.ReferenceCount {
					return "", fmt.Errorf("%w: too many embedded references", ErrEmbeddedExpansionLimit)
				}
				err := fmt.Errorf("line %d: malformed embedded spreadsheet reference", i+1)
				errorsFound = append(errorsFound, err)
				preview := "> **Embedded spreadsheet reference unavailable:** " + err.Error()
				if limits != nil && limits.ExpandedBytes > 0 && len(preview) > limits.ExpandedBytes-expandedBytes+len(line) {
					return "", ErrEmbeddedExpansionLimit
				}
				expandedBytes += len(preview) - len(line)
				return preview, nil
			}
			return line, nil
		}
		referenceCount++
		if limits != nil && limits.ReferenceCount > 0 && referenceCount > limits.ReferenceCount {
			return "", fmt.Errorf("%w: too many embedded references", ErrEmbeddedExpansionLimit)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var preview string
		var err error
		previewLimit := -1
		if limits != nil && limits.ExpandedBytes > 0 {
			previewLimit = limits.ExpandedBytes - expandedBytes + len(line)
		}
		switch reference.Kind {
		case spreadsheet.SheetReferenceKind:
			preview, err = embeddedSheetMarkdown(ctx, book, reference.ID, previewLimit)
		case spreadsheet.ChartReferenceKind:
			if render != nil {
				var data spreadsheet.ChartData
				_, data, err = embeddedChartData(ctx, book, reference.ID)
				if err == nil {
					preview = render(data)
				}
			} else if chartsAsImages {
				if limits != nil && len(images) >= limits.Count {
					return "", fmt.Errorf("%w: too many charts", ErrEmbeddedChartLimit)
				}
				var img artifact.Image
				preview, img, err = embeddedChartImageMarkdown(ctx, book, reference.ID)
				if err == nil {
					if limits != nil && len(img.Data) > limits.Bytes-imageBytes {
						return "", fmt.Errorf("%w: chart images exceed %d bytes", ErrEmbeddedChartLimit, limits.Bytes)
					}
					images = append(images, img)
					imageBytes += len(img.Data)
				}
			} else {
				preview, err = embeddedChartMarkdown(ctx, book, reference.ID)
			}
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if errors.Is(err, ErrEmbeddedExpansionLimit) {
			return "", err
		}
		if err != nil {
			errorsFound = append(errorsFound, fmt.Errorf("line %d: %w", i+1, err))
			preview = "> **Embedded content unavailable:** " + markdownText(err.Error())
		}
		if limits != nil && limits.ExpandedBytes > 0 && len(preview) > previewLimit {
			return "", ErrEmbeddedExpansionLimit
		}
		expandedBytes += len(preview) - len(line)
		return preview, nil
	}
	var output strings.Builder
	remaining := normalized
	for i := 0; ; i++ {
		line, rest, more := strings.Cut(remaining, "\n")
		expanded, err := processLine(i, line)
		if err != nil {
			return "", nil, nil, err
		}
		output.WriteString(expanded)
		if !more {
			break
		}
		output.WriteByte('\n')
		remaining = rest
	}
	if err := ctx.Err(); err != nil {
		return "", nil, nil, err
	}
	return output.String(), images, errorsFound, nil
}

type boundedEmbeddedText struct {
	strings.Builder
	limit int
	err   error
}

func (w *boundedEmbeddedText) Write(data []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.limit >= 0 && len(data) > w.limit-w.Len() {
		w.err = ErrEmbeddedExpansionLimit
		return 0, w.err
	}
	return w.Builder.Write(data)
}

func (w *boundedEmbeddedText) WriteString(data string) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.limit >= 0 && len(data) > w.limit-w.Len() {
		w.err = ErrEmbeddedExpansionLimit
		return 0, w.err
	}
	return w.Builder.WriteString(data)
}

func embeddedSheetMarkdown(ctx context.Context, book spreadsheet.Workbook, id string, limit int) (string, error) {
	index, err := spreadsheet.SheetIndexByID(book, id)
	if err != nil {
		return "", err
	}
	sheet := book.Sheets[index]
	lastRow, lastColumn := len(sheet.Rows), 1
	for i, row := range sheet.Rows {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		lastColumn = max(lastColumn, len(row))
	}
	for i, span := range sheet.Spans {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		lastRow = max(lastRow, span.LastRow)
		lastColumn = max(lastColumn, span.LastColumn)
	}
	for _, chart := range book.Charts {
		if strings.EqualFold(chart.PlaceSheet, sheet.Name) {
			lastRow = max(lastRow, chart.PlaceRow)
			lastColumn = max(lastColumn, chart.PlaceColumn)
		}
	}
	lastRow, lastColumn = max(lastRow, 1), max(lastColumn, 1)
	evaluator := spreadsheet.NewWorkbookEvaluatorContext(ctx, &book)
	spills, err := evaluator.SpillCells(index)
	if err != nil {
		return "", err
	}
	for cell := range spills {
		lastRow, lastColumn = max(lastRow, cell[0]), max(lastColumn, cell[1])
	}
	rows, columns := min(lastRow, embeddedPreviewRows), min(lastColumn, embeddedPreviewColumns)
	output := boundedEmbeddedText{limit: limit}
	fmt.Fprintf(&output, "**Spreadsheet: %s**\n\n| Row |", markdownText(sheet.Name))
	for column := 1; column <= columns; column++ {
		name := strings.TrimRight(spreadsheet.CellName(1, column), "0123456789")
		fmt.Fprintf(&output, " %s |", name)
	}
	output.WriteString("\n| --- |")
	for column := 0; column < columns; column++ {
		output.WriteString(" --- |")
	}
	for row := 1; row <= rows; row++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		fmt.Fprintf(&output, "\n| %d |", row)
		for column := 1; column <= columns; column++ {
			value, valueErr := embeddedCellValue(book, evaluator, spills, index, sheet.Name, row, column)
			if valueErr != nil {
				return "", valueErr
			}
			if output.err != nil || limit >= 0 && len(value) > limit-output.Len() {
				return "", ErrEmbeddedExpansionLimit
			}
			fmt.Fprintf(&output, " %s |", markdownCell(value))
		}
	}
	if rows < lastRow || columns < lastColumn {
		fmt.Fprintf(&output, "\n\n_Showing %d of %d rows and %d of %d columns._",
			rows, lastRow, columns, lastColumn)
	}
	if output.err != nil {
		return "", output.err
	}
	return output.String(), nil
}

func embeddedCellValue(book spreadsheet.Workbook, evaluator *spreadsheet.Evaluator, spills map[[2]int]spreadsheet.Value, sheetIndex int, sheet string, row, column int) (string, error) {
	if chart, ok := spreadsheet.ChartAtInWorkbook(&book, sheet, row, column); ok {
		title := chart.Title
		if title == "" {
			title = chart.Type + " chart"
		}
		return "[Chart: " + title + "]", nil
	}
	if span, ok := spreadsheet.SpanAt(book.Sheets[sheetIndex], row, column); ok &&
		(span.FirstRow != row || span.FirstColumn != column) {
		return "", nil
	}
	cell := spreadsheet.Cell{}
	if row <= len(book.Sheets[sheetIndex].Rows) && column <= len(book.Sheets[sheetIndex].Rows[row-1]) {
		cell = book.Sheets[sheetIndex].Rows[row-1][column-1]
	}
	if cell.Formula != "" || spills[[2]int{row, column}].Kind != spreadsheet.EmptyKind {
		value, err := evaluator.Evaluate(sheetIndex, row, column)
		if err != nil {
			return "", err
		}
		return value.Display(), nil
	}
	return cell.Value, nil
}

func embeddedChartMarkdown(ctx context.Context, book spreadsheet.Workbook, id string) (string, error) {
	_, data, err := embeddedChartData(ctx, book, id)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	title := data.Title
	if title == "" {
		title = data.Type + " chart"
	}
	fmt.Fprintf(&output, "**%s (%s chart)**\n\n| Category |", markdownText(title), markdownText(data.Type))
	for _, series := range data.Series {
		fmt.Fprintf(&output, " %s |", markdownCell(series.Name))
	}
	output.WriteString("\n| --- |")
	for range data.Series {
		output.WriteString(" --- |")
	}
	maxValue := 0.0
	for _, series := range data.Series {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		for _, value := range series.Values {
			maxValue = max(maxValue, math.Abs(value))
		}
	}
	for row, label := range data.Labels {
		if row%256 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		fmt.Fprintf(&output, "\n| %s |", markdownCell(label))
		for _, series := range data.Series {
			value := series.Values[row]
			stars := 0
			if maxValue > 0 {
				stars = int(math.Round(math.Abs(value) / maxValue * 12))
			}
			fmt.Fprintf(&output, " %s (%s) |", strings.Repeat("*", stars), strconv.FormatFloat(value, 'g', -1, 64))
		}
	}
	return output.String(), nil
}

func embeddedChartImageMarkdown(ctx context.Context, book spreadsheet.Workbook, id string) (string, artifact.Image, error) {
	chart, data, err := embeddedChartData(ctx, book, id)
	if err != nil {
		return "", artifact.Image{}, err
	}
	scene, err := chartrender.Layout(data)
	if err != nil {
		return "", artifact.Image{}, fmt.Errorf("chart %q: %w", chart.Title, err)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, chartrender.Raster(scene)); err != nil {
		return "", artifact.Image{}, fmt.Errorf("encode chart %q: %w", chart.Title, err)
	}
	img, err := artifact.NewImage(encoded.Bytes())
	if err != nil {
		return "", artifact.Image{}, fmt.Errorf("create chart image %q: %w", chart.Title, err)
	}
	title := data.Title
	if title == "" {
		title = data.Type + " chart"
	}
	return artifact.ImageMarkdown(title, img.Name), img, nil
}

func embeddedChartData(ctx context.Context, book spreadsheet.Workbook, id string) (spreadsheet.Chart, spreadsheet.ChartData, error) {
	var chart spreadsheet.Chart
	for _, candidate := range book.Charts {
		if candidate.ID == id {
			chart = candidate
			break
		}
	}
	if chart.ID == "" {
		return spreadsheet.Chart{}, spreadsheet.ChartData{}, fmt.Errorf("workbook has no chart with ID %q", id)
	}
	data, err := spreadsheet.EvaluateChartInWorkbookContext(ctx, &book, chart)
	if err != nil {
		return chart, spreadsheet.ChartData{}, fmt.Errorf("chart %q: %w", chart.Title, err)
	}
	return chart, data, nil
}

func markdownFence(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || len(trimmed) < 3 {
		return ""
	}
	if strings.HasPrefix(trimmed, "```") {
		return "```"
	}
	if strings.HasPrefix(trimmed, "~~~") {
		return "~~~"
	}
	return ""
}

func markdownText(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\n", " "), "|", "\\|")
}

func markdownCell(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(text, "\n", "<br>"), "|", "\\|"), "`", "\\`")
}
