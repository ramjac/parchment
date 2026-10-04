package cli

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"example.com/parchment/internal/spreadsheet"
)

func addSpreadsheetCommands(root *cobra.Command, streams output) {
	group := &cobra.Command{Use: "spreadsheet", Aliases: []string{"sheet"}, Short: "Create and manage text spreadsheets"}
	root.AddCommand(group)

	group.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List spreadsheets", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, service, err := openSpreadsheets(cmd)
			if err != nil {
				return err
			}
			books, err := service.List(cmd.Context())
			if err != nil {
				return err
			}
			for _, book := range books {
				sheet := book.Sheets[0]
				if _, err := fmt.Fprintf(streams.out, "%s\t%s\t%d sheets\t%d x %d\n",
					book.ID, strconv.Quote(book.Title), len(book.Sheets), len(sheet.Rows), len(sheet.Rows[0])); err != nil {
					return err
				}
			}
			return nil
		},
	})

	create := &cobra.Command{
		Use: "create <title>", Short: "Create a spreadsheet", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openSpreadsheets(cmd)
			if err != nil {
				return err
			}
			path, _ := cmd.Flags().GetString("csv-file")
			var rows [][]spreadsheet.Cell
			if path != "" {
				file, err := os.Open(path)
				if err != nil {
					return err
				}
				var readErr error
				rows, readErr = readSpreadsheetCSV(file, 32<<20)
				closeErr := file.Close()
				if readErr != nil {
					return errors.Join(readErr, closeErr)
				}
				if closeErr != nil {
					return fmt.Errorf("close CSV input: %w", closeErr)
				}
			}
			book, err := service.Create(cmd.Context(), args[0], rows)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(streams.out, book.ID)
			return err
		},
	}
	create.Flags().String("csv-file", "", "initialize cells from a CSV file")
	group.AddCommand(create)

	group.AddCommand(&cobra.Command{
		Use: "show <id>", Short: "Show the complete text workbook", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openSpreadsheets(cmd)
			if err != nil {
				return err
			}
			book, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			data, err := spreadsheet.Encode(book)
			if err != nil {
				return err
			}
			_, err = streams.out.Write(data)
			return err
		},
	})

	cell := &cobra.Command{
		Use: "cell <id> <A1-reference> [value-or-formula]", Short: "Read or set a cell",
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openSpreadsheets(cmd)
			if err != nil {
				return err
			}
			row, column, err := spreadsheet.CellCoordinates(args[1])
			if err != nil {
				return err
			}
			book, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			sheetName, _ := cmd.Flags().GetString("sheet")
			sheetIndex, err := spreadsheet.SheetIndex(book, sheetName)
			if err != nil {
				return err
			}
			if len(args) == 2 {
				if cmd.Flags().Changed("formula") {
					return errors.New("--formula is only valid when setting a cell")
				}
				cellValue := spreadsheet.Cell{}
				if row <= len(book.Sheets[sheetIndex].Rows) && column <= len(book.Sheets[sheetIndex].Rows[row-1]) {
					cellValue = book.Sheets[sheetIndex].Rows[row-1][column-1]
				}
				if cellValue.Formula != "" {
					value, err := spreadsheet.Evaluate(&book, sheetIndex, row, column)
					if err != nil {
						return err
					}
					_, err = fmt.Fprintf(streams.out, "%s\t= %s\t%s\n", args[1],
						strings.TrimPrefix(cellValue.Formula, "="), strconv.FormatFloat(value, 'f', -1, 64))
					return err
				}
				_, err = fmt.Fprintln(streams.out, cellValue.Value)
				return err
			}
			value := args[2]
			formula, _ := cmd.Flags().GetBool("formula")
			cellValue := spreadsheet.Cell{Value: value}
			if formula {
				cellValue = spreadsheet.Cell{Formula: value}
			}
			_, err = service.SetCellInSheet(cmd.Context(), args[0], sheetName, row, column, cellValue)
			return err
		},
	}
	cell.Flags().Bool("formula", false, "store the supplied expression as a formula")
	cell.Flags().String("sheet", "Sheet1", "sheet to read or edit")
	group.AddCommand(cell)

	for _, operation := range []struct {
		name string
		run  func(*cobra.Command, string, string, int) error
	}{
		{name: "insert-row", run: func(cmd *cobra.Command, id, sheet string, index int) error {
			_, service, err := openSpreadsheets(cmd)
			if err != nil {
				return err
			}
			_, err = service.InsertRowInSheet(cmd.Context(), id, sheet, index)
			return err
		}},
		{name: "insert-column", run: func(cmd *cobra.Command, id, sheet string, index int) error {
			_, service, err := openSpreadsheets(cmd)
			if err != nil {
				return err
			}
			_, err = service.InsertColumnInSheet(cmd.Context(), id, sheet, index)
			return err
		}},
	} {
		operation := operation
		command := &cobra.Command{
			Use: operation.name + " <id> <index>", Short: "Insert a spreadsheet " + strings.TrimPrefix(operation.name, "insert-"),
			Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				index, err := strconv.Atoi(args[1])
				if err != nil {
					return fmt.Errorf("invalid index %q: %w", args[1], err)
				}
				sheet, _ := cmd.Flags().GetString("sheet")
				return operation.run(cmd, args[0], sheet, index)
			},
		}
		command.Flags().String("sheet", "Sheet1", "sheet to edit")
		group.AddCommand(command)
	}
	group.AddCommand(&cobra.Command{
		Use: "add-sheet <id> <name>", Short: "Add a named worksheet", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openSpreadsheets(cmd)
			if err != nil {
				return err
			}
			_, err = service.AddSheet(cmd.Context(), args[0], args[1])
			return err
		},
	})

	deleteCommand := &cobra.Command{
		Use: "delete <id>", Short: "Permanently delete a spreadsheet", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			if !yes {
				return errors.New("deletion requires --yes")
			}
			_, service, err := openSpreadsheets(cmd)
			if err != nil {
				return err
			}
			return service.Delete(cmd.Context(), args[0])
		},
	}
	deleteCommand.Flags().Bool("yes", false, "confirm permanent deletion")
	group.AddCommand(deleteCommand)
}

func readSpreadsheetCSV(input io.Reader, maxBytes int64) ([][]spreadsheet.Cell, error) {
	limited := &io.LimitedReader{R: input, N: maxBytes + 1}
	reader := csv.NewReader(limited)
	reader.FieldsPerRecord = -1
	var rows [][]spreadsheet.Cell
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			if limited.N == 0 {
				return nil, fmt.Errorf("CSV input is larger than %d bytes", maxBytes)
			}
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read CSV input: %w", err)
		}
		if limited.N == 0 {
			return nil, fmt.Errorf("CSV input is larger than %d bytes", maxBytes)
		}
		if len(rows) >= spreadsheet.MaxRows {
			return nil, fmt.Errorf("CSV input cannot exceed %d rows", spreadsheet.MaxRows)
		}
		if len(record) > spreadsheet.MaxColumns {
			return nil, fmt.Errorf("CSV input cannot exceed %d columns", spreadsheet.MaxColumns)
		}
		row := make([]spreadsheet.Cell, len(record))
		for i, value := range record {
			row[i] = spreadsheet.Cell{Value: value}
		}
		rows = append(rows, row)
	}
}
