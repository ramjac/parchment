package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/charmbracelet/colorprofile"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/chartrender"
	"example.com/parchment/internal/pdfrender"
	"example.com/parchment/internal/spreadsheet"
	"example.com/parchment/internal/tui/termimg"
)

type chartMode uint8

const (
	chartClosed chartMode = iota
	chartList
	chartForm
	chartPreview
	chartExportPath
	chartOverwrite
	chartDelete
)

type spreadsheetChartUI struct {
	mode          chartMode
	index         int
	stage         int
	draft         spreadsheet.Chart
	input         textinput.Model
	exportFormat  string
	exportPath    string
	preview       string
	previewRows   int
	previewGraph  bool
	previewError  string
	previewText   string
	clearGraphics bool
}

type spreadsheetChartPreviewMsg struct {
	seq      int
	rendered string
	rows     int
	graphics bool
	text     string
	err      error
}

type spreadsheetChartExportMsg struct {
	seq  int
	path string
	err  error
}

func (m *spreadsheetModel) selectedChart() spreadsheet.Chart {
	return m.book.Charts[m.charts.index]
}

func (m *spreadsheetModel) openChartForm(edit bool) tea.Cmd {
	ui := &m.charts
	ui.mode, ui.stage = chartForm, 0
	if edit {
		ui.draft = m.selectedChart()
	} else {
		firstRow, lastRow := m.row+1, m.row+1
		firstCol, lastCol := m.column+1, m.column+1
		if m.selecting {
			firstRow, lastRow = min(m.selectionRow, m.row)+1, max(m.selectionRow, m.row)+1
			firstCol, lastCol = min(m.selectionColumn, m.column)+1, max(m.selectionColumn, m.column)+1
		}
		ui.draft = spreadsheet.Chart{Type: "bar", Sheet: m.book.Sheets[m.sheet].Name,
			FirstRow: firstRow, LastRow: lastRow, FirstColumn: firstCol, LastColumn: lastCol}
	}
	ui.input = textinput.New()
	ui.input.CharLimit = 1024
	return m.setChartFormField()
}

func (m *spreadsheetModel) setChartFormField() tea.Cmd {
	ui := &m.charts
	c := ui.draft
	prompts := []string{"Type (bar, line, pie): ", "Sheet: ", "Range (A1:B5): ",
		"Header row (yes/no): ", "Label column (yes/no): ", "Title: "}
	values := []string{c.Type, c.Sheet, spreadsheet.CellName(c.FirstRow, c.FirstColumn) + ":" +
		spreadsheet.CellName(c.LastRow, c.LastColumn), strconv.FormatBool(c.Header), strconv.FormatBool(c.Labels), c.Title}
	ui.input.Prompt = prompts[ui.stage]
	ui.input.SetWidth(max(1, m.width-6-runewidth.StringWidth(ui.input.Prompt)))
	ui.input.SetValue(values[ui.stage])
	ui.input.CursorEnd()
	return ui.input.Focus()
}

