package spreadsheet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

const MaxCharts = 128

type Chart struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	Sheet       string `json:"sheet"`
	FirstRow    int    `json:"first_row"`
	FirstColumn int    `json:"first_column"`
	LastRow     int    `json:"last_row"`
	LastColumn  int    `json:"last_column"`
	Header      bool   `json:"header,omitempty"`
	Labels      bool   `json:"labels,omitempty"`
	Invalid     bool   `json:"invalid,omitempty"`
}

type ChartSeries struct {
	Name   string
	Values []float64
}

type ChartData struct {
	Title  string
	Type   string
	Labels []string
	Series []ChartSeries
}

func validateCharts(charts []Chart) error {
	if len(charts) > MaxCharts {
		return fmt.Errorf("workbook cannot exceed %d charts", MaxCharts)
	}
	ids := make(map[string]bool, len(charts))
	for _, chart := range charts {
		if chart.ID == "" || len(chart.ID) > 128 || ids[chart.ID] || !utf8.ValidString(chart.ID) {
			return errors.New("chart IDs must be unique, nonempty UTF-8 strings")
		}
		ids[chart.ID] = true
		if !utf8.ValidString(chart.Title) || len(chart.Title) > 256 || !utf8.ValidString(chart.Sheet) ||
			strings.TrimSpace(chart.Sheet) == "" || len(chart.Sheet) > 128 {
			return errors.New("invalid chart title or sheet")
		}
		if chart.Type != "pie" && chart.Type != "bar" && chart.Type != "line" {
			return fmt.Errorf("unsupported chart type %q", chart.Type)
		}
		if chart.FirstRow < 1 || chart.FirstColumn < 1 || chart.LastRow < chart.FirstRow ||
			chart.LastColumn < chart.FirstColumn || chart.LastRow > MaxRows || chart.LastColumn > MaxColumns {
			return errors.New("chart range is outside the workbook")
		}
	}
	return nil
}

// AddChartToBook creates a chart definition without requiring currently valid data.
func AddChartToBook(book *Spreadsheet, chart Chart) (Chart, error) {
	if !chart.Invalid {
		if _, err := SheetIndex(*book, chart.Sheet); err != nil {
			return Chart{}, err
		}
	}
	if chart.ID == "" {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return Chart{}, err
		}
		chart.ID = hex.EncodeToString(id[:])
	}
	if err := validateCharts(append(append([]Chart(nil), book.Charts...), chart)); err != nil {
		return Chart{}, err
	}
	book.Charts = append(book.Charts, chart)
	return chart, nil
}

func UpdateChartInBook(book *Spreadsheet, chart Chart) error {
	if !chart.Invalid {
		if _, err := SheetIndex(*book, chart.Sheet); err != nil {
			return err
		}
	}
	for i := range book.Charts {
		if book.Charts[i].ID == chart.ID {
			next := append([]Chart(nil), book.Charts...)
			next[i] = chart
			if err := validateCharts(next); err != nil {
				return err
			}
			book.Charts = next
			return nil
		}
	}
	return errors.New("chart not found")
}

func DeleteChartFromBook(book *Spreadsheet, id string) error {
	for i, chart := range book.Charts {
		if chart.ID == id {
			book.Charts = append(book.Charts[:i], book.Charts[i+1:]...)
			return nil
		}
	}
	return errors.New("chart not found")
}

