package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"example.com/parchment/internal/config"
	"example.com/parchment/internal/spreadsheet"
)

func TestSpreadsheetChartUIStagesAndRecovers(t *testing.T) {
	m, _, repository, path := newSheetModel(t, [][]spreadsheet.Cell{
		{{Value: "Month"}, {Value: "Sales"}},
		{{Value: "Jan"}, {Value: "2"}},
		{{Value: "Feb"}, {Formula: "=B2*2"}},
	})
	driveSheet(t, m, sheetKey("right"))
	driveSheet(t, m, sheetKey("right"))
	driveSheet(t, m, sheetKey("g"))
	for i, value := range []string{"bar", "Sheet1", "A1:B3", "yes", "yes", "Sales"} {
		m.charts.fields[i].SetValue(value)
	}
	_, _ = m.Update(sheetKey("ctrl+s"))

	if len(m.book.Charts) != 1 || !m.dirty() || m.book.Charts[0].LastRow != 3 || m.charts.mode != chartClosed {
		t.Fatalf("chart creation did not stage the selection: %+v mode=%v stage=%v error=%q", m.book.Charts, m.charts.mode, m.charts.stage, m.errMessage)
	}
	m.recoveryStore = repository
	m.autosaveScheduler = func(session uint64) tea.Cmd {
		return func() tea.Msg { return spreadsheetAutosaveTickMsg{session: session} }
	}
	m.syncAutosave()
	saveDraft := m.saveRecovery(m.autosaveSession)
	if saveDraft == nil {
		t.Fatal("chart autosave was not scheduled")
	}
	_, _ = m.Update(saveDraft())
	draft, found, err := repository.LoadRecovery(context.Background(), path)
	if err != nil || !found {
		t.Fatalf("chart recovery draft: found=%t err=%v", found, err)
	}
	var recovered spreadsheetRecoveryData
	if err := json.Unmarshal(draft.Data, &recovered); err != nil || len(recovered.Dirty.Charts) != 1 {
		t.Fatalf("chart recovery data: %+v, %v", recovered.Dirty.Charts, err)
	}
	driveSheet(t, m, sheetKey("u"))
	if len(m.book.Charts) != 0 {
		t.Fatal("chart creation was not undoable")
	}
}

func TestRenderSpreadsheetChartUsesNTCharts(t *testing.T) {
	previous := terminalColorProfile
	t.Cleanup(func() { terminalColorProfile = previous })
	terminalColorProfile = colorprofile.TrueColor

	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "3"}}})
	m.theme = resolveTheme(config.Theme{Mode: "dark"})
	if got := chartThemeColor(m.theme.Accent); got != "#f2c66d" {
		t.Fatalf("theme accent color = %q", got)
	}
	if got := chartThemeColor(m.theme.Chrome); got != "#72bfb0" {
		t.Fatalf("theme series color = %q", got)
	}
	tests := []struct {
		name string
		data spreadsheet.ChartData
		want []string
	}{
		{
			name: "single-series bar",
			data: spreadsheet.ChartData{Type: "bar", Title: "Sales", Labels: []string{"Jan", "Feb"},
				Series: []spreadsheet.ChartSeries{{Name: "Sales", Values: []float64{2, 4}}}},
			want: []string{"Sales", "Jan", "Feb"},
		},
		{
			name: "multi-series line",
			data: spreadsheet.ChartData{Type: "line", Title: "Monthly results", Labels: []string{"Jan", "Feb"},
				Series: []spreadsheet.ChartSeries{
					{Name: "North", Values: []float64{2, 4}},
					{Name: "South", Values: []float64{5, 3}},
				}},
			want: []string{"Monthly results", "North", "South", "Jan", "Feb"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := renderSpreadsheetChart(tt.data, 50, 12, m.theme)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range tt.want {
				if !strings.Contains(output, value) {
					t.Errorf("preview %q does not contain category %q", output, value)
				}
			}
		})
	}
	lineOutput, err := renderSpreadsheetChart(tests[1].data, 50, 12, m.theme)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lineOutput, "\x1b[38;2;114;191;176m") {
		t.Fatalf("ntcharts preview did not use the semantic series color: %q", lineOutput)
	}
	output, err := renderSpreadsheetChart(spreadsheet.ChartData{
		Type: "line", Title: "Unsafe \x1b[31mtitle", Labels: []string{"Jan\x1b]0;bad\x07"},
		Series: []spreadsheet.ChartSeries{{Name: "North\x1b[0m", Values: []float64{2}}},
	}, 50, 12, m.theme)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "\x1b[31m") || strings.Contains(output, "\x1b]0;bad") {
		t.Fatalf("chart text passed through terminal control sequences: %q", output)
	}
	if _, err := renderSpreadsheetChart(tests[1].data, 50, 3, m.theme); !errors.Is(err, errSpreadsheetChartPreviewTooSmall) {
		t.Fatalf("too-small chart preview error = %v", err)
	}
	for _, data := range []spreadsheet.ChartData{
		{Type: "pie", Labels: []string{"Jan"}, Series: []spreadsheet.ChartSeries{{Values: []float64{1}}}},
		{Type: "bar", Labels: []string{"Jan"}, Series: []spreadsheet.ChartSeries{
			{Values: []float64{1}}, {Values: []float64{2}},
		}},
	} {
		if _, err := renderSpreadsheetChart(data, 50, 12, m.theme); err == nil {
			t.Fatalf("unsupported chart preview accepted: %+v", data)
		}
	}
}