func (m *spreadsheetModel) advanceChartForm() tea.Cmd {
	ui := &m.charts
	value := strings.TrimSpace(ui.input.Value())
	var err error
	switch ui.stage {
	case 0:
		if value != "bar" && value != "line" && value != "pie" {
			err = errors.New("type must be bar, line, or pie")
		} else {
			ui.draft.Type = value
		}
	case 1:
		_, err = spreadsheet.SheetIndex(m.book, value)
		if err == nil {
			ui.draft.Sheet = value
		}
	case 2:
		parts := strings.Split(value, ":")
		if len(parts) != 2 {
			err = errors.New("range must look like A1:B5")
		} else {
			var r1, c1, r2, c2 int
			r1, c1, err = spreadsheet.CellCoordinates(parts[0])
			if err == nil {
				r2, c2, err = spreadsheet.CellCoordinates(parts[1])
			}
			if err == nil && (r2 < r1 || c2 < c1) {
				err = errors.New("range must run from top-left to bottom-right")
			}
			if err == nil {
				ui.draft.FirstRow, ui.draft.FirstColumn, ui.draft.LastRow, ui.draft.LastColumn = r1, c1, r2, c2
			}
		}
	case 3, 4:
		var flag bool
		switch strings.ToLower(value) {
		case "yes", "y", "true":
			flag = true
		case "no", "n", "false":
		default:
			err = errors.New("enter yes or no")
		}
		if err == nil && ui.stage == 3 {
			ui.draft.Header = flag
		} else if err == nil {
			ui.draft.Labels = flag
		}
	case 5:
		ui.draft.Title = value
		ui.draft.Invalid = false
		if !m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
			if ui.draft.ID == "" {
				_, err := spreadsheet.AddChartToBook(book, ui.draft)
				return err
			}
			return spreadsheet.UpdateChartInBook(book, ui.draft)
		}) {
			return nil
		}
		if ui.draft.ID == "" {
			ui.index = len(m.book.Charts) - 1
		}
		ui.input.Blur()
		ui.mode = chartList
		m.status = "Chart staged; Ctrl+S saves workbook"
		return m.syncAutosave()
	}
	if err != nil {
		m.errMessage = err.Error()
		return nil
	}
	m.errMessage = ""
	ui.stage++
	return m.setChartFormField()
}

func (m *spreadsheetModel) updateCharts(msg tea.KeyPressMsg) tea.Cmd {
	ui := &m.charts
	key := msg.String()
	if key == "ctrl+c" {
		m.cancelPending()
		return tea.Quit
	}
	if m.pending {
		if key == "esc" {
			m.cancelPending()
			m.status = "Chart operation cancelled"
			return m.syncAutosave()
		}
		return nil
	}
	switch ui.mode {
	case chartForm, chartExportPath:
		switch key {
		case "esc":
			ui.input.Blur()
			ui.mode = chartList
			m.errMessage = ""
			return nil
		case "enter":
			if ui.mode == chartForm {
				return m.advanceChartForm()
			}
			ui.exportPath = strings.TrimSpace(ui.input.Value())
			return m.exportChart(false)
		}
		var cmd tea.Cmd
		ui.input, cmd = ui.input.Update(msg)
		return cmd
	case chartOverwrite:
		switch key {
		case "y", "Y":
			return m.exportChart(true)
		case "n", "N", "esc", "enter":
			ui.mode = chartList
			m.status = "Chart export cancelled"
		}
		return nil
	case chartDelete:
		ui.mode = chartList
		if key == "y" || key == "Y" {
			id := m.selectedChart().ID
			if m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
				return spreadsheet.DeleteChartFromBook(book, id)
			}) {
				ui.index = min(ui.index, max(0, len(m.book.Charts)-1))
				m.status = "Chart deleted; Ctrl+S saves workbook"
				return m.syncAutosave()
			}
		}
		return nil
	case chartPreview:
		if key == "esc" || key == "q" {
			ui.mode = chartList
			ui.preview = ""
			m.errMessage = ""
		}
		return nil
	}
	switch key {
	case "esc", "q":
		ui.mode = chartClosed
		m.status, m.errMessage = "", ""
	case "up", "k":
		ui.index = max(0, ui.index-1)
	case "down", "j":
		ui.index = min(max(0, len(m.book.Charts)-1), ui.index+1)
	case "n":
		return m.openChartForm(false)
	case "ctrl+s":
		return m.saveWorkbook()
	case "e":
		if len(m.book.Charts) > 0 {
			return m.openChartForm(true)
		}
	case "d":
		if len(m.book.Charts) > 0 {
			ui.mode = chartDelete
		}
	case "enter":
		if len(m.book.Charts) > 0 {
			ui.mode = chartPreview
			return m.renderChartPreview()
		}
	case "s", "p":
		if len(m.book.Charts) > 0 {
			ui.exportFormat = "svg"
			if key == "p" {
				ui.exportFormat = "pdf"
			}
			ui.mode = chartExportPath
			ui.input = textinput.New()
			ui.input.CharLimit = 64 << 10
			ui.input.Prompt = strings.ToUpper(ui.exportFormat) + " output path: "
			ui.input.SetWidth(max(1, m.width-6-runewidth.StringWidth(ui.input.Prompt)))
			ui.input.SetValue(strings.TrimSuffix(m.path, filepath.Ext(m.path)) + "-chart." + ui.exportFormat)
			ui.input.CursorEnd()
			return ui.input.Focus()
		}
	}
	return nil
}

