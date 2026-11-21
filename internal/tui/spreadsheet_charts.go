package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/spreadsheet"
)

type chartMode uint8

const (
	chartClosed chartMode = iota
	chartForm
	chartDelete
)

type spreadsheetChartUI struct {
	mode              chartMode
	formReturn        chartMode
	index             int
	stage             int
	draft             spreadsheet.Chart
	fields            [6]textinput.Model
	formPreview       string
	formText          string
	formError         string
	formSeq           uint64
	formPreviewOffset int
}

type spreadsheetChartFormPreviewMsg struct {
	seq      uint64
	rendered string
	text     string
	err      error
}

type spreadsheetChartFormPreviewRequest struct{ seq uint64 }

func (m *spreadsheetModel) selectedChart() spreadsheet.Chart {
	return m.book.Charts[m.charts.index]
}

func (m *spreadsheetModel) openChartForm(edit bool) tea.Cmd {
	ui := &m.charts
	ui.formReturn = chartClosed
	ui.mode, ui.stage = chartForm, 0
	if edit {
		ui.draft = m.selectedChart()
	} else {
		selection := m.selectedCellRange()
		ui.draft = spreadsheet.Chart{Type: "bar", Sheet: m.book.Sheets[m.sheet].Name,
			PlaceSheet: m.book.Sheets[m.sheet].Name, PlaceRow: m.row + 1, PlaceColumn: m.column + 1,
			FirstRow: selection.FirstRow, LastRow: selection.LastRow,
			FirstColumn: selection.FirstColumn, LastColumn: selection.LastColumn}
	}
	values := []string{ui.draft.Type, ui.draft.Sheet,
		spreadsheet.CellName(ui.draft.FirstRow, ui.draft.FirstColumn) + ":" +
			spreadsheet.CellName(ui.draft.LastRow, ui.draft.LastColumn),
		strconv.FormatBool(ui.draft.Header), strconv.FormatBool(ui.draft.Labels), ui.draft.Title}
	for i := range ui.fields {
		ui.fields[i] = textinput.New()
		ui.fields[i].Prompt = ""
		ui.fields[i].CharLimit = 1024
		ui.fields[i].SetValue(values[i])
		ui.fields[i].CursorEnd()
	}
	m.resizeChartForm()
	return tea.Batch(ui.fields[0].Focus(), m.renderChartFormPreview())
}

func (m *spreadsheetModel) chartFormDraft() (spreadsheet.Chart, error) {
	ui := &m.charts
	draft := ui.draft
	var err error
	draft.Type = strings.TrimSpace(ui.fields[0].Value())
	if draft.Type != "bar" && draft.Type != "line" {
		return draft, errors.New("type must be bar or line")
	}
	draft.Sheet = strings.TrimSpace(ui.fields[1].Value())
	if _, err = spreadsheet.SheetIndex(m.book, draft.Sheet); err != nil {
		return draft, err
	}
	parts := strings.Split(strings.TrimSpace(ui.fields[2].Value()), ":")
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
			draft.FirstRow, draft.FirstColumn, draft.LastRow, draft.LastColumn = r1, c1, r2, c2
		}
	}
	if err != nil {
		return draft, err
	}
	for i := 3; i <= 4; i++ {
		var flag bool
		switch strings.ToLower(strings.TrimSpace(ui.fields[i].Value())) {
		case "yes", "y", "true":
			flag = true
		case "no", "n", "false":
		default:
			return draft, errors.New("header and label options must be yes or no")
		}
		if i == 3 {
			draft.Header = flag
		} else {
			draft.Labels = flag
		}
	}
	draft.Title = strings.TrimSpace(ui.fields[5].Value())
	draft.Invalid = false
	return draft, nil
}