func TestSpreadsheetChartFormRejectsPie(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "3"}}})
	m.column = 1
	_, _ = m.Update(sheetKey("g"))
	m.charts.fields[0].SetValue("pie")
	_, _ = m.Update(sheetKey("ctrl+s"))
	if m.charts.stage != 0 || m.errMessage != "type must be bar or line" {
		t.Fatalf("pie chart type was not rejected: stage=%d error=%q", m.charts.stage, m.errMessage)
	}
}

func TestSpreadsheetChartFormLivePreviewAndCancel(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{
		{{Value: "Month"}, {Value: "Sales"}},
		{{Value: "Jan"}, {Value: "2"}},
		{{Value: "Feb"}, {Value: "4"}},
	})
	m.column = 2
	driveSheet(t, m, sheetKey("g"))
	m.charts.fields[2].SetValue("A1:B3")
	if cmd := m.renderChartFormPreview(); cmd != nil {
		driveSheet(t, m, cmd())
	}
	if m.charts.mode != chartForm {
		t.Fatal("g did not open a new chart")
	}
	if !strings.Contains(m.charts.formError, "exactly one") {
		t.Fatalf("initial preview error = %q", m.charts.formError)
	}
	for i := 0; i < 3; i++ {
		driveSheet(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	}
	driveSheet(t, m, sheetKey("right"))
	driveSheet(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	driveSheet(t, m, sheetKey("right"))
	if m.charts.formError != "" || !strings.Contains(m.charts.formText, "2 categories") {
		t.Fatalf("updated preview = %q, error %q", m.charts.formText, m.charts.formError)
	}
	view := m.View().Content
	for _, value := range []string{"Month", "Sales", "Type", "Sheet1", "A1:B3", "Header", "Labels", "Preview"} {
		if !strings.Contains(view, value) {
			t.Errorf("form view missing %q:\n%s", value, view)
		}
	}
	driveSheet(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.charts.stage != 3 {
		t.Fatalf("shift+tab focus = %d", m.charts.stage)
	}
	driveSheet(t, m, sheetKey("esc"))
	if m.charts.mode != chartClosed || len(m.book.Charts) != 0 || m.dirty() || m.selecting || m.row != 0 || m.column != 2 {
		t.Fatal("cancelling the form changed workbook or selection")
	}
}

func TestSpreadsheetNewChartShortcutAdaptsToTerminal(t *testing.T) {
	for _, tt := range []struct {
		name        string
		width       int
		height      int
		gridVisible bool
	}{
		{name: "wide", width: 100, height: 24, gridVisible: true},
		{name: "narrow", width: 35, height: 12},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "GRIDCELL"}}})
			driveSheet(t, m, tea.WindowSizeMsg{Width: tt.width, Height: tt.height})
			m.column = 1
			driveSheet(t, m, sheetKey("g"))
			view := m.View().Content
			if m.charts.mode != chartForm || !strings.Contains(view, "Type") || !strings.Contains(view, "Preview") {
				t.Fatalf("g did not open the chart form: %q", view)
			}
			if strings.Contains(view, "GRIDCELL") != tt.gridVisible {
				t.Fatalf("unexpected grid visibility: %q", view)
			}
			if tuiScreenWidth(view) > tt.width || strings.Count(view, "\n")+1 > tt.height {
				t.Fatalf("chart form exceeds terminal: %q", view)
			}
			driveSheet(t, m, sheetKey("esc"))
			if m.charts.mode != chartClosed || m.row != 0 || m.column != 1 {
				t.Fatal("cancelling new chart did not return to the grid")
			}
		})
	}
}

