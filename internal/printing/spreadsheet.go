package printing

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/spreadsheet"
)

const spreadsheetPrintColumnWidth = 12

// SpreadsheetPage includes text and grid geometry in printer columns and rows.
// RowHeights includes the column header followed by the logical data rows.
type SpreadsheetPage struct {
	Lines         []string
	GridLeftCol   int
	GridTopRow    int
	ColumnWidths  []int
	RowHeights    []int
	Spans         []SpreadsheetPageSpan
	SheetIndex    int
	FirstRow      int
	FirstColumn   int
	AllRowHeights []int
}

// SpreadsheetPageSpan describes a clipped span relative to the data area.
type SpreadsheetPageSpan struct {
	FirstRow, LastRow       int
	FirstColumn, LastColumn int
}

// SpreadsheetPages formats each worksheet across columns, then down rows.
// Each page contains full cells, with row heights shared across column bands.
func SpreadsheetPages(book spreadsheet.Spreadsheet) ([]SpreadsheetPage, error) {
	return SpreadsheetPagesContext(context.Background(), book)
}

// SpreadsheetPagesContext formats workbook pages while honoring cancellation.
func SpreadsheetPagesContext(ctx context.Context, book spreadsheet.Spreadsheet) ([]SpreadsheetPage, error) {
	layout := document.DefaultLayout()
	layout.PageNumbers = document.NumbersNone
	g, err := layout.Geometry()
	if err != nil {
		return nil, err
	}
	var pages []SpreadsheetPage
	evaluator := spreadsheet.NewEvaluator(&book)
	for sheetIndex, sheet := range book.Sheets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lastRow, lastCol := 0, 0
		for r, row := range sheet.Rows {
			for c, cell := range row {
				if cell != (spreadsheet.Cell{}) {
					lastRow = max(lastRow, r+1)
					lastCol = max(lastCol, c+1)
				}
			}
		}
		spans := make(map[[2]int]spreadsheet.CellSpan)
		for _, span := range sheet.Spans {
			lastRow, lastCol = max(lastRow, span.LastRow), max(lastCol, span.LastColumn)
			for row := span.FirstRow; row <= span.LastRow; row++ {
				for col := span.FirstColumn; col <= span.LastColumn; col++ {
					spans[[2]int{row, col}] = span
				}
			}
		}
		for _, chart := range book.Charts {
			if strings.EqualFold(chart.PlaceSheet, sheet.Name) {
				lastRow, lastCol = max(lastRow, chart.PlaceRow), max(lastCol, chart.PlaceColumn)
			}
		}
		spills, err := evaluator.SpillCells(sheetIndex)
		if err != nil {
			return nil, err
		}
		for cell := range spills {
			lastRow, lastCol = max(lastRow, cell[0]), max(lastCol, cell[1])
		}
		lastRow, lastCol = max(1, lastRow), max(1, lastCol)
		rowLabelWidth := len(strconv.Itoa(lastRow))
		columnsPerPage := (g.TextCols - rowLabelWidth) / (spreadsheetPrintColumnWidth + 1)
		if columnsPerPage < 1 {
			return nil, fmt.Errorf("spreadsheet page is too narrow")
		}
		title := wrapSpreadsheetText("Sheet: "+sheet.Name, g.TextCols)
		availableRows := g.TextRows - len(title) - 2
		if availableRows < 1 {
			return nil, fmt.Errorf("sheet %q title does not fit on a page", sheet.Name)
		}
		values := make([][][]string, lastRow)
		heights := make([]int, lastRow)
		for r := 0; r < lastRow; r++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			values[r] = make([][]string, lastCol)
			heights[r] = 1
			for c := 0; c < lastCol; c++ {
				span, spanned := spans[[2]int{r + 1, c + 1}]
				if spanned && (span.FirstRow != r+1 || span.FirstColumn != c+1) {
					continue
				}
				cell := spreadsheet.Cell{}
				if r < len(sheet.Rows) && c < len(sheet.Rows[r]) {
					cell = sheet.Rows[r][c]
				}
				value := cell.Value
				if _, ok := spreadsheet.ChartAt(&book, sheet.Name, r+1, c+1); ok {
					value = "[Chart]"
				}
				_, spilled := spills[[2]int{r + 1, c + 1}]
				if cell.Formula != "" || spilled {
					result, evalErr := evaluator.Evaluate(sheetIndex, r+1, c+1)
					if evalErr != nil {
						value = "#ERR"
					} else {
						value = result.Display()
					}
				}
				width := spreadsheetPrintColumnWidth
				if spanned {
					width = (span.LastColumn-span.FirstColumn+1)*(spreadsheetPrintColumnWidth+1) - 1
					width -= (span.LastColumn-1)/columnsPerPage - (span.FirstColumn-1)/columnsPerPage
				}
				values[r][c] = wrapSpreadsheetText(value, width)
				if !spanned {
					heights[r] = max(heights[r], len(values[r][c]))
				}
			}
			if heights[r] > availableRows {
				return nil, fmt.Errorf("sheet %q row %d does not fit on a page", sheet.Name, r+1)
			}
		}
		for _, span := range sheet.Spans {
			needed := len(values[span.FirstRow-1][span.FirstColumn-1])
			for row := span.FirstRow - 1; row < span.LastRow; row++ {
				needed -= heights[row]
			}
			for row := span.FirstRow - 1; row < span.LastRow && needed > 0; row++ {
				extra := min(needed, availableRows-heights[row])
				heights[row] += extra
				needed -= extra
			}
			if needed > 0 {
				return nil, fmt.Errorf("sheet %q span at %s does not fit on pages", sheet.Name, spreadsheet.CellName(span.FirstRow, span.FirstColumn))
			}
		}
		for firstRow := 0; firstRow < lastRow; {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			endRow, used := firstRow, 0
			for endRow < lastRow && used+heights[endRow] <= availableRows {
				used += heights[endRow]
				endRow++
			}
			for firstCol := 0; firstCol < lastCol; firstCol += columnsPerPage {
				endCol := min(firstCol+columnsPerPage, lastCol)
				lines := make([]string, g.PageRows)
				padding := strings.Repeat(" ", g.LeftCols)
				line := g.TopRows
				for _, part := range title {
					lines[line] = padding + part
					line++
				}
				line++
				page := SpreadsheetPage{
					Lines: lines, GridLeftCol: g.LeftCols, GridTopRow: line,
					ColumnWidths: []int{rowLabelWidth + 1},
					RowHeights:   append([]int{1}, heights[firstRow:endRow]...),
					SheetIndex:   sheetIndex, FirstRow: firstRow, FirstColumn: firstCol,
					AllRowHeights: heights,
				}
				for _, span := range sheet.Spans {
					if span.FirstRow-1 >= endRow || span.LastRow <= firstRow ||
						span.FirstColumn-1 >= endCol || span.LastColumn <= firstCol {
						continue
					}
					page.Spans = append(page.Spans, SpreadsheetPageSpan{
						FirstRow:    max(span.FirstRow-1, firstRow) - firstRow,
						LastRow:     min(span.LastRow, endRow) - firstRow - 1,
						FirstColumn: max(span.FirstColumn-1, firstCol) - firstCol,
						LastColumn:  min(span.LastColumn, endCol) - firstCol - 1,
					})
				}
				header := strings.Repeat(" ", rowLabelWidth)
				for c := firstCol; c < endCol; c++ {
					page.ColumnWidths = append(page.ColumnWidths, spreadsheetPrintColumnWidth+1)
					name := strings.TrimRight(spreadsheet.CellName(1, c+1), "0123456789")
					header += " " + padSpreadsheetCell(name)
				}
				lines[line] = padding + header
				line++
				for r := firstRow; r < endRow; r++ {
					for sub := 0; sub < heights[r]; sub++ {
						label := strings.Repeat(" ", rowLabelWidth)
						if sub == 0 {
							label = fmt.Sprintf("%*d", rowLabelWidth, r+1)
						}
						for c := firstCol; c < endCol; {
							if span, ok := spans[[2]int{r + 1, c + 1}]; ok {
								end := min(span.LastColumn, endCol)
								offset := sub
								for previous := span.FirstRow - 1; previous < r; previous++ {
									offset += heights[previous]
								}
								value := ""
								wrapped := values[span.FirstRow-1][span.FirstColumn-1]
								if offset < len(wrapped) {
									value = wrapped[offset]
								}
								fullWidth := (span.LastColumn-span.FirstColumn+1)*(spreadsheetPrintColumnWidth+1) - 1
								fullWidth -= (span.LastColumn-1)/columnsPerPage - (span.FirstColumn-1)/columnsPerPage
								value += strings.Repeat(" ", max(0, fullWidth-runewidth.StringWidth(value)))
								start := (c - span.FirstColumn + 1) * (spreadsheetPrintColumnWidth + 1)
								start -= c/columnsPerPage - (span.FirstColumn-1)/columnsPerPage
								width := (end-c)*(spreadsheetPrintColumnWidth+1) - 1
								part := ansi.Cut(value, start, start+width)
								label += " " + part + strings.Repeat(" ", max(0, width-runewidth.StringWidth(part)))
								c = end
								continue
							}
							value := ""
							if sub < len(values[r][c]) {
								value = values[r][c][sub]
							}
							label += " " + padSpreadsheetCell(value)
							c++
						}
						lines[line] = padding + label
						line++
					}
				}
				pages = append(pages, page)
			}
			firstRow = endRow
		}
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("spreadsheet has no sheets")
	}
	return pages, nil
}

func padSpreadsheetCell(s string) string {
	return s + strings.Repeat(" ", max(0, spreadsheetPrintColumnWidth-runewidth.StringWidth(s)))
}

func wrapSpreadsheetText(text string, width int) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		var part strings.Builder
		used := 0
		for _, r := range line {
			if unicode.IsControl(r) {
				r = ' '
			}
			w := runewidth.RuneWidth(r)
			if used+w > width && part.Len() > 0 {
				lines = append(lines, part.String())
				part.Reset()
				used = 0
			}
			part.WriteRune(r)
			used += w
		}
		lines = append(lines, part.String())
	}
	return lines
}

// PrintSpreadsheet renders printer-ready text pages separated by form feeds.
func PrintSpreadsheet(book spreadsheet.Spreadsheet) (string, error) {
	pages, err := SpreadsheetPages(book)
	if err != nil {
		return "", err
	}
	formatted := make([]string, len(pages))
	for i, page := range pages {
		formatted[i] = strings.Join(page.Lines, "\n") + "\n"
	}
	return strings.Join(formatted, "\f"), nil
}
