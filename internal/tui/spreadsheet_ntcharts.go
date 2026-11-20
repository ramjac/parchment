package tui

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ntcharts/v2/spec"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/spreadsheet"
)

var errSpreadsheetChartPreviewTooSmall = errors.New("chart preview is too small")

type chartLegendEntry struct {
	text  string
	color string
}

func renderSpreadsheetChart(data spreadsheet.ChartData, width, height int, theme Theme) (string, error) {
	var chartType spec.ChartType
	switch data.Type {
	case "bar":
		if len(data.Series) != 1 {
			return "", fmt.Errorf("bar charts require exactly one data series")
		}
		chartType = spec.ChartTypeBar
	case "line":
		chartType = spec.ChartTypeLine
	default:
		return "", fmt.Errorf("unsupported chart type %q", data.Type)
	}

	if len(data.Labels) == 0 || len(data.Series) == 0 {
		return "", fmt.Errorf("chart has no data")
	}
	styles := []lipgloss.Style{theme.Accent, theme.Chrome, theme.Link, theme.Image, theme.Code, theme.Error}
	palette := make([]string, len(styles))
	for i, style := range styles {
		palette[i] = chartThemeColor(style)
	}
	labels := make([]string, len(data.Labels))
	for i, label := range data.Labels {
		labels[i] = chartPreviewText(label)
	}
	series := make([]spec.Series, len(data.Series))
	legend := make([]chartLegendEntry, len(data.Series))
	for i, chartSeries := range data.Series {
		if len(chartSeries.Values) != len(labels) {
			return "", fmt.Errorf("chart series dimensions differ")
		}
		points := make([]spec.DataPoint, len(chartSeries.Values))
		for j, value := range chartSeries.Values {
			points[j] = spec.DataPoint{X: labels[j], Y: value}
		}
		name := chartPreviewText(chartSeries.Name)
		series[i] = spec.Series{
			Name: name, Values: points, Color: palette[i%len(palette)],
		}
		legend[i] = chartLegendEntry{text: "* " + name, color: palette[i%len(palette)]}
	}
	title := chartPreviewText(data.Title)
	titleLines := 0
	if title != "" {
		titleLines = 1
	}
	legendLines := chartPreviewLegendLines(legend, width)
	plotHeight := height - titleLines - len(legendLines)
	if plotHeight < 4 {
		return "", errSpreadsheetChartPreviewTooSmall
	}

	built, err := spec.Build(spec.Spec{
		Type: chartType, Title: title, Width: width, Height: plotHeight,
		XAxis: spec.XAxis{Type: spec.XAxisCategory, Labels: labels},
		Data:  spec.Data{Series: series},
		Theme: spec.Theme{Palette: palette, Foreground: chartThemeColor(theme.Muted)},
	})
	if err != nil {
		return "", err
	}
	view, ok := built.(interface{ View() string })
	if !ok {
		return "", fmt.Errorf("ntcharts returned unsupported model %T", built)
	}
	lines := make([]string, 0, titleLines+1+len(legendLines))
	if title != "" {
		lines = append(lines, theme.Heading.Render(title))
	}
	lines = append(lines, view.View())
	lines = append(lines, legendLines...)
	return strings.Join(lines, "\n"), nil
}

func chartPreviewText(value string) string {
	return runewidth.Truncate(sanitizeTerminalLine(value), 80, "…")
}

func chartPreviewLegendLines(items []chartLegendEntry, width int) []string {
	var lines []string
	var current strings.Builder
	currentWidth := 0
	for _, item := range items {
		itemWidth := runewidth.StringWidth(item.text)
		if itemWidth > width {
			item.text = runewidth.Truncate(item.text, max(1, width), "…")
			itemWidth = runewidth.StringWidth(item.text)
		}
		if currentWidth > 0 && currentWidth+2+itemWidth > width {
			lines = append(lines, current.String())
			current.Reset()
			currentWidth = 0
		}
		if currentWidth > 0 {
			current.WriteString("  ")
			currentWidth += 2
		}
		if item.color != "" {
			current.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(item.color)).Render(item.text))
		} else {
			current.WriteString(item.text)
		}
		currentWidth += itemWidth
	}
	if currentWidth > 0 {
		lines = append(lines, current.String())
	}
	return lines
}

func chartThemeColor(style lipgloss.Style) string {
	foreground := style.GetForeground()
	if foreground == nil {
		return ""
	}
	r, g, b, _ := foreground.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", uint8(r>>8), uint8(g>>8), uint8(b>>8))
}