func TestSpreadsheetChartFormPreviewRejectsStaleAndResizes(t *testing.T) {
	previous := terminalColorProfile
	t.Cleanup(func() { terminalColorProfile = previous })
	terminalColorProfile = colorprofile.TrueColor
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "3"}}})
	m.column = 1
	driveSheet(t, m, sheetKey("g"))
	m.charts.fields[2].SetValue("A1:A1")
	old := m.renderChartFormPreview()
	oldRender := m.prepareChartFormPreview(old().(spreadsheetChartFormPreviewRequest))
	m.charts.fields[5].SetValue("New title")
	newer := m.renderChartFormPreview()
	driveSheet(t, m, newer())
	driveSheet(t, m, oldRender())
	if m.charts.formError != "" || !strings.Contains(m.charts.formPreview, "New title") {
		t.Fatalf("stale preview replaced current preview: %q, %q", m.charts.formPreview, m.charts.formError)
	}
	driveSheet(t, m, tea.WindowSizeMsg{Width: 30, Height: 10})
	view := m.View().Content
	if tuiScreenWidth(view) > 30 || strings.Count(view, "\n")+1 > 10 || !strings.Contains(view, "Type") {
		t.Fatalf("small form does not fit: %q", view)
	}
	driveSheet(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	if m.charts.fields[5].Value() != "New title" || !strings.Contains(m.View().Content, "Preview") {
		t.Fatal("resize lost chart form state")
	}
	_, _ = m.Update(sheetKey("esc"))
	m.chartFormPreviewed(spreadsheetChartFormPreviewMsg{seq: m.charts.formSeq - 1, text: "stale"})
	if m.charts.formText == "stale" {
		t.Fatal("cancelled form accepted a late preview")
	}
}

func TestSpreadsheetChartFormStagesUnrenderableData(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "not numeric"}}})
	m.column = 1
	driveSheet(t, m, sheetKey("g"))
	m.charts.fields[2].SetValue("A1:A1")
	driveSheet(t, m, m.renderChartFormPreview()())
	if !strings.Contains(m.charts.formError, "numeric") {
		t.Fatalf("preview error = %q", m.charts.formError)
	}
	driveSheet(t, m, sheetKey("ctrl+s"))
	if len(m.book.Charts) != 1 || !m.dirty() {
		t.Fatal("unrenderable data prevented staging a valid chart")
	}
	driveSheet(t, m, sheetKey("e"))
	m.charts.fields[5].SetValue("Edited")
	driveSheet(t, m, sheetKey("ctrl+s"))
	if m.book.Charts[0].Title != "Edited" || len(m.bufferUndo) != 2 || m.charts.mode != chartClosed {
		t.Fatalf("edit not staged as one undo operation: %+v", m.book.Charts)
	}
}

