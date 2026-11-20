package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"example.com/parchment/internal/spreadsheet"
	"example.com/parchment/internal/tui/editor"
)

type embeddedSheetSelection struct {
	workbook        spreadsheet.Workbook
	activeSheet     int
	insertReference string
	speakerNotes    bool
}

func selectEmbeddedSheet(book spreadsheet.Workbook, sourceLine string) (embeddedSheetSelection, error) {
	book = book.Clone()
	line := strings.TrimSpace(sourceLine)
	speakerNotes := strings.HasPrefix(line, ": ")
	line = strings.TrimSpace(strings.TrimPrefix(line, ": "))
	if reference, ok := spreadsheet.ParseReference(line); ok {
		switch reference.Kind {
		case spreadsheet.SheetReferenceKind:
			index, err := spreadsheet.SheetIndexByID(book, reference.ID)
			if err != nil {
				return embeddedSheetSelection{}, err
			}
			return embeddedSheetSelection{workbook: book, activeSheet: index, speakerNotes: speakerNotes}, nil
		case spreadsheet.ChartReferenceKind:
			for _, chart := range book.Charts {
				if chart.ID != reference.ID {
					continue
				}
				for index, sheet := range book.Sheets {
					if strings.EqualFold(sheet.Name, chart.PlaceSheet) {
						return embeddedSheetSelection{workbook: book, activeSheet: index, speakerNotes: speakerNotes}, nil
					}
				}
				return embeddedSheetSelection{}, fmt.Errorf("chart %q is anchored to a missing sheet", chart.Title)
			}
			return embeddedSheetSelection{}, fmt.Errorf("workbook has no chart with ID %q", reference.ID)
		}
	}
	if strings.Contains(line, "parchment-sheet:") || strings.Contains(line, "parchment-chart:") {
		return embeddedSheetSelection{}, fmt.Errorf("malformed embedded spreadsheet reference")
	}
	if len(book.Sheets) >= spreadsheet.MaxSheets {
		return embeddedSheetSelection{}, fmt.Errorf("embedded workbook limit reached (%d sheets)", spreadsheet.MaxSheets)
	}
	name := "Sheet"
	for suffix := 1; sheetNameExists(book, name); suffix++ {
		name = fmt.Sprintf("Sheet %d", suffix)
	}
	sheet, err := spreadsheet.NewEmbeddedSheet(name)
	if err != nil {
		return embeddedSheetSelection{}, err
	}
	book.Sheets = append(book.Sheets, sheet)
	reference, err := spreadsheet.FormatReference(spreadsheet.Reference{
		Kind: spreadsheet.SheetReferenceKind,
		ID:   sheet.ID,
	})
	if err != nil {
		return embeddedSheetSelection{}, err
	}
	return embeddedSheetSelection{
		workbook: book, activeSheet: len(book.Sheets) - 1,
		insertReference: reference, speakerNotes: speakerNotes,
	}, nil
}

func sheetNameExists(book spreadsheet.Workbook, name string) bool {
	for _, sheet := range book.Sheets {
		if strings.EqualFold(sheet.Name, name) {
			return true
		}
	}
	return false
}

func sourceLineAt(model editor.Model) string {
	if model.Cursor.Line < 0 || model.Cursor.Line >= len(model.Lines) {
		return ""
	}
	return string(model.Lines[model.Cursor.Line])
}

func insertEmbeddedReference(model *editor.Model, reference, notePrefix string) {
	if notePrefix != "" {
		reference = notePrefix + reference
	}
	model.InsertString("\n\n" + reference + "\n\n")
}

func sourceWithEmbeddedReference(source string, cursor editor.Point, reference, notePrefix string) string {
	model := editor.NewWithUndoLimit(1)
	model.SetValue(source)
	model.SetCursorAtRow(cursor, 0)
	insertEmbeddedReference(&model, reference, notePrefix)
	return model.Value()
}

func renderEmbeddedTerminalChart(data spreadsheet.ChartData, width, height int, theme Theme) string {
	width = max(1, min(width, 256))
	if data.Title == "" {
		data.Title = data.Type + " chart"
	}
	if width >= 20 {
		rendered, err := renderSpreadsheetChart(data, width, min(max(height, 8), 16), theme)
		if err == nil {
			lines := strings.Split(rendered, "\n")
			for i, line := range lines {
				lines[i] = ansi.Truncate(line, width, "")
			}
			return strings.Join(lines, "\n")
		}
	}
	lines := []string{chartPreviewText(data.Title)}
	for _, series := range data.Series {
		var values []string
		for i, value := range series.Values {
			values = append(values, chartPreviewText(data.Labels[i])+"="+strconv.FormatFloat(value, 'g', -1, 64))
		}
		lines = append(lines, chartPreviewText(series.Name)+": "+strings.Join(values, ", "))
	}
	return ansi.Hardwrap(strings.Join(lines, "\n"), width, true)
}
