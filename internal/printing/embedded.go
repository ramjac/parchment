package printing

import (
	"bytes"
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
	expanded, _, errorsFound := expandEmbeddedReferences(source, book, false, nil)
	return expanded, errorsFound
}

// ExpandEmbeddedReferencesWithImages replaces chart references with generated
// PNG assets while expanding sheet references as bounded Markdown tables.
func ExpandEmbeddedReferencesWithImages(source string, book spreadsheet.Workbook) (string, []artifact.Image, []error) {
	return expandEmbeddedReferences(source, book, true, nil)
}

// ExpandEmbeddedReferencesWithChartRenderer uses render for evaluated charts
// while retaining the same reference parsing and sheet previews as other output.
func ExpandEmbeddedReferencesWithChartRenderer(source string, book spreadsheet.Workbook, render func(spreadsheet.ChartData) string) (string, []error) {
	expanded, _, errorsFound := expandEmbeddedReferences(source, book, false, render)
	return expanded, errorsFound
}

func expandEmbeddedReferences(source string, book spreadsheet.Workbook, chartsAsImages bool, render func(spreadsheet.ChartData) string) (string, []artifact.Image, []error) {
	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
	var errorsFound []error
	var images []artifact.Image
	fence := ""
	for i, line := range lines {
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
		reference, ok := spreadsheet.ParseReference(line)
		if !ok {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "![Spreadsheet](") && strings.Contains(trimmed, "parchment-sheet:") ||
				strings.HasPrefix(trimmed, "![Chart](") && strings.Contains(trimmed, "parchment-chart:") {
				err := fmt.Errorf("line %d: malformed embedded spreadsheet reference", i+1)
				errorsFound = append(errorsFound, err)
				lines[i] = "> **Embedded spreadsheet reference unavailable:** " + err.Error()
			}
			continue
		}
		var preview string
		var err error
		switch reference.Kind {
		case spreadsheet.SheetReferenceKind:
			preview, err = embeddedSheetMarkdown(book, reference.ID)
		case spreadsheet.ChartReferenceKind:
			if render != nil {
				var data spreadsheet.ChartData
				_, data, err = embeddedChartData(book, reference.ID)
				if err == nil {
					preview = render(data)
				}
			} else if chartsAsImages {
				var img artifact.Image
				preview, img, err = embeddedChartImageMarkdown(book, reference.ID)
				if err == nil {
					images = append(images, img)
				}
			} else {
				preview, err = embeddedChartMarkdown(book, reference.ID)
			}
		}
		if err != nil {
			errorsFound = append(errorsFound, fmt.Errorf("line %d: %w", i+1, err))
			lines[i] = "> **Embedded content unavailable:** " + markdownText(err.Error())
			continue
		}
		lines[i] = preview
	}
	return strings.Join(lines, "\n"), images, errorsFound
}

func embeddedSheetMarkdown(book spreadsheet.Workbook, id string) (string, error) {
	index, err := spreadsheet.SheetIndexByID(book, id)
	if err != nil {
		return "", err
	}
	sheet := book.Sheets[index]
	lastRow, lastColumn := len(sheet.Rows), 1
	for _, row := range sheet.Rows {
		lastColumn = max(lastColumn, len(row))
	}
	for _, span := range sheet.Spans {
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
	evaluator := spreadsheet.NewWorkbookEvaluator(&book)
	spills, err := evaluator.SpillCells(index)
	if err != nil {
		return "", err
	}
	for cell := range spills {
		lastRow, lastColumn = max(lastRow, cell[0]), max(lastColumn, cell[1])
	}
	rows, columns := min(lastRow, embeddedPreviewRows), min(lastColumn, embeddedPreviewColumns)
	var output strings.Builder
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
		fmt.Fprintf(&output, "\n| %d |", row)
		for column := 1; column <= columns; column++ {
			value, valueErr := embeddedCellValue(book, evaluator, spills, index, sheet.Name, row, column)
			if valueErr != nil {
				return "", valueErr
			}
			fmt.Fprintf(&output, " %s |", markdownCell(value))
		}
	}
	if rows < lastRow || columns < lastColumn {
		fmt.Fprintf(&output, "\n\n_Showing %d of %d rows and %d of %d columns._",
			rows, lastRow, columns, lastColumn)
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

func embeddedChartMarkdown(book spreadsheet.Workbook, id string) (string, error) {
	_, data, err := embeddedChartData(book, id)
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
		for _, value := range series.Values {
			maxValue = max(maxValue, math.Abs(value))
		}
	}
	for row, label := range data.Labels {
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

func embeddedChartImageMarkdown(book spreadsheet.Workbook, id string) (string, artifact.Image, error) {
	chart, data, err := embeddedChartData(book, id)
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

func embeddedChartData(book spreadsheet.Workbook, id string) (spreadsheet.Chart, spreadsheet.ChartData, error) {
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
	data, err := spreadsheet.EvaluateChartInWorkbook(&book, chart)
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