func TestSpreadsheetChartFormInvalidRangeAndOtherSheet(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "3"}}})
	m.book.Sheets = append(m.book.Sheets, spreadsheet.Sheet{
		Name: "Other", Rows: [][]spreadsheet.Cell{{{Value: "42"}}},
	})
	m.column = 1
	driveSheet(t, m, sheetKey("g"))
	m.charts.fields[2].SetValue("A1:")
	if cmd := m.renderChartFormPreview(); cmd != nil || m.charts.formError == "" || m.charts.formPreview != "" {
		t.Fatal("incomplete range did not immediately clear the preview")
	}
	driveSheet(t, m, sheetKey("ctrl+s"))
	if len(m.book.Charts) != 0 || m.errMessage == "" {
		t.Fatal("invalid range was staged")
	}
	m.charts.fields[1].SetValue("Other")
	m.charts.fields[2].SetValue("A1:A1")
	cmd := m.renderChartFormPreview()
	driveSheet(t, m, cmd())
	if m.charts.formError != "" || !strings.Contains(m.charts.formText, "42") || m.sheet != 0 {
		t.Fatalf("preview did not use other sheet: %q, %q, active %d", m.charts.formText, m.charts.formError, m.sheet)
	}
	driveSheet(t, m, sheetKey("ctrl+s"))
	if len(m.book.Charts) != 1 || m.book.Charts[0].Sheet != "Other" || m.sheet != 0 {
		t.Fatal("chart source changed the active sheet")
	}
}

func TestSpreadsheetChartFormPreviewDoesNotSuspendAutosave(t *testing.T) {
	m, _, repository, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "3"}}})
	m.recoveryStore = repository
	m.autosaveScheduler = func(uint64) tea.Cmd { return nil }
	m.book.Sheets[0].Rows[0][0].Value = "4"
	m.syncAutosave()
	session := m.autosaveSession
	m.column = 1
	driveSheet(t, m, sheetKey("g"))
	driveSheet(t, m, sheetKey("right"))
	if !m.autosaveActive || m.autosaveSession != session || m.pending {
		t.Fatal("live preview interrupted autosave or blocked input")
	}
}

func TestSpreadsheetChartFormLiveTypingAndMonochrome(t *testing.T) {
	previous := terminalColorProfile
	t.Cleanup(func() { terminalColorProfile = previous })
	terminalColorProfile = colorprofile.Ascii
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "3"}}})
	m.column = 1
	driveSheet(t, m, sheetKey("g"))
	m.charts.fields[2].SetValue("A1:A1")
	driveSheet(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	driveSheet(t, m, sheetKey("X"))
	if m.charts.fields[5].Value() != "X" || m.charts.formPreview != "" ||
		!strings.Contains(m.charts.formText, "X\nbar: 1 categories") ||
		!strings.Contains(m.charts.formText, "Categories: 1") || m.charts.formError != "" {
		t.Fatalf("typing or monochrome preview failed: title=%q text=%q error=%q",
			m.charts.fields[5].Value(), m.charts.formText, m.charts.formError)
	}
}

func TestSpreadsheetChartFormSmallErrorIsScrollable(t *testing.T) {
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "1"}, {Value: "2"}}})
	driveSheet(t, m, tea.WindowSizeMsg{Width: 25, Height: 10})
	m.column = 2
	driveSheet(t, m, sheetKey("g"))
	m.charts.fields[2].SetValue("A1:B1")
	driveSheet(t, m, m.renderChartFormPreview()())
	first := m.View().Content
	if !strings.Contains(first, "Preview PgUp/Dn") || !strings.Contains(first, "Chart:") {
		t.Fatalf("scrollable error not indicated: %q", first)
	}
	driveSheet(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	driveSheet(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if !strings.Contains(m.View().Content, "series") {
		t.Fatalf("error remainder is inaccessible: %q", m.View().Content)
	}
}