func (m *spreadsheetModel) stageChartForm() tea.Cmd {
	ui := &m.charts
	draft, err := m.chartFormDraft()
	if err != nil {
		m.errMessage = err.Error()
		return nil
	}
	if !m.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
		if draft.ID == "" {
			_, err := spreadsheet.AddChartToBook(book, draft)
			return err
		}
		return spreadsheet.UpdateChartInBook(book, draft)
	}) {
		return nil
	}
	if draft.ID == "" {
		ui.index = len(m.book.Charts) - 1
	}
	ui.fields[ui.stage].Blur()
	ui.formSeq++
	ui.mode = ui.formReturn
	m.status = "Chart staged; Ctrl+S saves workbook"
	return m.syncAutosave()
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
	case chartForm:
		switch key {
		case "esc":
			ui.fields[ui.stage].Blur()
			ui.formSeq++
			ui.mode = ui.formReturn
			m.errMessage = ""
			return nil
		case "ctrl+s":
			return m.stageChartForm()
		case "pgup", "pgdown":
			_, width, _, _, rows := m.chartFormSize()
			limit := max(0, len(m.chartFormPreviewLines(width))-rows)
			if key == "pgup" {
				ui.formPreviewOffset = max(0, ui.formPreviewOffset-max(1, rows))
			} else {
				ui.formPreviewOffset = min(limit, ui.formPreviewOffset+max(1, rows))
			}
			return nil
		case "tab", "shift+tab", "enter":
			ui.fields[ui.stage].Blur()
			step := 1
			if key == "shift+tab" {
				step = -1
			}
			ui.stage = (ui.stage + step + len(ui.fields)) % len(ui.fields)
			return ui.fields[ui.stage].Focus()
		case "left", "right", " ", "space":
			if ui.stage == 0 || ui.stage == 3 || ui.stage == 4 {
				value := ui.fields[ui.stage].Value()
				if ui.stage == 0 {
					if value == "bar" {
						value = "line"
					} else {
						value = "bar"
					}
				} else if value == "true" || value == "yes" {
					value = "false"
				} else {
					value = "true"
				}
				ui.fields[ui.stage].SetValue(value)
				m.errMessage = ""
				return m.renderChartFormPreview()
			}
		}
		before := ui.fields[ui.stage].Value()
		var cmd tea.Cmd
		ui.fields[ui.stage], cmd = ui.fields[ui.stage].Update(msg)
		if before != ui.fields[ui.stage].Value() {
			m.errMessage = ""
			return tea.Batch(cmd, m.renderChartFormPreview())
		}
		return cmd
	case chartDelete:
		ui.mode = chartClosed
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
	}
	return nil
}

// chartFormSize returns the inner pane sizes, excluding shared chrome.
func (m *spreadsheetModel) chartFormSize() (left, right, height, formRows, previewRows int) {
	width := max(1, m.width-4)
	height = max(1, m.height-5)
	right = width
	if width >= 49 && height >= 10 {
		left = width / 2
		right = width - left - 1
	}
	formRows = min(6, max(1, height-2))
	previewRows = max(0, height-formRows-1)
	return
}

func (m *spreadsheetModel) resizeChartForm() {
	_, width, _, _, _ := m.chartFormSize()
	for i := range m.charts.fields {
		m.charts.fields[i].SetWidth(max(1, width-11))
	}
}

func (m *spreadsheetModel) renderChartFormPreview() tea.Cmd {
	ui := &m.charts
	ui.formSeq++
	seq := ui.formSeq
	ui.formPreviewOffset = 0
	ui.formPreview, ui.formText, ui.formError = "", "", ""
	_, err := m.chartFormDraft()
	if err != nil {
		ui.formError = err.Error()
		return nil
	}
	ui.formText = "Updating preview…"
	return tea.Tick(75*time.Millisecond, func(time.Time) tea.Msg {
		return spreadsheetChartFormPreviewRequest{seq: seq}
	})
}

func (m *spreadsheetModel) prepareChartFormPreview(msg spreadsheetChartFormPreviewRequest) tea.Cmd {
	ui := &m.charts
	if ui.mode != chartForm || msg.seq != ui.formSeq {
		return nil
	}
	draft, err := m.chartFormDraft()
	if err != nil {
		ui.formError = err.Error()
		return nil
	}
	if draft.ID == "" {
		draft.ID = "preview"
	}
	book := cloneSpreadsheetState(m.book)
	_, cols, _, _, rows := m.chartFormSize()
	monochrome := compat.Profile == colorprofile.Ascii
	theme := m.theme
	return func() tea.Msg {
		result := spreadsheetChartFormPreviewMsg{seq: msg.seq}
		data, err := spreadsheet.EvaluateChart(&book, draft)
		if err != nil {
			result.err = err
			return result
		}
		result.text = spreadsheetChartFormSummary(data)
		if cols < 20 || rows < 6 || monochrome {
			return result
		}
		result.rendered, err = renderSpreadsheetChart(data, cols, rows, theme)
		if err != nil && !errors.Is(err, errSpreadsheetChartPreviewTooSmall) {
			result.err = err
		}
		return result
	}
}

