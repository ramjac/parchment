package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"example.com/parchment/internal/imageedit"
)

func addImageCommands(root *cobra.Command, streams output) {
	group := groupCommand(&cobra.Command{Use: "image", Short: "Crop and resize standalone PNG/JPEG files"})
	for _, operation := range []string{"crop", "resize"} {
		cmd := &cobra.Command{
			Use: operation + " <input>", Short: strings.ToUpper(operation[:1]) + operation[1:] + " a PNG or JPEG image",
			Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				width, _ := cmd.Flags().GetInt("width")
				height, _ := cmd.Flags().GetInt("height")
				quality := imageedit.DefaultQuality
				x, y := 0, 0
				if operation == "crop" {
					for _, name := range []string{"x", "y", "width", "height"} {
						if !cmd.Flags().Changed(name) {
							return fmt.Errorf("crop requires --%s", name)
						}
					}
					x, _ = cmd.Flags().GetInt("x")
					y, _ = cmd.Flags().GetInt("y")
					if x < 0 || y < 0 {
						return errors.New("crop coordinates must not be negative")
					}
				} else {
					quality, _ = cmd.Flags().GetInt("quality")
					if !cmd.Flags().Changed("width") && !cmd.Flags().Changed("height") {
						return errors.New("resize requires --width or --height")
					}
				}
				for _, name := range []string{"width", "height"} {
					value, _ := cmd.Flags().GetInt(name)
					if cmd.Flags().Changed(name) && value <= 0 {
						return fmt.Errorf("--%s must be positive", name)
					}
				}
				if quality < 1 || quality > 100 {
					return errors.New("JPEG quality must be between 1 and 100")
				}
				path, _ := cmd.Flags().GetString("output")
				inPlace, _ := cmd.Flags().GetBool("in-place")
				yes, _ := cmd.Flags().GetBool("yes")
				force, _ := cmd.Flags().GetBool("force")
				if inPlace == (path != "") {
					return errors.New("choose exactly one of --output or --in-place")
				}
				if inPlace && force {
					return errors.New("--force requires --output; use --yes to confirm --in-place")
				}
				if !inPlace && yes {
					return errors.New("--yes requires --in-place")
				}
				if inPlace && !yes && !streamsInteractive(cmd.InOrStdin(), cmd.OutOrStdout()) {
					return errors.New("in-place editing requires --yes when running non-interactively")
				}
				repo, _, err := openStore()
				if err != nil {
					return err
				}
				service := imageedit.NewService(repo)
				img, err := service.Load(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				target, err := service.PrepareSave(cmd.Context(), img, path, inPlace, force)
				if err != nil {
					return err
				}
				if operation == "crop" {
					img, err = img.Crop(x, y, width, height)
				} else {
					img, err = img.Resize(width, height)
				}
				if err != nil {
					return err
				}
				if inPlace && !yes {
					confirmed, err := confirmImageEdit(cmd.InOrStdin(), cmd.OutOrStdout(), args[0])
					if err != nil || !confirmed {
						return err
					}
				}
				if err := service.Save(cmd.Context(), img, target, quality); err != nil {
					return err
				}
				drainWarnings(repo, streams)
				return nil
			},
		}
		cmd.Flags().Int("width", 0, "output width in pixels (resize: omit to preserve proportions)")
		cmd.Flags().Int("height", 0, "output height in pixels (resize: omit to preserve proportions)")
		cmd.Flags().StringP("output", "o", "", "output .png, .jpg, or .jpeg file")
		cmd.Flags().Bool("in-place", false, "replace the input image, preserving its format")
		cmd.Flags().Bool("yes", false, "confirm in-place editing without prompting")
		cmd.Flags().Bool("force", false, "overwrite a distinct existing output file")
		if operation == "crop" {
			cmd.Flags().Int("x", 0, "left edge of crop, after orientation normalization")
			cmd.Flags().Int("y", 0, "top edge of crop, after orientation normalization")
		} else {
			cmd.Flags().Int("quality", imageedit.DefaultQuality, "JPEG output quality, 1-100 (ignored for PNG)")
		}
		group.AddCommand(cmd)
	}
	root.AddCommand(group)
}

func confirmImageEdit(in io.Reader, out io.Writer, path string) (bool, error) {
	if _, err := fmt.Fprintf(out, "Replace image %q in place? [y/N] ", path); err != nil {
		return false, err
	}
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer = strings.TrimSpace(answer)
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes"), nil
}