// EvaluateChart binds chart labels and numeric series to the current workbook.
func EvaluateChart(book *Spreadsheet, chart Chart) (ChartData, error) {
	if err := validateCharts([]Chart{chart}); err != nil {
		return ChartData{}, err
	}
	if chart.Invalid {
		return ChartData{}, errors.New("chart source range was deleted; edit the chart to repair it")
	}
	sheet, err := SheetIndex(*book, chart.Sheet)
	if err != nil {
		return ChartData{}, err
	}
	firstRow, firstColumn := chart.FirstRow, chart.FirstColumn
	if chart.Header {
		firstRow++
	}
	if chart.Labels {
		firstColumn++
	}
	if firstRow > chart.LastRow || firstColumn > chart.LastColumn {
		return ChartData{}, errors.New("chart source has no data")
	}
	if chart.Type == "pie" && chart.LastColumn != firstColumn {
		return ChartData{}, errors.New("pie charts require exactly one data series")
	}
	if chart.LastColumn-firstColumn+1 > 12 || (chart.LastRow-firstRow+1)*(chart.LastColumn-firstColumn+1) > 4096 {
		return ChartData{}, errors.New("chart exceeds the limit of 12 series or 4096 values")
	}
	evaluator := NewEvaluator(book)
	value := func(row, col int) (Value, error) { return evaluator.Evaluate(sheet, row, col) }
	data := ChartData{Title: chart.Title, Type: chart.Type}
	for c := firstColumn; c <= chart.LastColumn; c++ {
		name := CellName(1, c)
		if chart.Header {
			header, err := value(chart.FirstRow, c)
			if err != nil || header.Kind == ErrKind {
				return ChartData{}, fmt.Errorf("chart header %s is invalid", CellName(chart.FirstRow, c))
			}
			name = header.Display()
		}
		data.Series = append(data.Series, ChartSeries{Name: name})
	}
	total := 0.0
	for r := firstRow; r <= chart.LastRow; r++ {
		label := strconvChartRow(r)
		if chart.Labels {
			v, err := value(r, chart.FirstColumn)
			if err != nil || v.Kind == ErrKind || v.Kind == EmptyKind {
				return ChartData{}, fmt.Errorf("chart label %s is invalid", CellName(r, chart.FirstColumn))
			}
			label = v.Display()
		}
		data.Labels = append(data.Labels, label)
		for c := firstColumn; c <= chart.LastColumn; c++ {
			v, err := value(r, c)
			if err != nil || v.Kind != NumKind || math.IsNaN(v.Num) || math.IsInf(v.Num, 0) {
				return ChartData{}, fmt.Errorf("chart value %s must be numeric", CellName(r, c))
			}
			if chart.Type == "pie" && v.Num < 0 {
				return ChartData{}, fmt.Errorf("pie chart value %s cannot be negative", CellName(r, c))
			}
			total += v.Num
			data.Series[c-firstColumn].Values = append(data.Series[c-firstColumn].Values, v.Num)
		}
	}
	if chart.Type == "pie" && (total <= 0 || math.IsInf(total, 0)) {
		return ChartData{}, errors.New("pie chart total must be positive and finite")
	}
	return data, nil
}

func strconvChartRow(row int) string { return fmt.Sprintf("%d", row) }

func (s *Service) CreateChart(ctx context.Context, path string, chart Chart) (Spreadsheet, error) {
	return s.modify(ctx, path, "Create chart", func(book *Spreadsheet) error {
		_, err := AddChartToBook(book, chart)
		return err
	})
}

func (s *Service) UpdateChart(ctx context.Context, path string, chart Chart) (Spreadsheet, error) {
	return s.modify(ctx, path, "Update chart", func(book *Spreadsheet) error {
		return UpdateChartInBook(book, chart)
	})
}

func (s *Service) DeleteChart(ctx context.Context, path, id string) (Spreadsheet, error) {
	return s.modify(ctx, path, "Delete chart", func(book *Spreadsheet) error {
		return DeleteChartFromBook(book, id)
	})
}

func shiftCharts(book *Spreadsheet, sheet string, row, col int, deleting bool) {
	for i := range book.Charts {
		chart := &book.Charts[i]
		if chart.Invalid || !strings.EqualFold(chart.Sheet, sheet) {
			continue
		}
		first, last, at, limit := &chart.FirstRow, &chart.LastRow, row, MaxRows
		if col != 0 {
			first, last, at, limit = &chart.FirstColumn, &chart.LastColumn, col, MaxColumns
		}
		start, end := *first, *last
		if deleting {
			if at < start {
				start, end = start-1, end-1
			} else if at <= end {
				if start == end {
					chart.Invalid = true
					continue
				}
				end--
			}
		} else if at <= start {
			start, end = start+1, end+1
		} else if at <= end {
			end++
		}
		if start < 1 || end > limit {
			chart.Invalid = true
		} else {
			*first, *last = start, end
		}
	}
}
