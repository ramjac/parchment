package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/charmbracelet/colorprofile"

	"example.com/parchment/internal/spreadsheet"
	"example.com/parchment/internal/tui/termimg"
)

func TestSpreadsheetChartUIStagesPreviewAndExport(t *testing.T) {
	m, _, repository, path := newSheetModel(t, [][]spreadsheet.Cell{
		{{Value: "Month"}, {Value: "Sales"}},
		{{Value: "Jan"}, {Value: "2"}},
		{{Value: "Feb"}, {Formula: "=B2*2"}},
	})
	m.pdfStore = repository
	driveSheet(t, m, sheetKey("v"))
	driveSheet(t, m, sheetKey("down"))
	driveSheet(t, m, sheetKey("down"))
	driveSheet(t, m, sheetKey("right"))
	driveSheet(t, m, sheetKey("g"))
	driveSheet(t, m, sheetKey("n"))
	for _, value := range []string{"bar", "Sheet1", "A1:B3", "yes", "yes", "Sales"} {
		m.charts.input.SetValue(value)
		_, _ = m.Update(sheetKey("enter"))
	}

	if len(m.book.Charts) != 1 || !m.dirty() || m.book.Charts[0].LastRow != 3 {
		t.Fatalf("chart creation did not stage the selection: %+v mode=%v stage=%v error=%q", m.book.Charts, m.charts.mode, m.charts.stage, m.errMessage)
	}
	m.recoveryStore = repository
	m.autosaveScheduler = func(session uint64) tea.Cmd {
		return func() tea.Msg { return spreadsheetAutosaveTickMsg{session: session} }
	}
	m.syncAutosave()
	_, preview := m.Update(sheetKey("enter"))
	if preview == nil {
		t.Fatal("preview was not scheduled")
	}
	_, autosave := m.Update(preview())
	if !m.autosaveActive || autosave == nil {
		t.Fatal("chart preview did not resume autosaving")
	}
	_, saveDraft := m.Update(autosave())
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
	if m.charts.previewError != "" || !strings.Contains(m.charts.previewText, "2 categories") {
		t.Fatalf("preview failed: %s %s", m.charts.previewError, m.charts.previewText)
	}
	_, _ = m.Update(sheetKey("esc"))
	_, _ = m.Update(sheetKey("s"))
	output := filepath.Join(t.TempDir(), "chart.svg")
	m.charts.input.SetValue(output)
	_, export := m.Update(sheetKey("enter"))
	if export == nil {
		t.Fatal("export was not scheduled")
	}
	_, _ = m.Update(export())
	if data, err := os.ReadFile(output); err != nil || !strings.Contains(string(data), "<svg") {
		t.Fatalf("SVG export = %q, %v", data, err)
	}
	_, _ = m.Update(sheetKey("s"))
	m.charts.input.SetValue(path)
	_, export = m.Update(sheetKey("enter"))
	_, _ = m.Update(export())
	if m.charts.mode != chartOverwrite {
		t.Fatal("existing output did not request confirmation")
	}
	_, export = m.Update(sheetKey("y"))
	_, _ = m.Update(export())
	if !strings.Contains(m.errMessage, "cannot overwrite the source") {
		t.Fatalf("source overwrite error = %q", m.errMessage)
	}
	_, _ = m.Update(sheetKey("esc"))
	driveSheet(t, m, sheetKey("u"))
	if len(m.book.Charts) != 0 {
		t.Fatal("chart creation was not undoable")
	}
}

func TestSpreadsheetChartPreviewGraphicsCleanupAndSmallFallback(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	previous := compat.Profile
	t.Cleanup(func() { compat.Profile = previous })
	compat.Profile = colorprofile.TrueColor
	m, _, _, _ := newSheetModel(t, [][]spreadsheet.Cell{{{Value: "3"}}})
	_, err := spreadsheet.AddChartToBook(&m.book, spreadsheet.Chart{
		Type: "bar", Sheet: "Sheet1", FirstRow: 1, LastRow: 1, FirstColumn: 1, LastColumn: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = m.Update(sheetKey("g"))
	_, cmd := m.Update(sheetKey("enter"))
	_, _ = m.Update(cmd())
	view := m.View().Content
	if !strings.HasPrefix(view, termimg.ClearKittyPlacements()) || !strings.Contains(view, "\x1b_G") || !m.charts.previewGraph {
		t.Fatal("Kitty preview did not paint with graphics cleanup")
	}
	_, _ = m.Update(sheetKey("esc"))
	if !strings.HasPrefix(m.View().Content, termimg.ClearKittyPlacements()) {
		t.Fatal("leaving preview did not clear graphics")
	}
	_, _ = m.Update(tea.WindowSizeMsg{Width: 25, Height: 10})
	compat.Profile = colorprofile.Ascii
	_, cmd = m.Update(sheetKey("enter"))
	_, _ = m.Update(cmd())
	if m.charts.previewGraph || !strings.Contains(m.View().Content, "1 categories") {
		t.Fatal("small monochrome preview did not show a text summary")
	}
}