func spreadsheetChartFormSummary(data spreadsheet.ChartData) string {
	var prefix string
	if data.Title != "" {
		prefix = data.Title + "\n"
	}
	labels := data.Labels
	suffix := ""
	if len(labels) > 8 {
		labels, suffix = labels[:8], ", …"
	}
	return prefix + spreadsheetChartSummary(data) + "\nCategories: " + strings.Join(labels, ", ") + suffix
}

func (m *spreadsheetModel) chartFormPreviewed(msg spreadsheetChartFormPreviewMsg) {
	ui := &m.charts
	if ui.mode != chartForm || msg.seq != ui.formSeq {
		return
	}
	if msg.err != nil {
		ui.formError = msg.err.Error()
	} else {
		ui.formPreview, ui.formText, ui.formError = msg.rendered, msg.text, ""
	}
}

func chartFormLine(line string, width int) string {
	line = ansi.Truncate(line, width, "…")
	return line + strings.Repeat(" ", max(0, width-ansi.StringWidth(line)))
}

func (m *spreadsheetModel) chartFormPreviewLines(width int) []string {
	ui := &m.charts
	if ui.formError != "" {
		return strings.Split(ansi.Wrap("Chart: "+sanitizeTerminalLine(ui.formError), width, " "), "\n")
	}
	if ui.formPreview != "" {
		return strings.Split(ui.formPreview, "\n")
	}
	return strings.Split(ansi.Wrap(sanitizeTerminalText(ui.formText), width, " "), "\n")
}

func (m *spreadsheetModel) viewChartForm() string {
	ui := &m.charts
	left, width, height, formRows, previewRows := m.chartFormSize()
	labels := []string{"Type", "Source", "Range", "Header", "Labels", "Title"}
	start := min(max(0, ui.stage-formRows+1), len(labels)-formRows)
	form := make([]string, 0, height)
	for i := start; i < start+formRows; i++ {
		marker := "  "
		if i == ui.stage {
			marker = "> "
		}
		line := marker + fmt.Sprintf("%-7s", labels[i]) + " " + ui.fields[i].View()
		form = append(form, chartFormLine(line, width))
	}
	lines := m.chartFormPreviewLines(width)
	heading := "Preview"
	if len(lines) > previewRows {
		heading += " PgUp/Dn"
	}
	form = append(form, chartFormLine(m.theme.Heading.Render(heading), width))
	offset := min(ui.formPreviewOffset, max(0, len(lines)-previewRows))
	for i := 0; i < previewRows; i++ {
		line := ""
		if i+offset < len(lines) {
			line = lines[i+offset]
		}
		form = append(form, chartFormLine(line, width))
	}
	if left == 0 {
		return strings.Join(form, "\n")
	}
	grid := *m
	grid.width = left + 4
	grid.height = height + spreadsheetChromeLines - 1
	grid.scrollToSelection()
	gridLines := strings.Split(grid.renderGrid(), "\n")
	for i := range form {
		line := ""
		if i < len(gridLines) {
			line = gridLines[i]
		}
		form[i] = chartFormLine(line, left) + "│" + form[i]
	}
	return strings.Join(form, "\n")
}

func spreadsheetChartSummary(data spreadsheet.ChartData) string {
	summary := fmt.Sprintf("%s: %d categories, %d series", data.Type, len(data.Labels), len(data.Series))
	for _, series := range data.Series {
		values := series.Values
		if len(values) > 8 {
			summary += "\n" + series.Name + ": " + fmt.Sprint(values[:8]) + " …"
		} else {
			summary += "\n" + series.Name + ": " + fmt.Sprint(values)
		}
	}
	return summary
}

func (m *spreadsheetModel) viewCharts() string {
	if m.width < 20 || m.height < spreadsheetChromeLines+1 {
		return "Terminal too small. Press Esc to return."
	}
	ui := &m.charts
	help := "y delete • n/esc cancel"
	var content string
	switch ui.mode {
	case chartForm:
		content = m.viewChartForm()
		help = "ctrl+s stage • esc cancel • tab/shift+tab field • arrows/space choice"
	case chartDelete:
		content = "Delete chart in this cell? [y/N]"
	}
	return chromeLayout(m.width, m.height, m.renderHeader(), m.theme.Muted.Render(runewidth.Truncate(help, m.width, "…")),
		content, m.renderStatus(), m.theme.BorderColor)
}
