package printing

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/spreadsheet"
)

const spreadsheetPrintColumnWidth = 12

// SpreadsheetPage includes text and grid geometry in printer columns and rows.
// RowHeights includes the column header followed by the logical data rows.
type SpreadsheetPage struct {
	Lines        []string
	GridLeftCol  int
	GridTopRow   int
	ColumnWidths []int
	RowHeights   []int
}

// SpreadsheetPages formats each worksheet across columns, then down rows.
// Each page contains full cells, with row heights shared across column bands.
func SpreadsheetPages(book spreadsheet.Spreadsheet) ([]SpreadsheetPage, error) {
	layout := document.DefaultLayout()
	layout.PageNumbers = document.NumbersNone
	g, err := layout.Geometry()
	if err != nil {
		return nil, err
	}
	var pages []SpreadsheetPage
	for sheetIndex, sheet := range book.Sheets {
		lastRow, lastCol := 0, 0
		for r, row := range sheet.Rows {
			for c, cell := range row {
				if cell != (spreadsheet.Cell{}) {
					lastRow = max(lastRow, r+1)
					lastCol = max(lastCol, c+1)
				}
			}
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
			values[r] = make([][]string, lastCol)
			heights[r] = 1
			if r >= len(sheet.Rows) {
				continue
			}
			for c, cell := range sheet.Rows[r] {
				if c >= lastCol {
					break
				}
				value := cell.Value
				if cell.Formula != "" {
					result, evalErr := spreadsheet.Evaluate(&book, sheetIndex, r+1, c+1)
					var number float64
					if evalErr == nil {
						number, evalErr = result.Float64()
					}
					if evalErr != nil {
						value = "#ERR"
					} else {
						value = strconv.FormatFloat(number, 'f', -1, 64)
					}
				}
				values[r][c] = wrapSpreadsheetText(value, spreadsheetPrintColumnWidth)
				heights[r] = max(heights[r], len(values[r][c]))
			}
			if heights[r] > availableRows {
				return nil, fmt.Errorf("sheet %q row %d does not fit on a page", sheet.Name, r+1)
			}
		}
		for firstRow := 0; firstRow < lastRow; {
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
						for c := firstCol; c < endCol; c++ {
							value := ""
							if sub < len(values[r][c]) {
								value = values[r][c][sub]
							}
							label += " " + padSpreadsheetCell(value)
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