func (m *spreadsheetModel) renderChartPreview() tea.Cmd {
	ui := &m.charts
	ui.preview, ui.previewError, ui.previewText = "", "", ""
	if len(m.book.Charts) == 0 {
		return nil
	}
	m.stopAutosave()
	ctx, seq := m.startOperation()
	book, chart := cloneSpreadsheetState(m.book), m.selectedChart()
	cols, rows := min(70, m.width-6), min(16, m.height-9)
	protocol := termimg.DetectProtocol()
	monochrome := compat.Profile == colorprofile.Ascii
	m.status = "Preparing chart…"
	if protocol == termimg.ProtocolKitty {
		ui.clearGraphics = true
	}
	return func() tea.Msg {
		result := spreadsheetChartPreviewMsg{seq: seq}
		if err := ctx.Err(); err != nil {
			result.err = err
			return result
		}
		data, err := spreadsheet.EvaluateChart(&book, chart)
		if err != nil {
			result.err = err
			return result
		}
		result.text = fmt.Sprintf("%s: %d categories, %d series", data.Type, len(data.Labels), len(data.Series))
		if cols < 20 || rows < 4 || monochrome {
			for _, series := range data.Series {
				values := series.Values
				if len(values) > 8 {
					result.text += "\n" + series.Name + ": " + fmt.Sprint(values[:8]) + " …"
				} else {
					result.text += "\n" + series.Name + ": " + fmt.Sprint(values)
				}
			}
			return result
		}
		scene, err := chartrender.Layout(data)
		if err != nil {
			result.err = err
			return result
		}
		result.rendered, result.rows = termimg.RenderPixels(chartrender.Raster(scene), cols, rows, protocol)
		result.graphics = termimg.IsGraphicsOutput(protocol, result.rendered)
		if result.graphics {
			result.rendered = strings.TrimSuffix(strings.TrimPrefix(result.rendered, "\x1b[s"), "\x1b[u")
		}
		result.err = ctx.Err()
		return result
	}
}

func (m *spreadsheetModel) chartPreviewed(msg spreadsheetChartPreviewMsg) tea.Cmd {
	if msg.seq != m.seq {
		return nil
	}
	m.finishOperation()
	m.status = ""
	ui := &m.charts
	if msg.err != nil {
		ui.previewError = msg.err.Error()
	} else {
		ui.preview, ui.previewRows, ui.previewGraph, ui.previewText = msg.rendered, msg.rows, msg.graphics, msg.text
	}
	return m.syncAutosave()
}

func (m *spreadsheetModel) exportChart(overwrite bool) tea.Cmd {
	ui := &m.charts
	path, err := filepath.Abs(ui.exportPath)
	if ui.exportPath == "" || err != nil {
		m.errMessage = "Chart output path is required"
		return nil
	}
	if m.pdfStore == nil {
		m.errMessage = "Chart export storage is unavailable"
		return nil
	}
	book, chart, format := cloneSpreadsheetState(m.book), m.selectedChart(), ui.exportFormat
	store, source := m.pdfStore, m.path
	m.stopAutosave()
	ctx, seq := m.startOperation()
	m.status, m.errMessage = "Exporting chart…", ""
	return func() tea.Msg {
		result := spreadsheetChartExportMsg{seq: seq, path: path}
		var data []byte
		if format == "pdf" {
			var output bytes.Buffer
			result.err = pdfrender.RenderChartContext(ctx, &output, book, chart)
			data = output.Bytes()
		} else {
			values, err := spreadsheet.EvaluateChart(&book, chart)
			if err == nil {
				var scene chartrender.Scene
				scene, err = chartrender.Layout(values)
				if err == nil {
					data = chartrender.SVG(scene)
				}
			}
			result.err = err
		}
		if result.err == nil {
			if format == "pdf" {
				result.err = store.WritePDFExport(ctx, path, source, data, overwrite)
			} else if writer, ok := store.(interface {
				WriteTextExport(context.Context, string, string, []byte, bool) error
			}); ok {
				result.err = writer.WriteTextExport(ctx, path, source, data, overwrite)
			} else {
				result.err = errors.New("SVG export storage is unavailable")
			}
		}
		return result
	}
}

