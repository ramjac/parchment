package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"example.com/parchment/internal/presentation"
)

func addPresentationCommands(root *cobra.Command, streams output) {
	group := &cobra.Command{
		Use: "presentation", Aliases: []string{"pres"},
		Short: "Create and manage Markdown slide presentations",
	}
	root.AddCommand(groupCommand(group))

	create := &cobra.Command{
		Use: "create <file>", Short: "Create a presentation file", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openPresentations(cmd)
			if err != nil {
				return err
			}
			source, err := readPresentationSource(cmd)
			if err != nil {
				return err
			}
			path, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			item, err := service.Create(cmd.Context(), path, source)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(streams.out, item.Path)
			return err
		},
	}
	create.Flags().String("body-file", "", "read Markdown presentation source from a file (- for standard input)")
	group.AddCommand(create)

	group.AddCommand(&cobra.Command{
		Use: "show <file>", Short: "Show the editable Markdown source", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openPresentations(cmd)
			if err != nil {
				return err
			}
			item, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, err = io.WriteString(streams.out, item.Source)
			return err
		},
	})

	group.AddCommand(&cobra.Command{
		Use: "preview <file>", Short: "Print slide-by-slide plain-text preview", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openPresentations(cmd)
			if err != nil {
				return err
			}
			item, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			deck, err := presentation.Parse(item.Source)
			if err != nil {
				return err
			}
			_, err = io.WriteString(streams.out, presentation.Preview(deck))
			return err
		},
	})

	edit := &cobra.Command{
		Use: "edit <file>", Short: "Replace a presentation's Markdown source", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("body-file") {
				return errors.New("edit requires --body-file")
			}
			_, service, err := openPresentations(cmd)
			if err != nil {
				return err
			}
			current, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			source, err := readPresentationSource(cmd)
			if err != nil {
				return err
			}
			_, err = service.Update(cmd.Context(), current, source)
			return err
		},
	}
	edit.Flags().String("body-file", "", "read replacement Markdown source from a file (- for standard input)")
	group.AddCommand(edit)

}

func readPresentationSource(cmd *cobra.Command) (string, error) {
	path, _ := cmd.Flags().GetString("body-file")
	if path == "" {
		return "", nil
	}
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), (32<<20)+1))
	} else {
		file, openErr := os.Open(path)
		if openErr != nil {
			return "", openErr
		}
		info, statErr := file.Stat()
		if statErr != nil {
			return "", errors.Join(statErr, file.Close())
		}
		if info.Size() > 32<<20 {
			return "", errors.Join(errors.New("presentation source is larger than 32 MiB"), file.Close())
		}
		data, err = io.ReadAll(io.LimitReader(file, (32<<20)+1))
		err = errors.Join(err, file.Close())
	}
	if err != nil {
		return "", err
	}
	if len(data) > 32<<20 {
		return "", errors.New("presentation source is larger than 32 MiB")
	}
	return string(data), nil
}
