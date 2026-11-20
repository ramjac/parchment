package cli

import (
	"bytes"
	"errors"
	"fmt"
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

func findChart(book spreadsheet.Spreadsheet, id string) (spreadsheet.Chart, error) {
	for _, chart := range book.Charts {
		if chart.ID == id {
			return chart, nil
		}
	}
	return spreadsheet.Chart{}, fmt.Errorf("chart %q not found", id)
}

func addSpreadsheetChartCommands(group *cobra.Command, streams output) {
	chart := &cobra.Command{Use: "chart", Short: "Manage range-bound spreadsheet charts"}
	group.AddCommand(chart)
	create := &cobra.Command{
		Use: "create <file> <pie|bar|line> <range>", Args: cobra.ExactArgs(3),
		Short: "Create a chart from a sheet range",
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
			r1, c1, r2, c2, err := chartRange(args[2])
			if err != nil {
				return err
			}
			title, _ := cmd.Flags().GetString("title")
			header, _ := cmd.Flags().GetBool("header")
			labels, _ := cmd.Flags().GetBool("labels")
			book, err = service.CreateChart(cmd.Context(), args[0], spreadsheet.Chart{
				Title: title, Type: args[1], Sheet: sheet, FirstRow: r1, FirstColumn: c1,
				LastRow: r2, LastColumn: c2, Header: header, Labels: labels,
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(streams.out, book.Charts[len(book.Charts)-1].ID)
			return err
		},
	}
	create.Flags().String("sheet", "", "source sheet (defaults to first)")
	create.Flags().String("title", "", "chart title")
	create.Flags().Bool("header", false, "first row contains series names")
	create.Flags().Bool("labels", false, "first column contains category labels")
	chart.AddCommand(create)
	chart.AddCommand(&cobra.Command{
		Use: "list <file>", Args: cobra.ExactArgs(1), Short: "List chart definitions",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openSpreadsheets(cmd)
			if err != nil {
				return err
			}
			book, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			for _, c := range book.Charts {
				if _, err := fmt.Fprintf(streams.out, "%s\t%s\t%s\t%s!%s:%s\n", c.ID, c.Type, c.Title,
					c.Sheet, spreadsheet.CellName(c.FirstRow, c.FirstColumn), spreadsheet.CellName(c.LastRow, c.LastColumn)); err != nil {
					return err
				}
			}
			return nil
		},
	})
	edit := &cobra.Command{
		Use: "edit <file> <chart-id>", Args: cobra.ExactArgs(2), Short: "Edit a chart definition",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openSpreadsheets(cmd)
			if err != nil {
				return err
			}
			book, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			c, err := findChart(book, args[1])
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("title") {
				c.Title, _ = cmd.Flags().GetString("title")
			}
			if cmd.Flags().Changed("type") {
				c.Type, _ = cmd.Flags().GetString("type")
			}
			if cmd.Flags().Changed("sheet") {
				c.Sheet, _ = cmd.Flags().GetString("sheet")
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
	edit.Flags().String("type", "", "pie, bar, or line")
	edit.Flags().String("sheet", "", "source sheet")
	edit.Flags().String("range", "", "source range")
	edit.Flags().Bool("header", false, "first row contains series names")
	edit.Flags().Bool("labels", false, "first column contains category labels")
	chart.AddCommand(edit)
	chart.AddCommand(&cobra.Command{
		Use: "delete <file> <chart-id>", Args: cobra.ExactArgs(2), Short: "Delete a chart definition",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openSpreadsheets(cmd)
			if err != nil {
				return err
			}
			_, err = service.DeleteChart(cmd.Context(), args[0], args[1])
			return err
		},
	})
	export := &cobra.Command{
		Use: "export <file> <chart-id>", Args: cobra.ExactArgs(2), Short: "Export a chart as SVG or PDF",
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, service, err := openSpreadsheets(cmd)
			if err != nil {
				return err
			}
			book, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			c, err := findChart(book, args[1])
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
				return repo.WritePDFExport(cmd.Context(), path, book.Path, data, force)
			}
			return repo.WriteTextExport(cmd.Context(), path, book.Path, data, force)
		},
	}
	export.Flags().String("format", "svg", "output format: svg or pdf")
	export.Flags().StringP("output", "o", "", "output file (defaults to stdout)")
	export.Flags().Bool("force", false, "overwrite an existing output file")
	chart.AddCommand(export)
}