func (m *spreadsheetModel) chartExported(msg spreadsheetChartExportMsg) tea.Cmd {
	if msg.seq != m.seq {
		return nil
	}
	m.finishOperation()
	switch {
	case errors.Is(msg.err, fs.ErrExist):
		m.charts.mode, m.charts.exportPath = chartOverwrite, msg.path
		m.status = ""
	case msg.err != nil:
		m.charts.mode = chartList
		m.status, m.errMessage = "", "Chart export failed: "+msg.err.Error()
	default:
		m.charts.mode = chartList
		m.status, m.errMessage = "Chart exported to "+msg.path, ""
	}
	return m.syncAutosave()
}

func (m *spreadsheetModel) clearChartGraphics(output string) string {
	if m.charts.clearGraphics {
		return termimg.ClearKittyPlacements() + output
	}
	return output
}

func (m *spreadsheetModel) viewCharts() string {
	if m.width < 20 || m.height < spreadsheetChromeLines+1 {
		return m.clearChartGraphics("Terminal too small. Press Esc to return.")
	}
	ui := &m.charts
	help := "n new • e edit • enter preview • s SVG • p PDF • d delete • ctrl+s save • esc back"
	var content string
	switch ui.mode {
	case chartForm, chartExportPath:
		content = ui.input.View()
		help = "enter continue • esc cancel"
	case chartOverwrite:
		content = "Output exists. Overwrite? [y/N]"
	case chartDelete:
		content = "Delete selected chart? [y/N]"
	case chartPreview:
		help = "esc back"
		for i, line := range strings.Split(ui.previewText, "\n") {
			if i > 0 {
				content += "\n"
			}
			content += runewidth.Truncate(sanitizeTerminalLine(line), max(1, m.width-6), "…")
		}
		if ui.previewError != "" {
			content = "Chart error: " + sanitizeTerminalLine(ui.previewError)
		} else if ui.preview != "" {
			if ui.previewGraph {
				content += "\n" + strings.Repeat("\n", ui.previewRows)
			} else {
				content += "\n" + ui.preview
			}
		}
	default:
		if len(m.book.Charts) == 0 {
			content = "No charts. Press n to create one from the current selection."
		}
		visible := max(1, m.height-6)
		start := max(0, ui.index-visible+1)
		for i := start; i < len(m.book.Charts) && i < start+visible; i++ {
			chart := m.book.Charts[i]
			marker := "  "
			if i == ui.index {
				marker = "> "
			}
			line := fmt.Sprintf("%s%s [%s] %s!%s:%s", marker, chart.Title, chart.Type, chart.Sheet,
				spreadsheet.CellName(chart.FirstRow, chart.FirstColumn), spreadsheet.CellName(chart.LastRow, chart.LastColumn))
			if chart.Invalid {
				line += " (invalid source)"
			}
			content += runewidth.Truncate(sanitizeTerminalLine(line), max(1, m.width-6), "…") + "\n"
		}
	}
	output := chromeLayout(m.width, m.height, m.renderHeader(), m.theme.Muted.Render(runewidth.Truncate(help, m.width, "…")),
		content, m.renderStatus(), m.theme.BorderColor)
	output = m.clearChartGraphics(output)
	if ui.mode == chartPreview && ui.previewGraph && ui.preview != "" && ui.previewError == "" {
		// Header, help, border, and summary precede the image inside the pane.
		output += "\x1b[s\x1b[6;3H" + ui.preview + "\x1b[u"
	}
	return output
}
