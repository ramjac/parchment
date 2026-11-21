package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"example.com/parchment/internal/chartrender"
	"example.com/parchment/internal/pdfrender"
	"example.com/parchment/internal/spreadsheet"
)

func chartRange(value string) (int, int, int, int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, 0, 0, 0, errors.New("chart range must be a rectangle like A1:B5")
	}
	r1, c1, err := spreadsheet.CellCoordinates(parts[0])
	if err != nil {
		return 0, 0, 0, 0, err
	}
	r2, c2, err := spreadsheet.CellCoordinates(parts[1])
	if err != nil || r2 < r1 || c2 < c1 {
		return 0, 0, 0, 0, errors.New("chart range must run from top-left to bottom-right")
	}
	return r1, c1, r2, c2, nil
}

func findChart(book spreadsheet.Spreadsheet, sheet, cell string) (spreadsheet.Chart, error) {
	if sheet == "" {
		sheet = book.Sheets[0].Name
	}
	row, column, err := spreadsheet.CellCoordinates(cell)
	if err != nil {
		return spreadsheet.Chart{}, err
	}
	if chart, ok := spreadsheet.ChartAt(&book, sheet, row, column); ok {
		return chart, nil
	}
	return spreadsheet.Chart{}, fmt.Errorf("chart at %s!%s not found", sheet, cell)
}

func addSpreadsheetChartCommands(group *cobra.Command, streams output) {
	chart := &cobra.Command{Use: "chart", Short: "Edit charts in spreadsheet cells"}
	chart.PersistentFlags().String("sheet", "", "placement sheet (defaults to first)")
	group.AddCommand(chart)
	create := &cobra.Command{
		Use: "create <file> <cell> <bar|line> <range>", Args: cobra.ExactArgs(4),
		Short: "Create a chart in a cell from a source range",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openSpreadsheets(cmd)
			if err != nil {
				return err
			}
			book, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			sheet, _ := cmd.Flags().GetString("sheet")
			if sheet == "" {
				sheet = book.Sheets[0].Name
			}
			if _, err := spreadsheet.SheetIndex(book, sheet); err != nil {
				return err
			}
			row, column, err := spreadsheet.CellCoordinates(args[1])
			if err != nil {
				return err
			}
			source, _ := cmd.Flags().GetString("source-sheet")
			if source == "" {
				source = sheet
			}
			r1, c1, r2, c2, err := chartRange(args[3])
			if err != nil {
				return err
			}
			title, _ := cmd.Flags().GetString("title")
			header, _ := cmd.Flags().GetBool("header")
			labels, _ := cmd.Flags().GetBool("labels")
			book, err = service.CreateChart(cmd.Context(), args[0], spreadsheet.Chart{
				Title: title, Type: args[2], Sheet: source, PlaceSheet: sheet, PlaceRow: row, PlaceColumn: column, FirstRow: r1, FirstColumn: c1,
				LastRow: r2, LastColumn: c2, Header: header, Labels: labels,
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(streams.out, spreadsheet.CellName(row, column))
			return err
		},
	}
	create.Flags().String("source-sheet", "", "source sheet (defaults to placement sheet)")
	create.Flags().String("title", "", "chart title")
	create.Flags().Bool("header", false, "first row contains series names")
	create.Flags().Bool("labels", false, "first column contains category labels")
	chart.AddCommand(create)
	edit := &cobra.Command{
		Use: "edit <file> <cell>", Args: cobra.ExactArgs(2), Short: "Edit a chart in a cell",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openSpreadsheets(cmd)
			if err != nil {
				return err
			}
			book, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			sheet, _ := cmd.Flags().GetString("sheet")
			c, err := findChart(book, sheet, args[1])
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("title") {
				c.Title, _ = cmd.Flags().GetString("title")
			}
			if cmd.Flags().Changed("type") {
				c.Type, _ = cmd.Flags().GetString("type")
			}
			if cmd.Flags().Changed("source-sheet") {
				c.Sheet, _ = cmd.Flags().GetString("source-sheet")
			}
			if cmd.Flags().Changed("range") {
				value, _ := cmd.Flags().GetString("range")
				c.FirstRow, c.FirstColumn, c.LastRow, c.LastColumn, err = chartRange(value)
				if err != nil {
					return err
				}
			}
			if cmd.Flags().Changed("header") {
				c.Header, _ = cmd.Flags().GetBool("header")
			}
			if cmd.Flags().Changed("labels") {
				c.Labels, _ = cmd.Flags().GetBool("labels")
			}
			if _, err := spreadsheet.SheetIndex(book, c.Sheet); err != nil {
				return err
			}
			c.Invalid = false
			_, err = service.UpdateChart(cmd.Context(), args[0], c)
			return err
		},
	}
	edit.Flags().String("title", "", "chart title")
	edit.Flags().String("type", "", "bar or line")
	edit.Flags().String("source-sheet", "", "source sheet")
	edit.Flags().String("range", "", "source range")
	edit.Flags().Bool("header", false, "first row contains series names")
	edit.Flags().Bool("labels", false, "first column contains category labels")
	chart.AddCommand(edit)
	chart.AddCommand(&cobra.Command{
		Use: "delete <file> <cell>", Args: cobra.ExactArgs(2), Short: "Delete a chart from a cell",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openSpreadsheets(cmd)
			if err != nil {
				return err
			}
			book, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			sheet, _ := cmd.Flags().GetString("sheet")
			c, err := findChart(book, sheet, args[1])
			if err != nil {
				return err
			}
			_, err = service.DeleteChart(cmd.Context(), args[0], c.ID)
			return err
		},
	})
	export := &cobra.Command{
		Use: "export <file> <cell>", Args: cobra.ExactArgs(2), Short: "Export a chart as SVG or PDF",
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, service, err := openSpreadsheets(cmd)
			if err != nil {
				return err
			}
			book, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			sheet, _ := cmd.Flags().GetString("sheet")
			c, err := findChart(book, sheet, args[1])
			if err != nil {
				return err
			}
			format, _ := cmd.Flags().GetString("format")
			var data []byte
			switch format {
			case "svg":
				values, err := spreadsheet.EvaluateChart(&book, c)
				if err != nil {
					return err
				}
				scene, err := chartrender.Layout(values)
				if err != nil {
					return err
				}
				data = chartrender.SVG(scene)
			case "pdf":
				var output bytes.Buffer
				if err := pdfrender.RenderChartContext(cmd.Context(), &output, book, c); err != nil {
					return err
				}
				data = output.Bytes()
			default:
				return errors.New("chart format must be svg or pdf")
			}
			path, _ := cmd.Flags().GetString("output")
			if path == "" {
				_, err = streams.out.Write(data)
				return err
			}
			force, _ := cmd.Flags().GetBool("force")
			if format == "pdf" {
				err = repo.WritePDFExport(cmd.Context(), path, book.Path, data, false)
				if errors.Is(err, os.ErrExist) && force {
					err = repo.WritePDFExport(cmd.Context(), path, book.Path, data, true)
				}
				return err
			}
			err = repo.WriteTextExport(cmd.Context(), path, book.Path, data, false)
			if errors.Is(err, os.ErrExist) && force {
				err = repo.WriteTextExport(cmd.Context(), path, book.Path, data, true)
			}
			return err
		},
	}
	export.Flags().String("format", "svg", "output format: svg or pdf")
	export.Flags().StringP("output", "o", "", "output file (defaults to stdout)")
	export.Flags().Bool("force", false, "overwrite an existing output file")
	chart.AddCommand(export)
}
