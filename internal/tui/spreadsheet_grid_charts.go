package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"example.com/parchment/internal/spreadsheet"
)

type spreadsheetGridChartsMsg struct {
	seq    uint64
	charts map[[2]int][]string
	info   map[[2]int]string
}

func (m *spreadsheetModel) prepareGridCharts() tea.Cmd {
	book := cloneSpreadsheetState(m.book)
	sheet := m.sheet
	seq := m.gridChartSeq
	theme := m.theme
	return func() tea.Msg {
		result := spreadsheetGridChartsMsg{seq: seq, charts: make(map[[2]int][]string), info: make(map[[2]int]string)}
		for _, chart := range book.Charts {
			if !strings.EqualFold(chart.PlaceSheet, book.Sheets[sheet].Name) {
				continue
			}
			width, height := spreadsheetColumnWidth, 1
			if span, ok := spreadsheet.SpanAt(book.Sheets[sheet], chart.PlaceRow, chart.PlaceColumn); ok {
				width = (span.LastColumn-span.FirstColumn+1)*(spreadsheetColumnWidth+1) - 1
				height = span.LastRow - span.FirstRow + 1
			}
			lines := []string{"[Chart: expand span]"}
			coordinate := [2]int{chart.PlaceRow - 1, chart.PlaceColumn - 1}
			result.info[coordinate] = "Span more cells to display the chart"
			data, err := spreadsheet.EvaluateChart(&book, chart)
			if err != nil {
				lines = []string{"[Chart error]"}
				result.info[coordinate] = "Chart: " + err.Error()
			} else if width >= 20 && height >= 6 {
				rendered, renderErr := renderSpreadsheetChart(data, min(width, 256), min(height, 100), theme)
				if renderErr == nil {
					lines = strings.Split(rendered, "\n")
					if width > 256 || height > 100 {
						result.info[coordinate] = "Chart rendering limited to 256 columns and 100 rows"
					} else {
						delete(result.info, coordinate)
					}
				} else if renderErr != errSpreadsheetChartPreviewTooSmall {
					lines = []string{"[Chart error]"}
					result.info[coordinate] = "Chart: " + renderErr.Error()
				}
			}
			result.charts[coordinate] = lines
		}
		return result
	}
}
