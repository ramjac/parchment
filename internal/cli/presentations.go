package cli

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"example.com/parchment/internal/artifact"
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
			repo, service, err := openPresentations(cmd)
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
			drainWarnings(repo, output{err: cmd.ErrOrStderr()})
			return finishCreate(cmd, streams, item.Path, artifact.PresentationKind)
		},
	}
	create.Flags().String("body-file", "", "read Markdown presentation source from a file (- for standard input)")
	addEditFlag(create)
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
			repo, service, err := openPresentations(cmd)
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
			if err == nil {
				drainWarnings(repo, output{err: cmd.ErrOrStderr()})
			}
			return err
		},
	}
	edit.Flags().String("body-file", "", "read replacement Markdown source from a file (- for standard input)")
	group.AddCommand(edit)

	group.AddCommand(
		newPresentationImageCmd(streams),
		newPresentationImagesCmd(streams),
		newPresentationImageExtractCmd(),
		newPresentationImageRemoveCmd(),
	)

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

func newPresentationImageCmd(streams output) *cobra.Command {
	cmd := &cobra.Command{
		Use: "image <file> <image-file>", Short: "Embed a PNG, JPEG, or GIF image at the end of a presentation", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openPresentations(cmd)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(args[1])
			if err != nil {
				return err
			}
			alt, _ := cmd.Flags().GetString("alt")
			_, name, err := service.AddImage(cmd.Context(), args[0], alt, data)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(streams.out, "%s\n", name)
			return err
		},
	}
	cmd.Flags().String("alt", "", "alternative text for the image")
	return cmd
}

func newPresentationImagesCmd(streams output) *cobra.Command {
	return &cobra.Command{
		Use: "images <file>", Short: "List embedded images", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openPresentations(cmd)
			if err != nil {
				return err
			}
			item, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			for _, img := range item.Images {
				// To compute dimensions, we can use image.DecodeConfig
				conf, format, err := image.DecodeConfig(bytes.NewReader(img.Data))
				if err != nil {
					continue
				}
				fmt.Fprintf(streams.out, "%s\t%s\t%dx%d\t%d bytes\n", img.Name, format, conf.Width, conf.Height, len(img.Data))
			}
			return nil
		},
	}
}

func newPresentationImageExtractCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "image-extract <file> <image-name>", Short: "Extract an embedded image", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openPresentations(cmd)
			if err != nil {
				return err
			}
			img, err := service.GetImage(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			outPath, _ := cmd.Flags().GetString("output")
			if outPath == "" {
				_, err = os.Stdout.Write(img.Data)
				return err
			}
			flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			if force, _ := cmd.Flags().GetBool("force"); !force {
				flags |= os.O_EXCL
			}
			f, err := os.OpenFile(outPath, flags, 0600)
			if err != nil {
				if os.IsExist(err) {
					return fmt.Errorf("file %q already exists (use --force to overwrite)", outPath)
				}
				return err
			}
			if _, err := f.Write(img.Data); err != nil {
				f.Close()
				return err
			}
			return f.Close()
		},
	}
	cmd.Flags().StringP("output", "o", "", "write to a file instead of standard output")
	cmd.Flags().Bool("force", false, "overwrite output file if it exists")
	return cmd
}

func newPresentationImageRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "image-remove <file> <image-name>", Short: "Remove an embedded image from a presentation", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openPresentations(cmd)
			if err != nil {
				return err
			}
			strip, _ := cmd.Flags().GetBool("strip-references")
			_, err = service.RemoveImage(cmd.Context(), args[0], args[1], strip)
			return err
		},
	}
	cmd.Flags().Bool("strip-references", false, "remove Markdown image tags referencing the image from the presentation source")
	return cmd
}
